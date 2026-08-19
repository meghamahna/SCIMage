package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Event types name what happened to the user rather than which endpoint was
// called, so a receiver doesn't have to know which shape of deprovisioning its
// identity provider prefers.
//
// That distinction is load-bearing rather than tidy. Providers deprovision with
// PATCH active:false far more often than with DELETE, and that PATCH is applied
// through the same full-replace path as a PUT — so naming events after the call
// would emit user.replaced for the one change a receiver most needs to act on,
// and a consumer subscribed to deactivations would miss every real
// deprovisioning.
const (
	EventUserCreated     = "user.created"
	EventUserReplaced    = "user.replaced"
	EventUserDeactivated = "user.deactivated"
)

// Group events. A membership PATCH rides on EventGroupReplaced the same way
// PATCH active:false rides on EventUserReplaced/EventUserDeactivated above —
// PATCH funnels through UpdateGroup's full replace, so there is no separate
// membership-change event to classify.
const (
	EventGroupCreated  = "group.created"
	EventGroupReplaced = "group.replaced"
	EventGroupDeleted  = "group.deleted"
)

// changeEventType classifies a replace by what it did to the user, reading the
// before/after pair rather than the method that arrived.
//
// It is the active transition that makes a deactivation, not the resulting
// state: a replace that only changes the email of an already-inactive user is
// not a deprovisioning. So a repeat PATCH active:false against an inactive user
// reports user.replaced, while a repeat DELETE — which means deactivate whatever
// the current state — still reports user.deactivated. Both are no-op changes a
// receiver applying idempotently ignores.
//
// A reactivation is user.replaced with active=true in the after image. A fourth
// event type earns its place when a receiver needs to route on it.
func changeEventType(before, after *User) string {
	if before != nil && before.Active && !after.Active {
		return EventUserDeactivated
	}
	return EventUserReplaced
}

const (
	DeliveryPending    = "pending"
	DeliveryDelivered  = "delivered"
	DeliveryDeadLetter = "dead_letter"
)

// Delivery is one queued event, as the dispatcher sees it. TenantID is
// carried for review and for a future per-tenant delivery target; the
// dispatcher itself still sends every tenant's events to one configured
// SCIM_WEBHOOK_URL, since that's the app owner's own receiver regardless of
// which of their customers an event is about.
type Delivery struct {
	ID        int64
	TenantID  string
	EventType string
	TargetID  string
	Payload   []byte

	// Attempts counts this claim, since ClaimDueDeliveries increments before
	// handing the row over.
	Attempts int

	// NextAttemptAt is display-only: PendingDeliveries and DeadLetters read it
	// so the console can show when a queued row is due, not decide anything
	// with it. ClaimDueDeliveries's own scheduling logic never consults it.
	NextAttemptAt time.Time
}

// ChangeEvent is the webhook body. It carries both images for the same reason
// the audit log does: a receiver reconciling its own copy needs to know what
// changed, not only what the row now says. Before is nil for a create.
//
// The user shape is the stored one, matching the audit log's before/after
// rather than the SCIM wire format — one serialisation of a user, used in both
// places, so the two records of a change can be read side by side.
type ChangeEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	UserID     string    `json:"userId"`
	Before     *User     `json:"before,omitempty"`
	After      *User     `json:"after"`
}

// enqueueChange queues the event inside the mutation's transaction, the same
// discipline as the audit entry.
//
// Delivery is at-least-once, and deliberately not deduplicated here: a retried
// DELETE against an already-inactive user is a real call that the audit log
// records, so suppressing its event would make the outbound stream disagree
// with the audit trail. Receivers key on the delivery id and apply idempotently.
func (s *Store) enqueueChange(ctx context.Context, q querier, tenantID, eventType, targetID string, before, after *User) error {
	if !s.changeEvents {
		return nil
	}

	payload, err := json.Marshal(ChangeEvent{
		Type: eventType,
		// The database clock, not the process clock, so ordering agrees with
		// the row it describes.
		OccurredAt: after.UpdatedAt,
		UserID:     targetID,
		Before:     before,
		After:      after,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event for user %q: %w", eventType, targetID, err)
	}

	const stmt = `INSERT INTO webhook_deliveries (tenant_id, event_type, target_id, payload) VALUES ($1, $2, $3, $4)`
	if _, err := q.Exec(ctx, stmt, tenantID, eventType, targetID, payload); err != nil {
		return fmt.Errorf("enqueue %s event for user %q: %w", eventType, targetID, err)
	}
	return nil
}

// GroupChangeEvent mirrors ChangeEvent for the Group resource. It is a
// distinct type rather than a generalized one because the two schemas don't
// share a shape, the same reason AuditEntry keeps its images as raw JSON
// rather than one shared Go struct.
type GroupChangeEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	GroupID    string    `json:"groupId"`
	Before     *Group    `json:"before,omitempty"`
	After      *Group    `json:"after"`
}

// enqueueGroupChange mirrors enqueueChange: queued in the mutation's own
// transaction, at-least-once, not deduplicated here for the same reason a
// retried user mutation isn't — the audit log already recorded a real call.
func (s *Store) enqueueGroupChange(ctx context.Context, q querier, tenantID, eventType, targetID string, before, after *Group) error {
	if !s.changeEvents {
		return nil
	}

	// A delete has no after-image to take a timestamp from, so it falls back
	// to the process clock — there is no committed row left to read one from.
	occurred := time.Now()
	if after != nil {
		occurred = after.UpdatedAt
	}

	payload, err := json.Marshal(GroupChangeEvent{
		Type:       eventType,
		OccurredAt: occurred,
		GroupID:    targetID,
		Before:     before,
		After:      after,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event for group %q: %w", eventType, targetID, err)
	}

	const stmt = `INSERT INTO webhook_deliveries (tenant_id, event_type, target_id, payload) VALUES ($1, $2, $3, $4)`
	if _, err := q.Exec(ctx, stmt, tenantID, eventType, targetID, payload); err != nil {
		return fmt.Errorf("enqueue %s event for group %q: %w", eventType, targetID, err)
	}
	return nil
}

// ClaimDueDeliveries leases up to limit due rows: it counts the attempt and
// pushes next_attempt_at past the lease in the same statement that selects
// them, so a row can't be claimed twice while it is in flight. FOR UPDATE SKIP
// LOCKED makes two dispatchers claim disjoint sets rather than queue behind
// each other.
//
// The lease is what a claim holds, rather than an open transaction: delivery is
// an HTTP call, and holding a row lock across it would tie a database
// connection to whatever the receiver's latency happens to be.
//
// Counting the attempt at claim time rather than on failure is deliberate. A
// delivery that kills the dispatcher mid-flight still burns an attempt, so it
// eventually dead-letters instead of taking the process down forever.
func (s *Store) ClaimDueDeliveries(ctx context.Context, limit int, lease time.Duration) ([]Delivery, error) {
	if limit <= 0 {
		return nil, nil
	}

	const q = `UPDATE webhook_deliveries SET
	               attempts = attempts + 1,
	               next_attempt_at = now() + make_interval(secs => $2)
	           WHERE id IN (
	               SELECT id FROM webhook_deliveries
	               WHERE status = $3 AND next_attempt_at <= now()
	               ORDER BY next_attempt_at, id
	               LIMIT $1
	               FOR UPDATE SKIP LOCKED
	           )
	           RETURNING id, tenant_id, event_type, target_id, payload, attempts, next_attempt_at`

	rows, err := s.pool.Query(ctx, q, limit, lease.Seconds(), DeliveryPending)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	defer rows.Close()

	claimed, err := scanDeliveries(rows)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	return claimed, nil
}

// Every outcome is guarded on status = pending, so delivered and dead-lettered
// really are terminal. A dispatcher whose lease expired mid-send can report an
// outcome after another one has already finished the row: without the guard, a
// late failure could dead-letter an event that was in fact delivered, leaving it
// in the queue for a human to replay.
func (s *Store) MarkDelivered(ctx context.Context, id int64) error {
	const q = `UPDATE webhook_deliveries
	           SET status = $2, delivered_at = now(), last_error = NULL
	           WHERE id = $1 AND status = $3`

	return s.updateDelivery(ctx, q, "mark delivered", id, DeliveryDelivered, DeliveryPending)
}

// RescheduleDelivery returns a failed delivery to the queue. cause is stored so
// a reviewer can see why a row keeps missing without reading the dispatcher's
// logs.
func (s *Store) RescheduleDelivery(ctx context.Context, id int64, cause string, at time.Time) error {
	const q = `UPDATE webhook_deliveries
	           SET next_attempt_at = $3, last_error = $2
	           WHERE id = $1 AND status = $4`

	return s.updateDelivery(ctx, q, "reschedule delivery", id, truncateCause(cause), at, DeliveryPending)
}

// DeadLetterDelivery parks a delivery that is not worth retrying. The row stays
// with its payload and its last error, so a human can see what failed and
// replay it once the receiver is fixed — dropping it would make an
// undeliverable change indistinguishable from one that never happened.
func (s *Store) DeadLetterDelivery(ctx context.Context, id int64, cause string) error {
	const q = `UPDATE webhook_deliveries SET status = $2, last_error = $3
	           WHERE id = $1 AND status = $4`

	return s.updateDelivery(ctx, q, "dead-letter delivery", id, DeliveryDeadLetter, truncateCause(cause), DeliveryPending)
}

// A missing row is an error rather than a silent no-op: the dispatcher only
// names ids it just claimed, so not finding one means the queue changed under
// it, which is worth surfacing.
func (s *Store) updateDelivery(ctx context.Context, q, op string, id int64, args ...any) error {
	tag, err := s.pool.Exec(ctx, q, append([]any{id}, args...)...)
	if err != nil {
		return fmt.Errorf("%s %d: %w", op, id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%s %d: %w", op, id, ErrNotFound)
	}
	return nil
}

// PurgeDeliveredBefore deletes delivered rows whose delivery committed before
// cutoff, returning how many went. It bounds a table that otherwise only grows:
// every mutation queues a row, and a successful one would sit in the outbox
// forever without this.
//
// Only delivered rows are touched. A pending row is still in flight, and a
// dead-lettered one is kept for a human to inspect and replay once the receiver
// is fixed — dropping either would lose a delivery the operator still needs. The
// status guard is what makes retention safe to run unattended.
func (s *Store) PurgeDeliveredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM webhook_deliveries WHERE status = $1 AND delivered_at < $2`

	tag, err := s.pool.Exec(ctx, q, DeliveryDelivered, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge delivered deliveries before %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}

// WebhookSummary is a read-only health snapshot of the delivery queue for the
// admin console: how many events are waiting, delivered, or parked, and how long
// the oldest still-waiting one has been queued.
type WebhookSummary struct {
	Pending       int
	Delivered     int
	DeadLetter    int
	OldestPending *time.Time // nil when nothing is pending
}

// WebhookDeliverySummary counts deliveries by status across every tenant. It's
// deployment-wide because the sink is: one configured receiver drains all
// tenants' events, so the operator's view of delivery health is global too.
func (s *Store) WebhookDeliverySummary(ctx context.Context) (WebhookSummary, error) {
	const q = `SELECT
	               count(*) FILTER (WHERE status = $1) AS pending,
	               count(*) FILTER (WHERE status = $2) AS delivered,
	               count(*) FILTER (WHERE status = $3) AS dead_letter,
	               min(created_at) FILTER (WHERE status = $1) AS oldest_pending
	           FROM webhook_deliveries`

	var out WebhookSummary
	if err := s.pool.QueryRow(ctx, q, DeliveryPending, DeliveryDelivered, DeliveryDeadLetter).
		Scan(&out.Pending, &out.Delivered, &out.DeadLetter, &out.OldestPending); err != nil {
		return WebhookSummary{}, fmt.Errorf("webhook delivery summary: %w", err)
	}
	return out, nil
}

// WebhookDeliveryStatus returns one delivery's current status (pending,
// delivered, or dead_letter), for the console to poll after a replay. A missing
// row is ErrNotFound — a delivered row is kept until retention prunes it, so
// within a poll window "not found" means an unknown id, not a delivered one.
func (s *Store) WebhookDeliveryStatus(ctx context.Context, id int64) (string, error) {
	const q = `SELECT status FROM webhook_deliveries WHERE id = $1`

	var status string
	if err := s.pool.QueryRow(ctx, q, id).Scan(&status); err != nil {
		if isMissingRow(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("webhook delivery status %d: %w", id, err)
	}
	return status, nil
}

// ReplayDeadLetter returns one parked delivery to the queue: it flips a
// dead-lettered row back to pending, resets its attempt count so it gets a full
// retry budget again, and makes it due now. It is guarded on status =
// dead_letter so a delivered or in-flight row can't be disturbed, and it records
// who replayed what in the admin audit log in the same transaction — replay is
// an operator action worth a trail like any other mutation here.
//
// The HMAC signature is recomputed at send time, not stored, so a replayed
// delivery, however old, still arrives with a fresh timestamp inside the
// receiver's freshness window.
func (s *Store) ReplayDeadLetter(ctx context.Context, id int64, actor string) error {
	const q = `UPDATE webhook_deliveries
	           SET status = $2, attempts = 0, next_attempt_at = now(), last_error = NULL
	           WHERE id = $1 AND status = $3
	           RETURNING tenant_id, event_type`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("replay delivery %d: begin: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	var tenantID, eventType string
	if err := tx.QueryRow(ctx, q, id, DeliveryPending, DeliveryDeadLetter).Scan(&tenantID, &eventType); err != nil {
		if isMissingRow(err) {
			// No row matched: either no such delivery, or it isn't dead-lettered.
			// Neither is a row this operation may touch, so both are ErrNotFound.
			return fmt.Errorf("replay delivery %d: %w", id, ErrNotFound)
		}
		return fmt.Errorf("replay delivery %d: %w", id, err)
	}

	if err := insertAdminAudit(ctx, tx, tenantID, actor, AdminActionWebhookReplay, strconv.FormatInt(id, 10), eventType); err != nil {
		return fmt.Errorf("replay delivery %d: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("replay delivery %d: commit: %w", id, err)
	}
	return nil
}

// DeadLetters returns parked deliveries, newest first, for review and replay.
func (s *Store) DeadLetters(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	const q = `SELECT id, tenant_id, event_type, target_id, payload, attempts, next_attempt_at
	           FROM webhook_deliveries
	           WHERE status = $1
	           ORDER BY created_at DESC, id DESC
	           LIMIT $2`

	rows, err := s.pool.Query(ctx, q, DeliveryDeadLetter, limit)
	if err != nil {
		return nil, fmt.Errorf("list dead letters: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

// PendingDeliveries returns deliveries still on the queue, soonest-due first,
// so the console can show a replayed or retrying event as "queued" instead of
// it vanishing from view between replay and its next outcome (delivered or
// dead-lettered again).
func (s *Store) PendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	const q = `SELECT id, tenant_id, event_type, target_id, payload, attempts, next_attempt_at
	           FROM webhook_deliveries
	           WHERE status = $1
	           ORDER BY next_attempt_at ASC, id
	           LIMIT $2`

	rows, err := s.pool.Query(ctx, q, DeliveryPending, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending deliveries: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

func scanDeliveries(rows pgx.Rows) ([]Delivery, error) {
	var out []Delivery
	for rows.Next() {
		var (
			d        Delivery
			targetID *string
		)
		if err := rows.Scan(&d.ID, &d.TenantID, &d.EventType, &targetID, &d.Payload, &d.Attempts, &d.NextAttemptAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		d.TargetID = deref(targetID)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read deliveries: %w", err)
	}
	return out, nil
}

// maxCauseLen bounds last_error in runes, not bytes: a receiver's response body
// ends up in there and slicing bytes would cut a multi-byte rune in half.
const maxCauseLen = 500

// A cause is built from whatever the receiver sent back, so it is sanitised as
// well as bounded before it reaches a text column: Postgres rejects NUL
// outright and invalid UTF-8 would fail the write, turning a receiver's
// malformed error body into a stuck delivery.
func truncateCause(s string) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")

	r := []rune(s)
	if len(r) <= maxCauseLen {
		return s
	}
	return string(r[:maxCauseLen]) + "…"
}
