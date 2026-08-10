package scim

// Audit coverage for the handlers. The entries are what a reviewer and SAGE
// read, so the assertions are on the recorded content, not just that a line
// appeared.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meghamahna/SCIMage/internal/audit"
	"github.com/meghamahna/SCIMage/internal/store"
)

// auditEnv is a handler whose audit stream the test can read back.
type auditEnv struct {
	handler http.Handler
	log     *bytes.Buffer
}

func newAuditEnv(t *testing.T) auditEnv {
	t.Helper()
	requireDB(t)

	dsn, err := store.DSNFromEnv()
	if err != nil {
		t.Skipf("no database configured (%v)", err)
	}
	s, err := store.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect store: %v", err)
	}
	t.Cleanup(s.Close)

	t.Setenv("SCIM_RATE_LIMIT", "0")
	buf := &bytes.Buffer{}
	return auditEnv{
		handler: NewHandler(s, testToken, audit.New(buf)).Routes(),
		log:     buf,
	}
}

func (e auditEnv) do(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	prev := handler
	handler = e.handler
	defer func() { handler = prev }()

	return do(t, method, target, body)
}

// entries parses every line written so far.
func (e auditEnv) entries(t *testing.T) []audit.Entry {
	t.Helper()

	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(e.log.String()), "\n") {
		if line == "" {
			continue
		}
		var entry audit.Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit line is not JSON: %q: %v", line, err)
		}
		out = append(out, entry)
	}
	return out
}

func (e auditEnv) last(t *testing.T) audit.Entry {
	t.Helper()

	all := e.entries(t)
	if len(all) == 0 {
		t.Fatal("no audit entry was written")
	}
	return all[len(all)-1]
}

func TestAuditCreate(t *testing.T) {
	env := newAuditEnv(t)

	rr := env.do(t, http.MethodPost, "/Users", newUser())
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST = %d, want 201: %s", rr.Code, rr.Body)
	}
	created := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, created.ID) })

	got := env.last(t)
	if got.Action != audit.ActionCreate {
		t.Errorf("action = %q, want %q", got.Action, audit.ActionCreate)
	}
	if got.Result != audit.ResultSuccess {
		t.Errorf("result = %q, want %q", got.Result, audit.ResultSuccess)
	}
	if got.TargetID != created.ID {
		t.Errorf("targetId = %q, want %q", got.TargetID, created.ID)
	}
	if got.Time.IsZero() {
		t.Error("time is zero")
	}
	if got.Before != nil {
		t.Errorf("before = %+v, want nil on a create", got.Before)
	}
	if got.After == nil || got.After.UserName != created.UserName {
		t.Errorf("after = %+v, want the created user", got.After)
	}
	if !strings.HasPrefix(got.Actor.Token, "tok_") {
		t.Errorf("actor.token = %q, want a tok_ fingerprint", got.Actor.Token)
	}
	if strings.Contains(env.log.String(), testToken) {
		t.Error("the audit log contains the bearer token")
	}
}

// The before/after pair is the whole point: a reviewer has to see what changed.
func TestAuditReplaceRecordsBothImages(t *testing.T) {
	env := newAuditEnv(t)

	rr := env.do(t, http.MethodPost, "/Users", newUser())
	created := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, created.ID) })

	in := newUser()
	inactive := false
	in.Active = &inactive
	if rr := env.do(t, http.MethodPut, "/Users/"+created.ID, in); rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200: %s", rr.Code, rr.Body)
	}

	got := env.last(t)
	if got.Action != audit.ActionReplace || got.Result != audit.ResultSuccess {
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
	env := newAuditEnv(t)

	rr := env.do(t, http.MethodPost, "/Users", newUser())
	created := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, created.ID) })

	if rr := env.do(t, http.MethodDelete, "/Users/"+created.ID, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", rr.Code)
	}

	got := env.last(t)
	if got.Action != audit.ActionDeactivate || got.Result != audit.ResultSuccess {
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
	env := newAuditEnv(t)

	rr := env.do(t, http.MethodPost, "/Users", newUser())
	created := decodeBody[User](t, rr)
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
		{"duplicate create", http.MethodPost, "/Users", dup, http.StatusConflict, audit.ActionCreate},
		{"replace a missing user", http.MethodPut, "/Users/" + nonexistentID, newUser(), http.StatusNotFound, audit.ActionReplace},
		{"delete a missing user", http.MethodDelete, "/Users/" + nonexistentID, nil, http.StatusNotFound, audit.ActionDeactivate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rr := env.do(t, tc.method, tc.target, tc.body); rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.wantStatus, rr.Body)
			}

			got := env.last(t)
			if got.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Result != audit.ResultDenied {
				t.Errorf("result = %q, want %q", got.Result, audit.ResultDenied)
			}
			if got.Detail == "" {
				t.Error("detail is empty — a refusal should say why")
			}
		})
	}
}

// Reads must not be audited: they'd bury the mutations SAGE is looking for.
func TestAuditIgnoresReads(t *testing.T) {
	env := newAuditEnv(t)

	rr := env.do(t, http.MethodPost, "/Users", newUser())
	created := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, created.ID) })

	before := len(env.entries(t))

	env.do(t, http.MethodGet, "/Users/"+created.ID, nil)
	env.do(t, http.MethodGet, "/Users", nil)

	if after := len(env.entries(t)); after != before {
		t.Errorf("reads added %d audit entries, want 0", after-before)
	}
}
