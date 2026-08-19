package store

// Integration tests against the real compose Postgres. The properties worth
// covering here are transactional — that a queued event shares the mutation's
// commit, and that a claim leases a row — and neither is observable without a
// real database.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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

// deliveriesFor reads the queue for one user. Each test's rows live under its
// own tenant and its own target, so asserting on one target id is enough —
// nothing else could have queued against it.
func deliveriesFor(t *testing.T, s *Store, targetID string) []Delivery {
	t.Helper()

	const q = `SELECT id, tenant_id, event_type, target_id, payload, attempts, next_attempt_at
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

func deliveryExists(t *testing.T, s *Store, id int64) bool {
	t.Helper()

	var exists bool
	if err := s.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM webhook_deliveries WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("check delivery %d: %v", id, err)
	}
	return exists
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
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	replaced := *created
	replaced.UserName = uniqueUserName()
	if _, err := s.UpdateUser(ctx, tenantID, created.ID, &replaced, testAudit(tenantID)); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if _, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
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
		if got[i].TenantID != tenantID {
			t.Errorf("event %d tenant = %q, want %q", i, got[i].TenantID, tenantID)
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
		tenantID := newTestTenant(t, s)

		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		deactivating := *created
		deactivating.Active = false
		if _, err := s.UpdateUser(ctx, tenantID, created.ID, &deactivating, testAudit(tenantID)); err != nil {
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
		tenantID := newTestTenant(t, s)

		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		if _, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
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
		tenantID := newTestTenant(t, s)

		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		renamed := *created
		renamed.UserName = uniqueUserName()
		if _, err := s.UpdateUser(ctx, tenantID, created.ID, &renamed, testAudit(tenantID)); err != nil {
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
		tenantID := newTestTenant(t, s)

		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: false})

		restored := *created
		restored.Active = true
		if _, err := s.UpdateUser(ctx, tenantID, created.ID, &restored, testAudit(tenantID)); err != nil {
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
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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
	tenantID := newTestTenant(t, s)

	taken := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	other := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	// Renaming other onto taken's userName fails on the unique index.
	clash := *other
	clash.UserName = taken.UserName
	if _, err := s.UpdateUser(ctx, tenantID, other.ID, &clash, testAudit(tenantID)); !errors.Is(err, ErrDuplicateUserName) {
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
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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

// Retention prunes delivered rows past the window and leaves everything else:
// a delivered row newer than the cutoff, a pending row still in flight, and a
// dead-lettered one kept for a human to replay.
//
// The sweep is deployment-wide, not tenant-scoped — one dispatcher runs it for
// the whole server. So this test can't assert on a global row count while other
// store tests create deliveries concurrently; it backdates the one row it wants
// gone and uses a recent-past cutoff, which leaves every other test's fresh rows
// untouched, then asserts on its own ids alone.
func TestPurgeDeliveredRemovesOnlyOldDeliveredRows(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	oldUser := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	freshUser := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	pendingUser := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	deadUser := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	oldID := deliveriesFor(t, s, oldUser.ID)[0].ID
	freshID := deliveriesFor(t, s, freshUser.ID)[0].ID
	pendingID := deliveriesFor(t, s, pendingUser.ID)[0].ID
	deadID := deliveriesFor(t, s, deadUser.ID)[0].ID

	if err := s.MarkDelivered(ctx, oldID); err != nil {
		t.Fatalf("MarkDelivered old: %v", err)
	}
	if err := s.MarkDelivered(ctx, freshID); err != nil {
		t.Fatalf("MarkDelivered fresh: %v", err)
	}
	if err := s.DeadLetterDelivery(ctx, deadID, "receiver returned 400"); err != nil {
		t.Fatalf("DeadLetterDelivery: %v", err)
	}

	// Push one delivery's receipt back beyond the window; the fresh one keeps
	// its now() timestamp, which is what proves the cutoff is honored.
	backdateDelivery(t, s, oldID, time.Now().Add(-48*time.Hour))

	if _, err := s.PurgeDeliveredBefore(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("PurgeDeliveredBefore: %v", err)
	}

	if deliveryExists(t, s, oldID) {
		t.Error("a delivery older than the window survived retention")
	}
	if !deliveryExists(t, s, freshID) {
		t.Error("retention removed a delivery newer than the cutoff")
	}
	if !deliveryExists(t, s, pendingID) {
		t.Error("retention removed a pending row that was still in flight")
	}
	if !deliveryExists(t, s, deadID) {
		t.Error("retention removed a dead-lettered row a human still needs")
	}
}

func backdateDelivery(t *testing.T, s *Store, id int64, at time.Time) {
	t.Helper()

	if _, err := s.pool.Exec(context.Background(),
		`UPDATE webhook_deliveries SET delivered_at = $2 WHERE id = $1`, id, at); err != nil {
		t.Fatalf("backdate delivery %d: %v", id, err)
	}
}

// Delivered and dead-lettered are terminal. A dispatcher whose lease expired
// while it was still sending can report an outcome late, and that must not undo
// a state the row has already reached.
func TestATerminalDeliveryCannotBeRescheduled(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	for _, tc := range []struct {
		name     string
		terminal func(id int64) error
	}{
		{"delivered", func(id int64) error { return s.MarkDelivered(ctx, id) }},
		{"dead-lettered", func(id int64) error { return s.DeadLetterDelivery(ctx, id, "receiver returned 400") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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

// Replay flips a parked delivery back to pending with a fresh retry budget, so a
// dispatcher picks it up again, and records the operator action in the admin
// audit log.
func TestReplayDeadLetterRequeues(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	queued := deliveriesFor(t, s, created.ID)[0]

	// Exhaust it: one claim (attempts -> 1) then park it.
	if !claims(t, s, ctx, queued.ID, time.Minute) {
		t.Fatal("fresh delivery was not claimable")
	}
	if err := s.DeadLetterDelivery(ctx, queued.ID, "receiver returned 500"); err != nil {
		t.Fatalf("DeadLetterDelivery: %v", err)
	}

	if err := s.ReplayDeadLetter(ctx, queued.ID, "ops-alice"); err != nil {
		t.Fatalf("ReplayDeadLetter: %v", err)
	}

	status, attempts, _, lastError := deliveryStatus(t, s, queued.ID)
	if status != DeliveryPending {
		t.Errorf("status = %q, want %q", status, DeliveryPending)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (fresh retry budget)", attempts)
	}
	if lastError != nil {
		t.Errorf("last_error = %v, want cleared", lastError)
	}
	// Due now, so the dispatcher claims it again.
	if !claims(t, s, ctx, queued.ID, time.Minute) {
		t.Error("replayed delivery was not claimable")
	}

	// The replay is on the admin audit trail, attributed and targeted.
	entries, err := s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == AdminActionWebhookReplay && e.TargetID == strconv.FormatInt(queued.ID, 10) {
			found = true
			if e.Actor != "ops-alice" {
				t.Errorf("replay audit actor = %q, want ops-alice", e.Actor)
			}
		}
	}
	if !found {
		t.Error("no webhook.replay entry in the admin audit log")
	}
}

// AuditWindowStats counts entries and distinct callers in a range, uncapped, for
// the console's window-over-window delta on the ARIA page.
func TestAuditWindowStats(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if total, callers, err := s.AuditWindowStats(ctx, tenantID, start, end); err != nil || total != 0 || callers != 0 {
		t.Fatalf("fresh tenant window = (%d, %d, %v), want (0, 0, nil)", total, callers, err)
	}

	createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	total, callers, err := s.AuditWindowStats(ctx, tenantID, start, end)
	if err != nil {
		t.Fatalf("AuditWindowStats: %v", err)
	}
	if total < 2 {
		t.Errorf("after two creates, total = %d, want >= 2", total)
	}
	if callers < 1 {
		t.Errorf("distinct callers = %d, want >= 1", callers)
	}
}

// WebhookDeliveryStatus reflects a delivery's lifecycle and reports a missing
// row as ErrNotFound, which is what lets the console poll a replay to its end.
func TestWebhookDeliveryStatus(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	d := deliveriesFor(t, s, created.ID)[0]

	if got, err := s.WebhookDeliveryStatus(ctx, d.ID); err != nil || got != DeliveryPending {
		t.Fatalf("fresh delivery status = (%q, %v), want (%q, nil)", got, err, DeliveryPending)
	}

	if err := s.MarkDelivered(ctx, d.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if got, _ := s.WebhookDeliveryStatus(ctx, d.ID); got != DeliveryDelivered {
		t.Errorf("delivered status = %q, want %q", got, DeliveryDelivered)
	}

	if _, err := s.WebhookDeliveryStatus(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing row = %v, want ErrNotFound", err)
	}
}

// Replay only touches a genuinely parked row: a missing id or one that isn't
// dead-lettered is ErrNotFound, so it can't disturb a delivered or in-flight
// delivery.
func TestReplayDeadLetterOnlyParkedRows(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	if err := s.ReplayDeadLetter(ctx, -1, "ops"); !errors.Is(err, ErrNotFound) {
		t.Errorf("replay of a missing row = %v, want ErrNotFound", err)
	}

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	pending := deliveriesFor(t, s, created.ID)[0]
	if err := s.ReplayDeadLetter(ctx, pending.ID, "ops"); !errors.Is(err, ErrNotFound) {
		t.Errorf("replay of a pending (not parked) row = %v, want ErrNotFound", err)
	}
}

// A receiver's error body reaches a text column, so it is bounded and
// sanitised: invalid UTF-8 or a NUL would otherwise fail the write and leave
// the delivery stuck in flight.
func TestAHostileErrorBodyStillRecords(t *testing.T) {
	s := newEventStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

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
