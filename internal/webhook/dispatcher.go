package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// Queue is the outbox as the dispatcher uses it. *store.Store is the
// implementation; declaring it here keeps the delivery logic testable without a
// database, and keeps this package from depending on the whole store surface.
type Queue interface {
	ClaimDueDeliveries(ctx context.Context, limit int, lease time.Duration) ([]store.Delivery, error)
	MarkDelivered(ctx context.Context, id int64) error
	RescheduleDelivery(ctx context.Context, id int64, cause string, at time.Time) error
	DeadLetterDelivery(ctx context.Context, id int64, cause string) error
}

var _ Queue = (*store.Store)(nil)

type Config struct {
	URL    string
	Secret string

	// MaxAttempts bounds retries. Past it a delivery is parked rather than
	// retried forever, so one unreachable receiver can't starve the queue.
	MaxAttempts int

	Poll        time.Duration
	Batch       int
	Timeout     time.Duration
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// Sized for provisioning traffic: changes arrive in bursts and a receiver that
// is down is usually down for minutes, not milliseconds. Six attempts on a
// doubling backoff from 5s spans roughly five minutes of outage.
const (
	defaultMaxAttempts = 6
	defaultPoll        = 5 * time.Second
	defaultBatch       = 20
	defaultTimeout     = 10 * time.Second
	defaultBaseBackoff = 5 * time.Second
	defaultMaxBackoff  = 5 * time.Minute
)

// ConfigFromEnv reads the webhook settings. It reports false when
// SCIM_WEBHOOK_URL is unset, which is how change delivery stays optional — a
// deployment that doesn't want webhooks shouldn't have to configure a secret
// for a dispatcher that never runs.
//
// A URL without a secret is an error rather than an unsigned send: a receiver
// with no way to tell a real event from a forged one is worse than no webhook.
func ConfigFromEnv() (Config, bool, error) {
	raw := strings.TrimSpace(os.Getenv("SCIM_WEBHOOK_URL"))
	if raw == "" {
		return Config{}, false, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, false, fmt.Errorf("SCIM_WEBHOOK_URL is not a valid URL: %w", err)
	}

	// Events carry user attributes, so plaintext is opt-in and meant for a local
	// receiver during development. The signature protects authenticity, never
	// confidentiality.
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && os.Getenv("SCIM_WEBHOOK_ALLOW_HTTP") == "1":
		// Scheme, host and path only. url.Redacted masks userinfo but keeps the
		// query, which can carry a capability token — and this line is written
		// to a log file.
		slog.Warn("webhook endpoint is plaintext; change events carry user attributes",
			"url", u.Scheme+"://"+u.Host+u.Path)
	case u.Scheme == "http":
		return Config{}, false, errors.New("SCIM_WEBHOOK_URL must be https (set SCIM_WEBHOOK_ALLOW_HTTP=1 for a local receiver)")
	default:
		return Config{}, false, fmt.Errorf("SCIM_WEBHOOK_URL scheme %q is not supported", u.Scheme)
	}

	secret := strings.TrimSpace(os.Getenv("SCIM_WEBHOOK_SECRET"))
	if len(secret) < minSecretLen {
		return Config{}, false, fmt.Errorf("SCIM_WEBHOOK_SECRET must be at least %d characters (generate one with: openssl rand -hex 32)", minSecretLen)
	}

	return Config{
		URL:         raw,
		Secret:      secret,
		MaxAttempts: envInt("SCIM_WEBHOOK_MAX_ATTEMPTS", defaultMaxAttempts),
		Poll:        defaultPoll,
		Batch:       defaultBatch,
		Timeout:     defaultTimeout,
		BaseBackoff: defaultBaseBackoff,
		MaxBackoff:  defaultMaxBackoff,
	}, true, nil
}

func envInt(name string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

type Dispatcher struct {
	queue Queue
	cfg   Config
	http  *http.Client
	now   func() time.Time
}

func New(q Queue, cfg Config) *Dispatcher {
	cfg.MaxAttempts = orDefault(cfg.MaxAttempts, defaultMaxAttempts)
	cfg.Batch = orDefault(cfg.Batch, defaultBatch)
	cfg.Poll = orDefaultDuration(cfg.Poll, defaultPoll)
	cfg.Timeout = orDefaultDuration(cfg.Timeout, defaultTimeout)
	cfg.BaseBackoff = orDefaultDuration(cfg.BaseBackoff, defaultBaseBackoff)
	cfg.MaxBackoff = orDefaultDuration(cfg.MaxBackoff, defaultMaxBackoff)

	return &Dispatcher{
		queue: q,
		cfg:   cfg,
		http: &http.Client{
			Timeout: cfg.Timeout,
			// Redirects are not followed: the payload carries user attributes
			// and is signed for the configured endpoint. Following a 302 would
			// hand both to a host the operator never configured.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

// leaseFor sizes a claim's lease to the whole batch rather than to one
// delivery, because drain sends the batch sequentially: the last row can wait
// for every row ahead of it before its own attempt starts.
//
// Leasing only Poll+Timeout would make the lease shorter than the work it
// covers as soon as a batch has more than one slow row, and the rows still
// queued behind the current send would come due again — claimed by a second
// dispatcher, or by this one on its next tick. That double-POSTs the event and,
// because the attempt is counted at claim time, burns two attempts per real
// retry, so a delivery dead-letters at roughly half its configured budget.
//
// The trade-off is that a dispatcher which dies mid-batch leaves its rows
// waiting out the full lease. Delaying a retry is the cheaper failure.
func (d *Dispatcher) leaseFor() time.Duration {
	return d.cfg.Poll + time.Duration(d.cfg.Batch)*d.cfg.Timeout
}

// Run drains the queue until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	slog.Info("webhook dispatcher started", "poll", d.cfg.Poll, "max_attempts", d.cfg.MaxAttempts)

	ticker := time.NewTicker(d.cfg.Poll)
	defer ticker.Stop()

	for {
		if err := d.drain(ctx); err != nil && ctx.Err() == nil {
			slog.Error("drain webhook queue", "error", err)
		}

		select {
		case <-ctx.Done():
			slog.Info("webhook dispatcher stopped")
			return
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) error {
	claimed, err := d.queue.ClaimDueDeliveries(ctx, d.cfg.Batch, d.leaseFor())
	if err != nil {
		return err
	}

	for _, del := range claimed {
		if ctx.Err() != nil {
			// The lease expires on its own, so an unsent claim returns to the
			// queue rather than needing to be released on the way out.
			return ctx.Err()
		}
		d.attempt(ctx, del)
	}
	return nil
}

// attempt sends one delivery and records the outcome. Errors from the queue are
// logged rather than returned: the delivery already happened or already failed,
// and the lease means an unrecorded outcome is retried, not lost.
func (d *Dispatcher) attempt(ctx context.Context, del store.Delivery) {
	err := d.send(ctx, del)
	if err == nil {
		if err := d.queue.MarkDelivered(ctx, del.ID); err != nil {
			slog.Error("record webhook delivery", "delivery_id", del.ID, "error", err)
		}
		return
	}

	var perm *permanentError
	final := errors.As(err, &perm) || del.Attempts >= d.cfg.MaxAttempts

	if final {
		slog.Error("webhook delivery dead-lettered",
			"delivery_id", del.ID, "event", del.EventType, "attempts", del.Attempts, "error", err)

		if err := d.queue.DeadLetterDelivery(ctx, del.ID, err.Error()); err != nil {
			slog.Error("record webhook dead letter", "delivery_id", del.ID, "error", err)
		}
		return
	}

	at := d.now().Add(d.backoff(del.Attempts))
	slog.Warn("webhook delivery failed, will retry",
		"delivery_id", del.ID, "event", del.EventType, "attempts", del.Attempts, "next_attempt", at, "error", err)

	if err := d.queue.RescheduleDelivery(ctx, del.ID, err.Error(), at); err != nil {
		slog.Error("reschedule webhook delivery", "delivery_id", del.ID, "error", err)
	}
}

func (d *Dispatcher) send(ctx context.Context, del store.Delivery) error {
	ts := d.now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.URL, bytes.NewReader(del.Payload))
	if err != nil {
		// A URL that won't build a request won't build one next time either.
		return permanent(fmt.Errorf("build request: %w", err))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SCIMage")
	req.Header.Set(EventHeader, del.EventType)
	req.Header.Set(DeliveryHeader, strconv.FormatInt(del.ID, 10))
	req.Header.Set(SignatureHeader, Sign(d.cfg.Secret, ts, del.ID, del.EventType, del.Payload))

	resp, err := d.http.Do(req)
	if err != nil {
		return requestFailure(req, err)
	}
	defer resp.Body.Close()

	// Read a bounded prefix: the body goes into last_error, and a hostile or
	// broken receiver shouldn't be able to stream unbounded data into a column.
	// Draining it also lets the connection be reused. A read error here only
	// costs detail in a diagnostic string, so it is ignored rather than masking
	// the status code that actually decides the outcome.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // diagnostic only

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	failure := fmt.Errorf("receiver returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	if retryable(resp.StatusCode) {
		return failure
	}
	return permanent(failure)
}

// maxErrorBody is what gets read from a failed response for the error message.
const maxErrorBody = 2 << 10

// requestFailure reduces a transport error to the host it was talking to.
// url.Error stringifies the full endpoint including its query, which can carry
// a capability token, and this error is both logged and stored in last_error.
func requestFailure(req *http.Request, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("post to receiver %s: %w", req.URL.Host, err)
}

// retryable reports whether another attempt could plausibly land. A 4xx other
// than 408 and 429 means the receiver understood the request and rejected it —
// retrying sends identical bytes to the same endpoint for the same answer, so
// the delivery goes straight to the dead-letter queue where someone can see it.
// A 3xx arrives here only because redirects are not followed, and pointing the
// endpoint somewhere else is the operator's job, not a retry's.
func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

// backoff doubles from BaseBackoff and caps at MaxBackoff, with up to 20%
// jitter so a batch that failed together doesn't come back together.
//
// The doubling stops at the cap rather than shifting by the attempt count and
// clamping after: a large BaseBackoff shifted far enough overflows to a
// negative duration, which would schedule the retry in the past.
func (d *Dispatcher) backoff(attempts int) time.Duration {
	wait := d.cfg.BaseBackoff
	for i := 1; i < attempts && wait < d.cfg.MaxBackoff; i++ {
		wait *= 2
	}
	wait = min(wait, d.cfg.MaxBackoff)

	return wait + rand.N(wait/5+1)
}

// permanentError marks a failure that retrying cannot fix.
type permanentError struct{ err error }

func permanent(err error) error         { return &permanentError{err: err} }
func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func orDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func orDefaultDuration(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}
