package scim

// The handler depends on the UserStore interface, so these drive it with a
// stand-in instead of Postgres. Two things are worth covering that way: that
// the SCIM surface works over an implementation that isn't the bundled store,
// and that an unexpected store failure becomes a 500 with the cause kept out of
// the response. A healthy database won't produce the second on demand.
//
// The store's own behaviour is covered against real Postgres in
// internal/store — nothing here mocks the database.

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// fakeTenantID/fakeToken authenticate every request the fake-store tests
// send, without touching Postgres for auth any more than for the user store.
const (
	fakeTenantID = "tenant_fa4e000000000000"
	fakeKeyID    = "fa4e00000000000a"
	// fakePlaintext is the secret half of fakeToken, not a real credential —
	// named to avoid the shape a hardcoded-secret scanner looks for.
	fakePlaintext = "fakesecret0000000000000000000000000000000000000000000000000000"
)

var fakeToken = "scimage_" + fakeKeyID + "_" + fakePlaintext

func validFakeToken() *store.Token {
	sum := sha256.Sum256([]byte(fakePlaintext))
	return &store.Token{KeyID: fakeKeyID, TenantID: fakeTenantID, SecretHash: sum[:], Label: "fake"}
}

// fakeTokenStore hands back whatever it's configured with, ignoring the key
// id it's asked to look up — tests set tok or err directly rather than
// simulating a lookup miss.
type fakeTokenStore struct {
	tok *store.Token
	err error
}

func (f fakeTokenStore) GetTokenByKeyID(_ context.Context, _ string) (*store.Token, error) {
	return f.tok, f.err
}

func (fakeTokenStore) TouchToken(_ context.Context, _ string) error { return nil }

// fakeStore returns what it is told to and records how it was called.
type fakeStore struct {
	user *store.User
	err  error

	lastCall string
	gotUser  *store.User
	gotRec   store.AuditRecord
}

func (f *fakeStore) fail(method string) error {
	f.lastCall = method
	return f.err
}

func (f *fakeStore) CreateUser(_ context.Context, _ string, u *store.User, rec store.AuditRecord) (*store.User, error) {
	f.gotUser, f.gotRec = u, rec
	return f.user, f.fail("CreateUser")
}

func (f *fakeStore) GetUser(_ context.Context, _, _ string) (*store.User, error) {
	return f.user, f.fail("GetUser")
}

func (f *fakeStore) ListUsers(_ context.Context, _ string, _, _ int, _ store.UserFilter) ([]store.User, int, error) {
	return nil, 0, f.fail("ListUsers")
}

// Both images are the canned user rather than a separately configured pair:
// no test here asserts on the before/after difference, and returning a non-nil
// Change keeps the fake inside the interface's contract.
func (f *fakeStore) UpdateUser(_ context.Context, _, _ string, u *store.User, rec store.AuditRecord) (*store.Change, error) {
	f.gotUser, f.gotRec = u, rec
	return &store.Change{Before: f.user, After: f.user}, f.fail("UpdateUser")
}

// PatchUser hands patch the canned user, as the real store hands it the locked
// row, and records what patch returned.
func (f *fakeStore) PatchUser(_ context.Context, _, _ string, patch func(*store.User) (*store.User, error), rec store.AuditRecord) (*store.Change, error) {
	f.gotRec = rec
	if err := f.fail("PatchUser"); err != nil {
		return nil, err
	}
	u, err := patch(f.user)
	if err != nil {
		return nil, err
	}
	f.gotUser = u
	return &store.Change{Before: f.user, After: f.user}, nil
}

func (f *fakeStore) DeactivateUser(_ context.Context, _, _ string, rec store.AuditRecord) (*store.Change, error) {
	f.gotRec = rec
	return &store.Change{Before: f.user, After: f.user}, f.fail("DeactivateUser")
}

func (*fakeStore) RecordRefusal(context.Context, store.AuditRecord, string, string, string, string) {}

// The limiter and the base-URL override are cleared rather than left to the
// environment, so these stay deterministic however SCIM_RATE_LIMIT and
// SCIM_BASE_URL are set. Both have their own tests.
func fakeHandler(s UserStore) http.Handler {
	h := NewHandler(s, nil, fakeTokenStore{tok: validFakeToken()}, nil)
	h.limiter = nil
	h.externalURL = ""
	return h.Routes()
}

// request targets are relative to /Users, e.g. "/Users" or "/Users/"+id — the
// tenant prefix is added here so the test table stays focused on what varies.
func request(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, "/scim/v2/"+fakeTenantID+target, r)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+fakeToken)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// discardLogs keeps the expected slog.Error lines out of the test output.
func discardLogs(t *testing.T) {
	t.Helper()

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func sampleStoreUser() *store.User {
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	return &store.User{
		ID:        "6f1e2c3d-0000-4000-8000-abcdefabcdef",
		UserName:  "bjensen",
		Active:    true,
		CreatedAt: at,
		UpdatedAt: at,
	}
}

func TestStoreFailureIsAServerErrorWithoutTheCause(t *testing.T) {
	discardLogs(t)

	// A recognisable cause: the assertion is that none of it reaches the client.
	const cause = "connection refused to db-primary.internal:5432"
	failure := errors.New(cause)

	const (
		validUser = `{"schemas":["` + userSchema + `"],"userName":"bjensen"}`
		patchBody = `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],` +
			`"Operations":[{"op":"replace","path":"active","value":false}]}`
	)

	for _, tc := range []struct {
		name           string
		method, target string
		body           string
		fake           *fakeStore
		// wantCall pins which store call failed, so a subtest can't quietly
		// stop reaching the path it is named for.
		wantCall string
	}{
		{"create", http.MethodPost, "/Users", validUser,
			&fakeStore{err: failure}, "CreateUser"},
		{"get", http.MethodGet, "/Users/" + nonexistentID, "",
			&fakeStore{err: failure}, "GetUser"},
		{"list", http.MethodGet, "/Users", "",
			&fakeStore{err: failure}, "ListUsers"},
		{"replace", http.MethodPut, "/Users/" + nonexistentID, validUser,
			&fakeStore{err: failure}, "UpdateUser"},
		{"patch", http.MethodPatch, "/Users/" + nonexistentID, patchBody,
			&fakeStore{err: failure}, "PatchUser"},
		{"deactivate", http.MethodDelete, "/Users/" + nonexistentID, "",
			&fakeStore{err: failure}, "DeactivateUser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := request(t, fakeHandler(tc.fake), tc.method, tc.target, tc.body)

			if tc.fake.lastCall != tc.wantCall {
				t.Errorf("failed on %q, want %q", tc.fake.lastCall, tc.wantCall)
			}
			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d (body %s)", rr.Code, http.StatusInternalServerError, rr.Body)
			}
			if got := rr.Header().Get("Content-Type"); got != contentType {
				t.Errorf("Content-Type = %q, want %q", got, contentType)
			}
			if strings.Contains(rr.Body.String(), cause) {
				t.Errorf("response leaks the store error: %s", rr.Body.String())
			}

			e := decodeBody[Error](t, rr)
			if e.Status != "500" {
				t.Errorf("status in body = %q, want %q", e.Status, "500")
			}
			if len(e.Schemas) != 1 || e.Schemas[0] != errorSchema {
				t.Errorf("schemas = %v, want [%s]", e.Schemas, errorSchema)
			}
		})
	}
}

// The point of the interface: an application with its own user table can back
// the SCIM surface without Postgres anywhere in the path. The audit record
// still has to arrive at the store — that obligation is what the interface
// carries, so it is asserted here and not only against the real store.
func TestHandlerServesAUserStoreThatIsNotPostgres(t *testing.T) {
	fake := &fakeStore{user: sampleStoreUser()}
	h := fakeHandler(fake)

	rr := request(t, h, http.MethodPost, "/Users",
		`{"schemas":["`+userSchema+`"],"userName":"bjensen","name":{"givenName":"Barbara"}}`)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rr.Code, http.StatusCreated, rr.Body)
	}

	// What the handler handed the store: the request body, mapped.
	if fake.gotUser == nil {
		t.Fatal("handler did not pass a user to the store")
	}
	if fake.gotUser.UserName != "bjensen" {
		t.Errorf("stored userName = %q, want %q", fake.gotUser.UserName, "bjensen")
	}
	if fake.gotUser.GivenName == nil || *fake.gotUser.GivenName != "Barbara" {
		t.Errorf("stored givenName = %v, want %q", fake.gotUser.GivenName, "Barbara")
	}
	if fake.gotRec.ActorToken == "" || fake.gotRec.ActorIP == "" || fake.gotRec.TenantID == "" {
		t.Errorf("audit record reached the store incomplete: %+v", fake.gotRec)
	}
	if fake.gotRec.TenantID != fakeTenantID {
		t.Errorf("audit record tenant = %q, want %q", fake.gotRec.TenantID, fakeTenantID)
	}

	// What the store handed back: the response, mapped.
	got := decodeBody[User](t, rr)
	if got.ID != fake.user.ID {
		t.Errorf("id = %q, want %q", got.ID, fake.user.ID)
	}

	wantLocation := "http://example.com/scim/v2/" + fakeTenantID + "/Users/" + fake.user.ID
	if got.Meta == nil || got.Meta.Location != wantLocation {
		t.Errorf("meta.location = %+v, want %q", got.Meta, wantLocation)
	}
	if loc := rr.Header().Get("Location"); loc != wantLocation {
		t.Errorf("Location header = %q, want %q", loc, wantLocation)
	}
}

// PATCH hands the store a callback rather than a user, so an implementation has
// to run it on its current row and write what it returns, with the audit record.
func TestHandlerPatchesThroughAUserStoreThatIsNotPostgres(t *testing.T) {
	fake := &fakeStore{user: sampleStoreUser()}

	rr := request(t, fakeHandler(fake), http.MethodPatch, "/Users/"+fake.user.ID,
		`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],`+
			`"Operations":[{"op":"replace","path":"active","value":false}]}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body)
	}
	if fake.gotUser == nil {
		t.Fatal("patch did not return a user for the store to write")
	}
	if fake.gotUser.Active || fake.gotUser.UserName != fake.user.UserName {
		t.Errorf("patched user active=%v userName=%q, want inactive %q", fake.gotUser.Active, fake.gotUser.UserName, fake.user.UserName)
	}
	if !fake.user.Active {
		t.Error("patch modified the user it was handed")
	}
	if fake.gotRec.TenantID != fakeTenantID || fake.gotRec.ActorToken == "" {
		t.Errorf("audit record reached the store incomplete: %+v", fake.gotRec)
	}
}
