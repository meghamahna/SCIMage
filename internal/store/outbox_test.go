package store

// Integration tests against the real compose Postgres. The properties worth
// covering here are transactional — that a queued event shares the mutation's
// commit, and that a claim leases a row — and neither is observable without a
// real database.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// newEventStore opts into the outbox, which is off by default.
func newEventStore(t *testing.T) *Store {
	t.Helper()

	s := newTestStore(t)
	WithChangeEvents()(s)
	return s
}

// deliveriesFor reads the queue for one user. Tests share a database, so they
// assert on their own target rather than on the table's contents.
func deliveriesFor(t *testing.T, s *Store, targetID string) []Delivery {
	t.Helper()

	const q = `SELECT id, event_type, target_id, payload, attempts
	           FROM webhook_deliveries WHERE target_id = $1 ORDER BY id`

	rows, err := s.pool.Query(context.Background(), q, targetID)
	if err != nil {
		t.Fatalf("read deliveries for %q: %v", targetID, err)
	}
	defer rows.Close()

	ds, err := scanDeliveries(rows)
	if err != nil {
		t.Fatalf("read deliveries for %q: %v", targetID, err)
	}
	return ds
}

func deliveryStatus(t *testing.T, s *Store, id int64) (status string, attempts int, nextAt time.Time, lastError *string) {
	t.Helper()

	err := s.pool.QueryRow(context.Background(),
		`SELECT status, attempts, next_attempt_at, last_error FROM webhook_deliveries WHERE id = $1`, id).
		Scan(&status, &attempts, &nextAt, &lastError)
	if err != nil {
		t.Fatalf("read delivery %d: %v", id, err)
	}
	return status, attempts, nextAt, lastError
}

// The queue is never drained by the store itself, so a test cleans up the rows
// it queued the same way it cleans up its users.
func cleanupDeliveries(t *testing.T, s *Store, targetID string) {
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(),
			`DELETE FROM webhook_deliveries WHERE target_id = $1`, targetID); err != nil {
			t.Errorf("cleanup: delete deliveries for %q: %v", targetID, err)
		}
	})
}

func decodeEvent(t *testing.T, d Delivery) ChangeEvent {
	t.Helper()

	var e ChangeEvent
	if err := json.Unmarshal(d.Payload, &e); err != nil {
		t.Fatalf("decode payload of delivery %d: %v", d.ID, err)
	}
	return e
}

func TestMutationsQueueTheirChangeEvent(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	replaced := *created
	replaced.UserName = uniqueUserName()
	if _, err := s.UpdateUser(ctx, created.ID, &replaced, testAudit); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if _, err := s.DeactivateUser(ctx, created.ID, testAudit); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}

	got := deliveriesFor(t, s, created.ID)
	wantTypes := []string{EventUserCreated, EventUserReplaced, EventUserDeactivated}
	if len(got) != len(wantTypes) {
		t.Fatalf("queued %d events, want %d", len(got), len(wantTypes))
	}

	for i, want := range wantTypes {
		if got[i].EventType != want {
			t.Errorf("event %d = %q, want %q", i, got[i].EventType, want)
		}
		if got[i].Attempts != 0 {
			t.Errorf("event %d queued with %d attempts, want 0", i, got[i].Attempts)
		}

		e := decodeEvent(t, got[i])
		if e.Type != want {
			t.Errorf("payload type = %q, want %q", e.Type, want)
		}
		if e.UserID != created.ID {
			t.Errorf("payload userId = %q, want %q", e.UserID, created.ID)
		}
		if e.After == nil {
			t.Errorf("payload for %s has no after image", want)
		}
	}

	// A create has nothing to compare against; a change does.
	if e := decodeEvent(t, got[0]); e.Before != nil {
		t.Errorf("create carried a before image: %+v", e.Before)
	}
	if e := decodeEvent(t, got[1]); e.Before == nil || e.Before.UserName != created.UserName {
		t.Errorf("replace before image = %+v, want userName %q", e.Before, created.UserName)
	}

	// The deactivation is the event a receiver acts on, so both images matter.
	e := decodeEvent(t, got[2])
	if e.Before == nil || !e.Before.Active {
		t.Errorf("deactivate before image = %+v, want active", e.Before)
	}
	if e.After == nil || e.After.Active {
		t.Errorf("deactivate after image = %+v, want inactive", e.After)
	}
}

// Both shapes of deprovisioning have to reach a receiver as the same event.
// Identity providers overwhelmingly send PATCH active:false rather than DELETE
// (see the Phase 8 interop notes), and that PATCH is applied through the same
// full-replace path as a PUT — so naming the event after the method would emit
// user.replaced for the one change a receiver most needs to act on.
func TestDeprovisioningConvergesOnOneEventType(t *testing.T) {
	ctx := context.Background()

	t.Run("a replace that clears active", func(t *testing.T) {
		s := newEventStore(t)

		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
		cleanupDeliveries(t, s, created.ID)

		deactivating := *created
		deactivating.Active = false
		if _, err := s.UpdateUser(ctx, created.ID, &deactivating, testAudit); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

		got := deliveriesFor(t, s, created.ID)
		if len(got) != 2 {
			t.Fatalf("queued %d events, want 2", len(got))
		}
		if got[1].EventType != EventUserDeactivated {
			t.Errorf("event = %q, want %q", got[1].EventType, EventUserDeactivated)
		}
	})

	t.Run("a delete", func(t *testing.T) {
		s := newEventStore(t)

		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
		cleanupDeliveries(t, s, created.ID)

		if _, err := s.DeactivateUser(ctx, created.ID, testAudit); err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}

		got := deliveriesFor(t, s, created.ID)
		if len(got) != 2 {
			t.Fatalf("queued %d events, want 2", len(got))
		}
		if got[1].EventType != EventUserDeactivated {
			t.Errorf("event = %q, want %q", got[1].EventType, EventUserDeactivated)
		}
	})

	// A replace that leaves the user active is not a deprovisioning, so the
	// classification can't just be "any update to an active user".
	t.Run("a replace that leaves active alone", func(t *testing.T) {
		s := newEventStore(t)

		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
		cleanupDeliveries(t, s, created.ID)

		renamed := *created
		renamed.UserName = uniqueUserName()
		if _, err := s.UpdateUser(ctx, created.ID, &renamed, testAudit); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

		got := deliveriesFor(t, s, created.ID)
		if len(got) != 2 {
			t.Fatalf("queued %d events, want 2", len(got))
		}
		if got[1].EventType != EventUserReplaced {
			t.Errorf("event = %q, want %q", got[1].EventType, EventUserReplaced)
		}
	})

	// Reactivation is a replace, distinguishable from the after image.
	t.Run("a replace that restores active", func(t *testing.T) {
		s := newEventStore(t)

		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: false})
		cleanupDeliveries(t, s, created.ID)

		restored := *created
		restored.Active = true
		if _, err := s.UpdateUser(ctx, created.ID, &restored, testAudit); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

		got := deliveriesFor(t, s, created.ID)
		if len(got) != 2 {
			t.Fatalf("queued %d events, want 2", len(got))
		}
		if got[1].EventType != EventUserReplaced {
			t.Errorf("event = %q, want %q", got[1].EventType, EventUserReplaced)
		}
		if e := decodeEvent(t, got[1]); e.After == nil || !e.After.Active {
			t.Errorf("after image = %+v, want active", e.After)
		}
	})
}

// The queue is off unless a dispatcher is going to drain it.
func TestNoChangeEventsWithoutTheOption(t *testing.T) {
	s := newTestStore(t)

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	if got := deliveriesFor(t, s, created.ID); len(got) != 0 {
		t.Errorf("queued %d events with the outbox off, want 0", len(got))
	}
}

// The point of queueing inside the transaction: a mutation that doesn't commit
// leaves nothing to send. A duplicate userName rolls the whole transaction
// back, so the event has to roll back with it.
func TestARefusedMutationQueuesNothing(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	taken := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, taken.ID)

	other := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, other.ID)

	// Renaming other onto taken's userName fails on the unique index.
	clash := *other
	clash.UserName = taken.UserName
	if _, err := s.UpdateUser(ctx, other.ID, &clash, testAudit); !errors.Is(err, ErrDuplicateUserName) {
		t.Fatalf("UpdateUser onto a taken userName = %v, want ErrDuplicateUserName", err)
	}

	// Only the create should have queued anything.
	got := deliveriesFor(t, s, other.ID)
	if len(got) != 1 || got[0].EventType != EventUserCreated {
		t.Fatalf("queued %d events, want only the create: %+v", len(got), got)
	}
}

func TestClaimLeasesADeliveryFromOtherDispatchers(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	queued := deliveriesFor(t, s, created.ID)
	if len(queued) != 1 {
		t.Fatalf("queued %d events, want 1", len(queued))
	}
	id := queued[0].ID

	// The table is shared with other tests, so the assertion is about this row,
	// not about how much came back.
	if !claims(t, s, ctx, id, time.Minute) {
		t.Fatal("a due delivery was not claimed")
	}

	// Still pending, but leased into the future and counted.
	status, attempts, nextAt, _ := deliveryStatus(t, s, id)
	if status != DeliveryPending {
		t.Errorf("status after claim = %q, want %q", status, DeliveryPending)
	}
	if attempts != 1 {
		t.Errorf("attempts after claim = %d, want 1", attempts)
	}
	if !nextAt.After(time.Now()) {
		t.Errorf("next_attempt_at = %v, want it leased into the future", nextAt)
	}

	// A second dispatcher must not pick it up while the lease holds.
	if claims(t, s, ctx, id, time.Minute) {
		t.Error("a leased delivery was claimed a second time")
	}
}

// claims reports whether id came back in a claim.
func claims(t *testing.T, s *Store, ctx context.Context, id int64, lease time.Duration) bool {
	t.Helper()

	claimed, err := s.ClaimDueDeliveries(ctx, MaxPageSize, lease)
	if err != nil {
		t.Fatalf("ClaimDueDeliveries: %v", err)
	}

	for _, d := range claimed {
		if d.ID == id {
			return true
		}
	}
	return false
}

func TestDeliveryOutcomesArePersisted(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	id := deliveriesFor(t, s, created.ID)[0].ID

	// Reschedule: still pending, due when told, with the cause kept.
	retryAt := time.Now().Add(90 * time.Second).Truncate(time.Millisecond)
	if err := s.RescheduleDelivery(ctx, id, "receiver returned 503", retryAt); err != nil {
		t.Fatalf("RescheduleDelivery: %v", err)
	}

	status, _, nextAt, lastError := deliveryStatus(t, s, id)
	if status != DeliveryPending {
		t.Errorf("status = %q, want %q", status, DeliveryPending)
	}
	if !nextAt.Equal(retryAt) {
		t.Errorf("next_attempt_at = %v, want %v", nextAt, retryAt)
	}
	if lastError == nil || *lastError != "receiver returned 503" {
		t.Errorf("last_error = %v, want the cause", lastError)
	}

	// A leased row is not due, so it can't be claimed before its retry time.
	if claims(t, s, ctx, id, time.Minute) {
		t.Error("a delivery was claimed before its next attempt was due")
	}

	// Delivered: terminal, and the stale error is cleared.
	if err := s.MarkDelivered(ctx, id); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	status, _, _, lastError = deliveryStatus(t, s, id)
	if status != DeliveryDelivered {
		t.Errorf("status = %q, want %q", status, DeliveryDelivered)
	}
	if lastError != nil {
		t.Errorf("last_error = %q, want it cleared on success", *lastError)
	}
}

// Delivered and dead-lettered are terminal. A dispatcher whose lease expired
// while it was still sending can report an outcome late, and that must not undo
// a state the row has already reached.
func TestATerminalDeliveryCannotBeRescheduled(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		terminal func(id int64) error
	}{
		{"delivered", func(id int64) error { return s.MarkDelivered(ctx, id) }},
		{"dead-lettered", func(id int64) error { return s.DeadLetterDelivery(ctx, id, "receiver returned 400") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
			cleanupDeliveries(t, s, created.ID)

			id := deliveriesFor(t, s, created.ID)[0].ID
			if err := tc.terminal(id); err != nil {
				t.Fatalf("reaching the terminal state: %v", err)
			}

			wantStatus, _, _, _ := deliveryStatus(t, s, id)

			// Every writer is guarded, not just the reschedule: a late failure
			// must not dead-letter an event that was in fact delivered, and a
			// late success must not resurrect a parked one.
			late := map[string]error{
				"RescheduleDelivery": s.RescheduleDelivery(ctx, id, "a late failure report", time.Now()),
				"DeadLetterDelivery": s.DeadLetterDelivery(ctx, id, "a late failure report"),
				"MarkDelivered":      s.MarkDelivered(ctx, id),
			}
			for op, err := range late {
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("%s on a %s row = %v, want ErrNotFound", op, tc.name, err)
				}
			}

			status, _, _, _ := deliveryStatus(t, s, id)
			if status != wantStatus {
				t.Errorf("status = %q, want it left at %q", status, wantStatus)
			}
		})
	}
}

// A dead letter keeps its payload so a human can see what failed and replay it.
func TestDeadLetteredDeliveriesStayReadable(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	queued := deliveriesFor(t, s, created.ID)[0]

	if err := s.DeadLetterDelivery(ctx, queued.ID, "receiver returned 400 Bad Request"); err != nil {
		t.Fatalf("DeadLetterDelivery: %v", err)
	}

	status, _, _, lastError := deliveryStatus(t, s, queued.ID)
	if status != DeliveryDeadLetter {
		t.Errorf("status = %q, want %q", status, DeliveryDeadLetter)
	}
	if lastError == nil || *lastError != "receiver returned 400 Bad Request" {
		t.Errorf("last_error = %v, want the cause", lastError)
	}

	// Parked, so no dispatcher picks it up again.
	if claims(t, s, ctx, queued.ID, time.Minute) {
		t.Error("a dead-lettered delivery was claimed")
	}

	letters, err := s.DeadLetters(ctx, MaxPageSize)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}

	var found *Delivery
	for i := range letters {
		if letters[i].ID == queued.ID {
			found = &letters[i]
		}
	}
	if found == nil {
		t.Fatal("the parked delivery is not in the dead-letter queue")
	}
	if e := decodeEvent(t, *found); e.UserID != created.ID {
		t.Errorf("dead letter payload userId = %q, want %q", e.UserID, created.ID)
	}
}

func TestUpdatingAnUnknownDeliveryIsAnError(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	const missing = -1

	if err := s.MarkDelivered(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkDelivered on a missing row = %v, want ErrNotFound", err)
	}
	if err := s.DeadLetterDelivery(ctx, missing, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeadLetterDelivery on a missing row = %v, want ErrNotFound", err)
	}
	if err := s.RescheduleDelivery(ctx, missing, "gone", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("RescheduleDelivery on a missing row = %v, want ErrNotFound", err)
	}
}

// A receiver's error body reaches a text column, so it is bounded and
// sanitised: invalid UTF-8 or a NUL would otherwise fail the write and leave
// the delivery stuck in flight.
func TestAHostileErrorBodyStillRecords(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	cleanupDeliveries(t, s, created.ID)

	id := deliveriesFor(t, s, created.ID)[0].ID

	// A NUL, an invalid UTF-8 sequence, and multi-byte runes past the limit —
	// so the truncation has to land on a rune boundary rather than mid-sequence.
	hostile := "bad\x00request \xff\xfe " + strings.Repeat("é", 2*maxCauseLen)
	if err := s.DeadLetterDelivery(ctx, id, hostile); err != nil {
		t.Fatalf("DeadLetterDelivery with a hostile cause: %v", err)
	}

	_, _, _, lastError := deliveryStatus(t, s, id)
	if lastError == nil {
		t.Fatal("last_error was not recorded")
	}

	got := *lastError
	// maxCauseLen runes plus the ellipsis that marks the cut.
	if n := len([]rune(got)); n != maxCauseLen+1 {
		t.Errorf("last_error is %d runes, want %d", n, maxCauseLen+1)
	}
	if !utf8.ValidString(got) {
		t.Error("last_error is not valid UTF-8")
	}
	if strings.ContainsRune(got, 0) {
		t.Error("last_error still contains a NUL")
	}
	if !strings.HasPrefix(got, "bad") || strings.Contains(got, "\xff") {
		t.Errorf("last_error = %q, want the sanitised cause", got[:min(40, len(got))])
	}
}
