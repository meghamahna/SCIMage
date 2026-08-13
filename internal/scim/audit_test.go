package scim

// Audit coverage for the handlers. Entries are read back out of Postgres,
// because the table is the record — the point of Phase 7's follow-up is that a
// mutation and its entry commit together.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/meghamahna/SCIMage/internal/store"
)

// decodeAuditUser unmarshals a User audit image, the same reason
// internal/store's own audit tests need the equivalent helper: Before/After
// are raw JSON now that one table serves both resource kinds.
func decodeAuditUser(t *testing.T, raw json.RawMessage) store.User {
	t.Helper()

	if raw == nil {
		t.Fatal("image is nil")
	}
	var u store.User
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatalf("unmarshal audited user: %v", err)
	}
	return u
}

func decodeAuditGroup(t *testing.T, raw json.RawMessage) store.Group {
	t.Helper()

	if raw == nil {
		t.Fatal("image is nil")
	}
	var g store.Group
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("unmarshal audited group: %v", err)
	}
	return g
}

// latestEntry returns the newest audit row for the test tenant.
func latestEntry(t *testing.T) store.AuditEntry {
	t.Helper()

	entries, err := testStore.ListAuditEntries(t.Context(), testTenantID, 1)
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
	if err := cleanupPool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1`, testTenantID).Scan(&n); err != nil {
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
		t.Errorf("before = %s, want nil on a create", got.Before)
	}
	if after := decodeAuditUser(t, got.After); after.UserName != created.UserName {
		t.Errorf("after.userName = %q, want %q", after.UserName, created.UserName)
	}
	if got.Actor.ActorToken != testKeyID(t) {
		t.Errorf("actor.token = %q, want the token's key id %q", got.Actor.ActorToken, testKeyID(t))
	}
	if got.Actor.ActorToken == testToken {
		t.Error("the audit entry stores the bearer token itself")
	}
	if got.Actor.TenantID != testTenantID {
		t.Errorf("actor.tenantId = %q, want %q", got.Actor.TenantID, testTenantID)
	}
}

// The before/after pair is the whole point: a reviewer has to see what changed.
func TestAuditReplaceRecordsBothImages(t *testing.T) {
	requireDB(t)

	created := decodeBody[User](t, do(t, http.MethodPost, "/Users", newUser()))
	t.Cleanup(func() { hardDelete(t, created.ID) })

	in := newUser()
	inactive := Bool(false)
	in.Active = &inactive
	if rr := do(t, http.MethodPut, "/Users/"+created.ID, in); rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200: %s", rr.Code, rr.Body)
	}

	got := latestEntry(t)
	if got.Action != store.ActionReplace || got.Result != store.ResultSuccess {
		t.Fatalf("action/result = %q/%q, want replace/success", got.Action, got.Result)
	}
	if got.Before == nil || got.After == nil {
		t.Fatalf("before/after = %s/%s, want both", got.Before, got.After)
	}
	before := decodeAuditUser(t, got.Before)
	after := decodeAuditUser(t, got.After)

	if before.UserName != created.UserName {
		t.Errorf("before.userName = %q, want the pre-change %q", before.UserName, created.UserName)
	}
	if after.UserName != in.UserName {
		t.Errorf("after.userName = %q, want %q", after.UserName, in.UserName)
	}
	if !before.Active {
		t.Error("before.active = false, want the pre-change value true")
	}
	if after.Active {
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
	if got.Before == nil || !decodeAuditUser(t, got.Before).Active {
		t.Errorf("before.active = %s, want true", got.Before)
	}
	if got.After == nil || decodeAuditUser(t, got.After).Active {
		t.Errorf("after.active = %s, want false", got.After)
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

// Groups share audit_log with Users, so resource_type is what tells a
// reviewer which struct an entry's before/after images decode as.
func TestAuditGroupCreateAndDelete(t *testing.T) {
	requireDB(t)

	created := createGroup(t, newGroup())

	got := latestEntry(t)
	if got.ResourceType != store.ResourceGroup || got.Action != store.ActionCreate {
		t.Fatalf("resourceType/action = %q/%q, want group/create", got.ResourceType, got.Action)
	}
	if after := decodeAuditGroup(t, got.After); after.DisplayName != created.DisplayName {
		t.Errorf("after.displayName = %q, want %q", after.DisplayName, created.DisplayName)
	}

	rr := do(t, http.MethodDelete, "/Groups/"+created.ID, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204: %s", rr.Code, rr.Body)
	}

	got = latestEntry(t)
	// Distinct from ActionDeactivate: a group has no active attribute, so
	// removing one is a real deletion, not a soft one.
	if got.Action != store.ActionDelete {
		t.Errorf("action = %q, want %q", got.Action, store.ActionDelete)
	}
	if got.After != nil {
		t.Errorf("after = %s, want nil on a delete", got.After)
	}
	if before := decodeAuditGroup(t, got.Before); before.ID != created.ID {
		t.Errorf("before.id = %q, want %q", before.ID, created.ID)
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

// testKeyID recovers the key id half of testToken, so the assertion doesn't
// hard-code it — it's generated fresh by TestMain on every run.
func testKeyID(t *testing.T) string {
	t.Helper()

	keyID, _, ok := store.ParseToken(testToken)
	if !ok {
		t.Fatalf("testToken %q does not parse", testToken)
	}
	return keyID
}
