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

// fakeStore returns what it is told to and records how it was called.
type fakeStore struct {
	user *store.User

	// err fails every method, unless failOn names one — a handler path that
	// calls the store twice can then be driven to the second call.
	err    error
	failOn string

	lastCall string
	gotUser  *store.User
	gotRec   store.AuditRecord
}

func (f *fakeStore) fail(method string) error {
	f.lastCall = method
	if f.failOn != "" && f.failOn != method {
		return nil
	}
	return f.err
}

func (f *fakeStore) CreateUser(_ context.Context, u *store.User, rec store.AuditRecord) (*store.User, error) {
	f.gotUser, f.gotRec = u, rec
	return f.user, f.fail("CreateUser")
}

func (f *fakeStore) GetUser(_ context.Context, _ string) (*store.User, error) {
	return f.user, f.fail("GetUser")
}

func (f *fakeStore) ListUsers(_ context.Context, _, _ int, _ store.UserFilter) ([]store.User, int, error) {
	return nil, 0, f.fail("ListUsers")
}

// Both images are the canned user rather than a separately configured pair:
// no test here asserts on the before/after difference, and returning a non-nil
// Change keeps the fake inside the interface's contract.
func (f *fakeStore) UpdateUser(_ context.Context, _ string, u *store.User, rec store.AuditRecord) (*store.Change, error) {
	f.gotUser, f.gotRec = u, rec
	return &store.Change{Before: f.user, After: f.user}, f.fail("UpdateUser")
}

func (f *fakeStore) DeactivateUser(_ context.Context, _ string, rec store.AuditRecord) (*store.Change, error) {
	f.gotRec = rec
	return &store.Change{Before: f.user, After: f.user}, f.fail("DeactivateUser")
}

// The limiter and the base-URL override are cleared rather than left to the
// environment, so these stay deterministic however SCIM_RATE_LIMIT and
// SCIM_BASE_URL are set. Both have their own tests.
func fakeHandler(s UserStore) http.Handler {
	h := NewHandler(s, testToken)
	h.limiter = nil
	h.externalURL = ""
	return h.Routes()
}

func request(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, target, r)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+testToken)

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
		{"patch reading the user", http.MethodPatch, "/Users/" + nonexistentID, patchBody,
			&fakeStore{err: failure}, "GetUser"},
		// The read succeeds, so this reaches the write the first patch case
		// never gets to.
		{"patch writing the user", http.MethodPatch, "/Users/" + nonexistentID, patchBody,
			&fakeStore{user: sampleStoreUser(), err: failure, failOn: "UpdateUser"}, "UpdateUser"},
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
	if fake.gotRec.ActorToken == "" || fake.gotRec.ActorIP == "" {
		t.Errorf("audit record reached the store incomplete: %+v", fake.gotRec)
	}

	// What the store handed back: the response, mapped.
	got := decodeBody[User](t, rr)
	if got.ID != fake.user.ID {
		t.Errorf("id = %q, want %q", got.ID, fake.user.ID)
	}

	wantLocation := "http://example.com/Users/" + fake.user.ID
	if got.Meta == nil || got.Meta.Location != wantLocation {
		t.Errorf("meta.location = %+v, want %q", got.Meta, wantLocation)
	}
	if loc := rr.Header().Get("Location"); loc != wantLocation {
		t.Errorf("Location header = %q, want %q", loc, wantLocation)
	}
}
