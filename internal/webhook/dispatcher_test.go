package webhook

// The dispatcher is driven against a real httptest receiver and a fake queue.
// The receiver is real because the things worth checking here — what a
// signature covers, which status codes retry, whether a redirect is followed —
// are properties of an actual HTTP exchange.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// fakeQueue records the outcome the dispatcher chose for each delivery.
type fakeQueue struct {
	mu      sync.Mutex
	pending []store.Delivery

	// lease is what the last claim asked for.
	lease time.Duration

	delivered   []int64
	rescheduled map[int64]time.Time
	causes      map[int64]string
	dead        []int64

	// purgedBefore records the cutoff of the last retention sweep, and purged
	// how many rows it claimed to remove.
	purgedBefore time.Time
	purged       int64
}

func newFakeQueue(ds ...store.Delivery) *fakeQueue {
	return &fakeQueue{
		pending:     ds,
		rescheduled: map[int64]time.Time{},
		causes:      map[int64]string{},
	}
}

// Claims are one-shot: the queue hands over what it has and then reports
// nothing due, which is what a lease does in Postgres.
func (q *fakeQueue) ClaimDueDeliveries(_ context.Context, limit int, lease time.Duration) ([]store.Delivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.lease = lease

	claimed := q.pending
	if len(claimed) > limit {
		claimed = claimed[:limit]
	}
	q.pending = q.pending[len(claimed):]
	return claimed, nil
}

func (q *fakeQueue) MarkDelivered(_ context.Context, id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.delivered = append(q.delivered, id)
	return nil
}

func (q *fakeQueue) RescheduleDelivery(_ context.Context, id int64, cause string, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.rescheduled[id] = at
	q.causes[id] = cause
	return nil
}

func (q *fakeQueue) DeadLetterDelivery(_ context.Context, id int64, cause string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.dead = append(q.dead, id)
	q.causes[id] = cause
	return nil
}

func (q *fakeQueue) PurgeDeliveredBefore(_ context.Context, cutoff time.Time) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.purgedBefore = cutoff
	return q.purged, nil
}

func delivery(id int64, attempts int) store.Delivery {
	return store.Delivery{
		ID:        id,
		EventType: store.EventUserDeactivated,
		TargetID:  "6f1e2c3d-0000-4000-8000-abcdefabcdef",
		Payload:   testBody,
		Attempts:  attempts,
	}
}

func testConfig(url string) Config {
	return Config{
		URL:         url,
		Secret:      testSigningKey,
		MaxAttempts: 3,
		Batch:       10,
		Timeout:     2 * time.Second,
		BaseBackoff: time.Second,
		MaxBackoff:  time.Minute,
	}
}

// newDispatcher pins the clock so signature and backoff assertions are exact.
func newDispatcher(t *testing.T, q Queue, url string) *Dispatcher {
	t.Helper()

	d := New(q, testConfig(url))
	d.now = func() time.Time { return signedAt }
	return d
}

func quietLogs(t *testing.T) {
	t.Helper()

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestDeliverySendsASignedRequestAndMarksItDelivered(t *testing.T) {
	var got *http.Request
	var body []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	q := newFakeQueue(delivery(7, 1))
	if err := newDispatcher(t, q, srv.URL).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if len(q.delivered) != 1 || q.delivered[0] != 7 {
		t.Fatalf("delivered = %v, want [7]", q.delivered)
	}
	if len(q.dead) != 0 || len(q.rescheduled) != 0 {
		t.Errorf("a 204 should not retry or park: dead=%v rescheduled=%v", q.dead, q.rescheduled)
	}

	if string(body) != string(testBody) {
		t.Errorf("body = %q, want %q", body, testBody)
	}

	// The signature has to verify over exactly the bytes and headers the
	// receiver read — that is the whole contract.
	sig := got.Header.Get(SignatureHeader)
	if err := Verify(testSigningKey, sig, 7, store.EventUserDeactivated, body, signedAt, testTolerance); err != nil {
		t.Errorf("receiver could not verify the signature: %v", err)
	}
	if err := Verify(wrongSigningKey, sig, 7, store.EventUserDeactivated, body, signedAt, testTolerance); err == nil {
		t.Error("signature verified under the wrong secret")
	}

	if got := got.Header.Get(DeliveryHeader); got != "7" {
		t.Errorf("%s = %q, want %q", DeliveryHeader, got, "7")
	}
	if got := got.Header.Get(EventHeader); got != store.EventUserDeactivated {
		t.Errorf("%s = %q, want %q", EventHeader, got, store.EventUserDeactivated)
	}
	if got := got.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestDeliveryOutcomesByStatus(t *testing.T) {
	quietLogs(t)

	for _, tc := range []struct {
		name     string
		status   int
		attempts int
		want     string // "retry" or "dead"
	}{
		{"a 500 is the receiver's problem, not the payload's", http.StatusInternalServerError, 1, "retry"},
		{"a 502 from a proxy in front of a restarting receiver", http.StatusBadGateway, 1, "retry"},
		{"a 429 is an explicit ask to come back later", http.StatusTooManyRequests, 1, "retry"},
		{"a 408 timed out rather than refused", http.StatusRequestTimeout, 1, "retry"},

		// A receiver that understood the request and said no will say no again.
		{"a 400 will not become valid on a retry", http.StatusBadRequest, 1, "dead"},
		{"a 401 needs the operator, not another attempt", http.StatusUnauthorized, 1, "dead"},
		{"a 404 means the endpoint is wrong", http.StatusNotFound, 1, "dead"},

		// Retryable, but out of attempts.
		{"a 500 on the last attempt", http.StatusInternalServerError, 3, "dead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "receiver said no", tc.status)
			}))
			defer srv.Close()

			q := newFakeQueue(delivery(1, tc.attempts))
			if err := newDispatcher(t, q, srv.URL).drain(context.Background()); err != nil {
				t.Fatalf("drain: %v", err)
			}

			if len(q.delivered) != 0 {
				t.Fatalf("a %d was recorded as delivered", tc.status)
			}

			switch tc.want {
			case "retry":
				if len(q.dead) != 0 {
					t.Errorf("dead-lettered a %d, want a retry", tc.status)
				}
				if _, ok := q.rescheduled[1]; !ok {
					t.Errorf("a %d was not rescheduled", tc.status)
				}
			case "dead":
				if len(q.dead) != 1 {
					t.Errorf("dead = %v, want the delivery parked", q.dead)
				}
				if _, ok := q.rescheduled[1]; ok {
					t.Errorf("rescheduled a %d that should have been parked", tc.status)
				}
			}

			// The receiver's answer is kept, so a reviewer can see why.
			if cause := q.causes[1]; !strings.Contains(cause, strconv.Itoa(tc.status)) {
				t.Errorf("cause = %q, want it to mention %d", cause, tc.status)
			}
		})
	}
}

// The retention sweep asks the queue to prune everything delivered before
// now-Retention, using the same pinned clock the rest of the dispatcher does.
func TestPurgeDeliveredUsesRetentionCutoff(t *testing.T) {
	quietLogs(t)

	q := newFakeQueue()
	q.purged = 3
	d := newDispatcher(t, q, "https://receiver.invalid")
	d.cfg.Retention = 30 * 24 * time.Hour

	d.purgeDelivered(context.Background())

	want := signedAt.Add(-d.cfg.Retention)
	if !q.purgedBefore.Equal(want) {
		t.Errorf("cutoff = %s, want %s", q.purgedBefore.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestRetentionFromEnv(t *testing.T) {
	// An empty value takes the same trimmed-and-unset branch a missing variable
	// does, so t.Setenv covers both and restores the environment afterwards.
	for _, tc := range []struct {
		name string
		val  string
		want time.Duration
	}{
		{"unset falls back to the default", "", defaultRetention},
		{"an explicit day count is honored", "7", 7 * 24 * time.Hour},
		{"zero disables the sweep", "0", 0},
		{"a negative value is ignored", "-5", defaultRetention},
		{"junk is ignored", "soon", defaultRetention},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCIM_WEBHOOK_RETENTION_DAYS", tc.val)

			if got := retentionFromEnv(); got != tc.want {
				t.Errorf("retentionFromEnv() = %s, want %s", got, tc.want)
			}
		})
	}
}

// A signed payload of user attributes must not follow a redirect to a host the
// operator never configured.
func TestDeliveryDoesNotFollowRedirects(t *testing.T) {
	quietLogs(t)

	var elsewhereHit bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer srv.Close()

	q := newFakeQueue(delivery(1, 1))
	if err := newDispatcher(t, q, srv.URL).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if elsewhereHit {
		t.Fatal("the payload was sent to the redirect target")
	}
	if len(q.delivered) != 0 {
		t.Error("a 302 was recorded as delivered")
	}
	if len(q.dead) != 1 {
		t.Errorf("dead = %v, want the redirect parked for the operator", q.dead)
	}
}

// A receiver that is simply not there is a retry, not a dead letter: the
// endpoint may be mid-restart.
func TestUnreachableReceiverIsRetried(t *testing.T) {
	quietLogs(t)

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	q := newFakeQueue(delivery(1, 1))
	if err := newDispatcher(t, q, url).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if _, ok := q.rescheduled[1]; !ok {
		t.Errorf("an unreachable receiver was not rescheduled (dead=%v)", q.dead)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	d := New(newFakeQueue(), testConfig("https://receiver.example/hook"))

	var prev time.Duration
	for attempts := 1; attempts <= 4; attempts++ {
		got := d.backoff(attempts)

		// Base doubles per attempt; jitter adds up to a fifth on top.
		want := time.Duration(1<<(attempts-1)) * time.Second
		if got < want || got > want+want/5 {
			t.Errorf("backoff(%d) = %v, want within [%v, %v]", attempts, got, want, want+want/5)
		}
		if got <= prev {
			t.Errorf("backoff(%d) = %v, not longer than the previous %v", attempts, got, prev)
		}
		prev = got
	}

	// Far past the cap, including a shift count that would overflow a naive
	// implementation into a negative duration.
	for _, attempts := range []int{20, 64, 1000} {
		got := d.backoff(attempts)
		if got < time.Minute || got > time.Minute+12*time.Second {
			t.Errorf("backoff(%d) = %v, want it capped near %v", attempts, got, time.Minute)
		}
	}
}

// A batch is sent sequentially, so the lease a claim takes has to outlast the
// whole batch. A lease covering one send would let the rows queued behind the
// current attempt come due mid-batch: a second dispatcher would deliver them
// again and, since the attempt is counted at claim time, each row would spend
// its retry budget at double rate.
func TestClaimLeaseOutlastsTheWholeBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Batch = 20
	cfg.Timeout = 10 * time.Second

	q := newFakeQueue(delivery(1, 1))
	d := New(q, cfg)

	if err := d.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// The worst case a claim has to survive: every row taking the full timeout.
	worstCase := time.Duration(cfg.Batch) * cfg.Timeout
	if q.lease <= worstCase {
		t.Errorf("claimed with a lease of %v, want longer than the %v a full batch can take", q.lease, worstCase)
	}
}

// A cancelled context stops the run rather than continuing to send.
func TestRunStopsOnContextCancel(t *testing.T) {
	quietLogs(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	q := newFakeQueue(delivery(1, 1))
	d := newDispatcher(t, q, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
