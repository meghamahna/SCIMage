package store

import (
	"context"
	"encoding/json"
	"fmt"
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

// Delivery is one queued event, as the dispatcher sees it.
type Delivery struct {
	ID        int64
	EventType string
	TargetID  string
	Payload   []byte

	// Attempts counts this claim, since ClaimDueDeliveries increments before
	// handing the row over.
	Attempts int
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
func (s *Store) enqueueChange(ctx context.Context, q querier, eventType, targetID string, before, after *User) error {
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

	const stmt = `INSERT INTO webhook_deliveries (event_type, target_id, payload) VALUES ($1, $2, $3)`
	if _, err := q.Exec(ctx, stmt, eventType, targetID, payload); err != nil {
		return fmt.Errorf("enqueue %s event for user %q: %w", eventType, targetID, err)
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
	           RETURNING id, event_type, target_id, payload, attempts`

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

// DeadLetters returns parked deliveries, newest first, for review and replay.
func (s *Store) DeadLetters(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	const q = `SELECT id, event_type, target_id, payload, attempts
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

func scanDeliveries(rows pgx.Rows) ([]Delivery, error) {
	var out []Delivery
	for rows.Next() {
		var (
			d        Delivery
			targetID *string
		)
		if err := rows.Scan(&d.ID, &d.EventType, &targetID, &d.Payload, &d.Attempts); err != nil {
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
