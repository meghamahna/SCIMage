package scim

// Audit coverage for the handlers. Entries are read back out of Postgres,
// because the table is the record — the point of Phase 7's follow-up is that a
// mutation and its entry commit together.

import (
	"net/http"
	"testing"

	"github.com/meghamahna/SCIMage/internal/store"
)

// latestEntry returns the newest audit row.
func latestEntry(t *testing.T) store.AuditEntry {
	t.Helper()

	entries, err := testStore.ListAuditEntries(t.Context(), 1)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no audit entry was written")
	}
	return entries[0]
}

func countEntries(t *testing.T) int {
	t.Helper()

	var n int
	if err := cleanupPool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatalf("count audit entries: %v", err)
	}
	return n
}

func TestAuditCreate(t *testing.T) {
	requireDB(t)

	rr := do(t, http.MethodPost, "/Users", newUser())
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST = %d, want 201: %s", rr.Code, rr.Body)
	}
	created := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, created.ID) })

	got := latestEntry(t)
	if got.Action != store.ActionCreate {
		t.Errorf("action = %q, want %q", got.Action, store.ActionCreate)
	}
	if got.Result != store.ResultSuccess {
		t.Errorf("result = %q, want %q", got.Result, store.ResultSuccess)
	}
	if got.TargetID != created.ID {
		t.Errorf("targetId = %q, want %q", got.TargetID, created.ID)
	}
	if got.At.IsZero() {
		t.Error("at is zero")
	}
	if got.Before != nil {
		t.Errorf("before = %+v, want nil on a create", got.Before)
	}
	if got.After == nil || got.After.UserName != created.UserName {
		t.Errorf("after = %+v, want the created user", got.After)
	}
	if got.Actor.ActorToken != "tok_"+testActorSuffix(t) {
		t.Errorf("actor.token = %q, want the handler's fingerprint", got.Actor.ActorToken)
	}
	if got.Actor.ActorToken == testToken {
		t.Error("the audit entry stores the bearer token itself")
	}
}

// The before/after pair is the whole point: a reviewer has to see what changed.
func TestAuditReplaceRecordsBothImages(t *testing.T) {
	requireDB(t)

	created := decodeBody[User](t, do(t, http.MethodPost, "/Users", newUser()))
	t.Cleanup(func() { hardDelete(t, created.ID) })

	in := newUser()
	inactive := false
	in.Active = &inactive
	if rr := do(t, http.MethodPut, "/Users/"+created.ID, in); rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200: %s", rr.Code, rr.Body)
	}

	got := latestEntry(t)
	if got.Action != store.ActionReplace || got.Result != store.ResultSuccess {
		t.Fatalf("action/result = %q/%q, want replace/success", got.Action, got.Result)
	}
	if got.Before == nil || got.After == nil {
		t.Fatalf("before/after = %+v/%+v, want both", got.Before, got.After)
	}
	if got.Before.UserName != created.UserName {
		t.Errorf("before.userName = %q, want the pre-change %q", got.Before.UserName, created.UserName)
	}
	if got.After.UserName != in.UserName {
		t.Errorf("after.userName = %q, want %q", got.After.UserName, in.UserName)
	}
	if !got.Before.Active {
		t.Error("before.active = false, want the pre-change value true")
	}
	if got.After.Active {
		t.Error("after.active = true, want false")
	}
}

func TestAuditDeactivate(t *testing.T) {
	requireDB(t)

	created := decodeBody[User](t, do(t, http.MethodPost, "/Users", newUser()))
	t.Cleanup(func() { hardDelete(t, created.ID) })

	if rr := do(t, http.MethodDelete, "/Users/"+created.ID, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", rr.Code)
	}

	got := latestEntry(t)
	if got.Action != store.ActionDeactivate || got.Result != store.ResultSuccess {
		t.Fatalf("action/result = %q/%q, want deactivate/success", got.Action, got.Result)
	}
	if got.Before == nil || !got.Before.Active {
		t.Errorf("before.active = %+v, want true", got.Before)
	}
	if got.After == nil || got.After.Active {
		t.Errorf("after.active = %+v, want false", got.After)
	}
}

// A refused mutation is the interesting one — a burst of denials is a signal,
// and it's invisible if only successes are recorded.
func TestAuditRecordsRefusals(t *testing.T) {
	requireDB(t)

	created := decodeBody[User](t, do(t, http.MethodPost, "/Users", newUser()))
	t.Cleanup(func() { hardDelete(t, created.ID) })

	dup := newUser()
	dup.UserName = created.UserName

	for _, tc := range []struct {
		name           string
		method, target string
		body           any
		wantStatus     int
		wantAction     string
	}{
		{"duplicate create", http.MethodPost, "/Users", dup, http.StatusConflict, store.ActionCreate},
		{"replace a missing user", http.MethodPut, "/Users/" + nonexistentID, newUser(), http.StatusNotFound, store.ActionReplace},
		{"delete a missing user", http.MethodDelete, "/Users/" + nonexistentID, nil, http.StatusNotFound, store.ActionDeactivate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rr := do(t, tc.method, tc.target, tc.body); rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}

			got := latestEntry(t)
			if got.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Result != store.ResultDenied {
				t.Errorf("result = %q, want %q", got.Result, store.ResultDenied)
			}
			if got.Detail == "" {
				t.Error("detail is empty — a refusal should say why")
			}
		})
	}
}

// Reads must not be audited: they'd bury the mutations SAGE is looking for.
func TestAuditIgnoresReads(t *testing.T) {
	requireDB(t)

	created := decodeBody[User](t, do(t, http.MethodPost, "/Users", newUser()))
	t.Cleanup(func() { hardDelete(t, created.ID) })

	before := countEntries(t)

	do(t, http.MethodGet, "/Users/"+created.ID, nil)
	do(t, http.MethodGet, "/Users", nil)

	if after := countEntries(t); after != before {
		t.Errorf("reads added %d audit entries, want 0", after-before)
	}
}

// testActorSuffix recomputes the fingerprint the handler derives, so the
// assertion doesn't hard-code a hash of the test token.
func testActorSuffix(t *testing.T) string {
	t.Helper()

	actor := NewHandler(nil, testToken).actor
	return actor[len("tok_"):]
}
