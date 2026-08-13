package scim

// Handler tests via httptest, against the real store and compose Postgres.
// Run with `make test`; they skip when no database is configured.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meghamahna/SCIMage/internal/store"
)

const nonexistentID = "00000000-0000-4000-8000-000000000000"

var (
	handler http.Handler
	// testStore is the same store the handler uses, so audit tests can read the
	// entries back out of Postgres.
	testStore *store.Store
	// A second handle purely so tests can hard-delete; the store only soft-deletes.
	cleanupPool *pgxpool.Pool
	skipReason  string

	// testTenantID/testToken authenticate every request the suite sends. One
	// tenant for the whole file — the point of the isolation tests elsewhere is
	// that two tenants can't see each other's data, not that every test needs
	// its own.
	testTenantID string
	testToken    string
)

func TestMain(m *testing.M) {
	dsn, err := store.DSNFromEnv()
	if err != nil {
		skipReason = err.Error()
		os.Exit(m.Run())
	}

	ctx := context.Background()
	testStore, err = store.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect store: %v\n", err)
		os.Exit(1)
	}
	cleanupPool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect cleanup pool: %v\n", err)
		os.Exit(1)
	}
	// The suite fires hundreds of requests as fast as it can and would trip its
	// own rate limit. The limiter is covered directly in ratelimit_test.go.
	os.Setenv("SCIM_RATE_LIMIT", "0")

	// A fixed literal name would collide with itself on a second run whose
	// process never reached the cleanup below (killed, crashed, or any
	// early os.Exit above) — tenant names are unique now, so the leftover
	// row would block every run after that one, permanently. Unique per
	// run is the same reasoning newTestTenant uses in internal/store.
	tenantName := fmt.Sprintf("handler-test-tenant-%d", time.Now().UnixNano())
	tenant, err := testStore.CreateTenant(ctx, tenantName, "test-suite")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test tenant: %v\n", err)
		os.Exit(1)
	}
	testTenantID = tenant.ID

	plaintext, _, err := testStore.IssueToken(ctx, testTenantID, "handler test", "test-suite", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "issue test token: %v\n", err)
		os.Exit(1)
	}
	testToken = plaintext

	handler = NewHandler(testStore, testStore, testStore, testStore).Routes()

	code := m.Run()

	// The suite's rows outlive the users they describe, by design (audit_log,
	// webhook_deliveries), so cleanup removes everything under the test tenant
	// rather than leaving it in a shared database.
	for _, q := range []string{
		`DELETE FROM webhook_deliveries WHERE tenant_id = $1`,
		`DELETE FROM audit_log WHERE tenant_id = $1`,
		`DELETE FROM admin_audit_log WHERE tenant_id = $1`,
		`DELETE FROM scim_tokens WHERE tenant_id = $1`,
		`DELETE FROM tenant_attributes WHERE tenant_id = $1`,
		`DELETE FROM group_members WHERE tenant_id = $1`,
		`DELETE FROM groups WHERE tenant_id = $1`,
		`DELETE FROM users WHERE tenant_id = $1`,
		`DELETE FROM tenants WHERE id = $1`,
	} {
		if _, err := cleanupPool.Exec(ctx, q, testTenantID); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup tenant %s: %v\n", testTenantID, err)
		}
	}

	testStore.Close()
	cleanupPool.Close()
	os.Exit(code)
}

func requireDB(t *testing.T) {
	t.Helper()

	if skipReason != "" {
		t.Skipf("no database configured — run `make test` (%s)", skipReason)
	}
}

var userNameSeq atomic.Int64

func uniqueUserName() string {
	return fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), userNameSeq.Add(1))
}

// A string body is sent verbatim, so tests can post raw or malformed JSON.
func do(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		r = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, "/scim/v2/"+testTenantID+target, r)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+testToken)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeBody[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()

	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	return out
}

func hardDelete(t *testing.T, id string) {
	t.Helper()

	if _, err := cleanupPool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id); err != nil {
		t.Errorf("cleanup: delete user %q: %v", id, err)
	}
}

// firstUserIDs reads the first n users straight from the database, in the order
// the store lists them. Pagination assertions need an oracle that doesn't go
// through the arithmetic under test — comparing one paged call against another
// hides an off-by-one, since both shift together.
func firstUserIDs(t *testing.T, n int) []string {
	t.Helper()

	rows, err := cleanupPool.Query(context.Background(),
		`SELECT id FROM users WHERE tenant_id = $1 ORDER BY created_at, id LIMIT $2`, testTenantID, n)
	if err != nil {
		t.Fatalf("read expected user order: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan expected user order: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read expected user order: %v", err)
	}
	return ids
}

func createUser(t *testing.T, in User) User {
	t.Helper()

	rr := do(t, http.MethodPost, "/Users", in)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /Users = %d, want 201: %s", rr.Code, rr.Body)
	}

	out := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, out.ID) })
	return out
}

func newUser() User {
	return User{
		Schemas:  []string{userSchema},
		UserName: uniqueUserName(),
		Name:     &Name{GivenName: "Barbara", FamilyName: "Jensen"},
		Emails:   []Email{{Value: "bjensen@example.com", Primary: true}},
	}
}

func active(t *testing.T, u User) bool {
	t.Helper()

	if u.Active == nil {
		t.Fatal("active is absent from the response")
	}
	return bool(*u.Active)
}

func TestCreateUser(t *testing.T) {
	requireDB(t)

	t.Run("201 with Location and a full resource", func(t *testing.T) {
		in := newUser()
		rr := do(t, http.MethodPost, "/Users", in)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}
		if got := rr.Header().Get("Content-Type"); got != contentType {
			t.Errorf("Content-Type = %q, want %q", got, contentType)
		}

		out := decodeBody[User](t, rr)
		t.Cleanup(func() { hardDelete(t, out.ID) })

		if out.ID == "" {
			t.Error("id is empty")
		}
		if out.UserName != in.UserName {
			t.Errorf("userName = %q, want %q", out.UserName, in.UserName)
		}
		if out.Meta == nil || out.Meta.ResourceType != "User" {
			t.Fatalf("meta = %+v, want resourceType User", out.Meta)
		}
		if loc := rr.Header().Get("Location"); loc == "" || loc != out.Meta.Location {
			t.Errorf("Location = %q, meta.location = %q — want both set and equal", loc, out.Meta.Location)
		}
		if !strings.HasSuffix(out.Meta.Location, "/Users/"+out.ID) {
			t.Errorf("meta.location = %q, want it to end in /Users/%s", out.Meta.Location, out.ID)
		}
		if out.Name == nil || out.Name.GivenName != "Barbara" {
			t.Errorf("name = %+v, want givenName Barbara", out.Name)
		}
		if len(out.Emails) != 1 || out.Emails[0].Value != "bjensen@example.com" {
			t.Errorf("emails = %+v, want the primary address", out.Emails)
		}
	})

	// SCIM defaults an omitted active to true.
	t.Run("active defaults to true when omitted", func(t *testing.T) {
		if out := createUser(t, newUser()); !active(t, out) {
			t.Error("active = false, want true")
		}
	})

	t.Run("active=false is honoured", func(t *testing.T) {
		in := newUser()
		inactive := Bool(false)
		in.Active = &inactive

		if out := createUser(t, in); active(t, out) {
			t.Error("active = true, want false")
		}
	})

	t.Run("Location resolves to the created user", func(t *testing.T) {
		out := createUser(t, newUser())

		rr := do(t, http.MethodGet, "/Users/"+out.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", out.Meta.Location, rr.Code)
		}
		if got := decodeBody[User](t, rr); got.ID != out.ID {
			t.Errorf("id = %q, want %q", got.ID, out.ID)
		}
	})

	// Real IdPs send attributes this server doesn't model, plus extension URNs.
	// Rejecting them would break every actual client.
	t.Run("unknown fields and extension schemas are accepted", func(t *testing.T) {
		body := fmt.Sprintf(`{
		  "schemas": [%q, "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"],
		  "userName": %q,
		  "externalId": "0oa1b2c3d4",
		  "groups": [{"value": "abc", "display": "Everyone"}],
		  "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User": {"department": "Eng"}
		}`, userSchema, uniqueUserName())

		rr := do(t, http.MethodPost, "/Users", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}
		t.Cleanup(func() { hardDelete(t, decodeBody[User](t, rr).ID) })
	})
}

func TestCreateUserConflict(t *testing.T) {
	requireDB(t)

	existing := createUser(t, newUser())

	for _, tc := range []struct{ name, userName string }{
		{"exact duplicate", existing.UserName},
		{"different case", strings.ToUpper(existing.UserName)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newUser()
			in.UserName = tc.userName

			rr := do(t, http.MethodPost, "/Users", in)
			if rr.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body)
			}

			scimErr := decodeBody[Error](t, rr)
			if scimErr.Status != "409" {
				t.Errorf("status = %q, want \"409\"", scimErr.Status)
			}
			if scimErr.ScimType != "uniqueness" {
				t.Errorf("scimType = %q, want uniqueness", scimErr.ScimType)
			}
			if len(scimErr.Schemas) != 1 || scimErr.Schemas[0] != errorSchema {
				t.Errorf("schemas = %v, want the Error schema", scimErr.Schemas)
			}
		})
	}
}

func TestCreateUserBadRequest(t *testing.T) {
	requireDB(t)

	t.Run("malformed JSON is invalidSyntax", func(t *testing.T) {
		rr := do(t, http.MethodPost, "/Users", `{"userName":`)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if got := decodeBody[Error](t, rr).ScimType; got != "invalidSyntax" {
			t.Errorf("scimType = %q, want invalidSyntax", got)
		}
	})

	// An over-long attribute must not reach the database: user_name is indexed,
	// and a btree entry over ~2704 bytes is an error, i.e. a 500.
	oversized := newUser()
	oversized.UserName = strings.Repeat("a", 4000)

	missingSchema := newUser()
	missingSchema.Schemas = nil

	wrongSchema := newUser()
	wrongSchema.Schemas = []string{"urn:totally:bogus"}

	blank := newUser()
	blank.UserName = "   "

	for _, tc := range []struct {
		name string
		body User
	}{
		{"missing userName", User{Schemas: []string{userSchema}}},
		{"blank userName", blank},
		{"oversized userName", oversized},
		{"missing schemas", missingSchema},
		{"unrecognised schemas", wrongSchema},
	} {
		t.Run(tc.name+" is 400 invalidValue", func(t *testing.T) {
			rr := do(t, http.MethodPost, "/Users", tc.body)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
			}
			if got := decodeBody[Error](t, rr).ScimType; got != "invalidValue" {
				t.Errorf("scimType = %q, want invalidValue", got)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	requireDB(t)

	t.Run("returns the resource", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := do(t, http.MethodGet, "/Users/"+created.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[User](t, rr)
		if got.UserName != created.UserName {
			t.Errorf("userName = %q, want %q", got.UserName, created.UserName)
		}
		if len(got.Schemas) != 1 || got.Schemas[0] != userSchema {
			t.Errorf("schemas = %v, want the User schema", got.Schemas)
		}
	})

	// A junk id is a 404, not a 500.
	for _, tc := range []struct{ name, id string }{
		{"unknown id", nonexistentID},
		{"malformed id", "not-a-uuid"},
	} {
		t.Run(tc.name+" is 404", func(t *testing.T) {
			rr := do(t, http.MethodGet, "/Users/"+tc.id, nil)

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
			}
			if got := decodeBody[Error](t, rr).Status; got != "404" {
				t.Errorf("status = %q, want \"404\"", got)
			}
		})
	}
}

func TestListUsers(t *testing.T) {
	requireDB(t)

	const created = 3
	for range created {
		createUser(t, newUser())
	}

	all := decodeBody[ListResponse](t, do(t, http.MethodGet, "/Users", nil))

	t.Run("returns a ListResponse", func(t *testing.T) {
		if len(all.Schemas) != 1 || all.Schemas[0] != listSchema {
			t.Errorf("schemas = %v, want the ListResponse schema", all.Schemas)
		}
		if all.TotalResults < created {
			t.Errorf("totalResults = %d, want at least %d", all.TotalResults, created)
		}
		if all.StartIndex != 1 {
			t.Errorf("startIndex = %d, want 1 by default", all.StartIndex)
		}
		if all.ItemsPerPage != len(all.Resources) {
			t.Errorf("itemsPerPage = %d, but Resources has %d", all.ItemsPerPage, len(all.Resources))
		}
	})

	// RFC 7644 §3.4.2 capitalises Resources, which a lowercase key would break.
	t.Run("Resources is capitalised", func(t *testing.T) {
		rr := do(t, http.MethodGet, "/Users?count=1", nil)

		raw := decodeBody[map[string]json.RawMessage](t, rr)
		if _, ok := raw["Resources"]; !ok {
			t.Errorf("no \"Resources\" key in %s", rr.Body)
		}
	})

	// startIndex is 1-based, so page N begins at the Nth user in the table.
	t.Run("startIndex is 1-based", func(t *testing.T) {
		want := firstUserIDs(t, 3)
		if len(want) < 3 {
			t.Skipf("need at least 3 users, have %d", len(want))
		}

		for i, wantID := range want {
			start := i + 1
			page := decodeBody[ListResponse](t, do(t, http.MethodGet,
				fmt.Sprintf("/Users?startIndex=%d&count=1", start), nil))

			if len(page.Resources) != 1 {
				t.Fatalf("startIndex=%d returned %d resources, want 1", start, len(page.Resources))
			}
			if got := page.Resources[0].ID; got != wantID {
				t.Errorf("startIndex=%d gave user %s, want %s", start, got, wantID)
			}
		}
	})

	t.Run("count caps the page", func(t *testing.T) {
		page := decodeBody[ListResponse](t, do(t, http.MethodGet, "/Users?count=2", nil))

		if len(page.Resources) != 2 {
			t.Fatalf("got %d resources, want 2", len(page.Resources))
		}
		if page.ItemsPerPage != 2 {
			t.Errorf("itemsPerPage = %d, want 2", page.ItemsPerPage)
		}
	})

	t.Run("startIndex below 1 floors to 1", func(t *testing.T) {
		page := decodeBody[ListResponse](t, do(t, http.MethodGet, "/Users?startIndex=0&count=1", nil))

		if page.StartIndex != 1 {
			t.Errorf("startIndex = %d, want 1", page.StartIndex)
		}
		want := firstUserIDs(t, 1)
		if len(page.Resources) != 1 || page.Resources[0].ID != want[0] {
			t.Errorf("startIndex=0 did not return the first user (%s)", want[0])
		}
	})

	t.Run("count=0 returns no resources but keeps the total", func(t *testing.T) {
		page := decodeBody[ListResponse](t, do(t, http.MethodGet, "/Users?count=0", nil))

		if len(page.Resources) != 0 {
			t.Errorf("got %d resources, want 0", len(page.Resources))
		}
		if page.TotalResults < created {
			t.Errorf("totalResults = %d, want at least %d", page.TotalResults, created)
		}
	})

}

func TestReplaceUser(t *testing.T) {
	requireDB(t)

	t.Run("replaces every attribute", func(t *testing.T) {
		created := createUser(t, newUser())

		in := newUser()
		in.Name = &Name{GivenName: "Barb"} // familyName omitted, so it must clear
		in.Emails = nil

		rr := do(t, http.MethodPut, "/Users/"+created.ID, in)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		out := decodeBody[User](t, rr)
		if out.ID != created.ID {
			t.Errorf("id changed: %q -> %q", created.ID, out.ID)
		}
		if out.UserName != in.UserName {
			t.Errorf("userName = %q, want %q", out.UserName, in.UserName)
		}
		if out.Name == nil || out.Name.GivenName != "Barb" || out.Name.FamilyName != "" {
			t.Errorf("name = %+v, want givenName Barb and no familyName", out.Name)
		}
		if len(out.Emails) != 0 {
			t.Errorf("emails = %+v, want none after a full replace", out.Emails)
		}
	})

	// A full replace that omits active restores the default, so PUT can undo a
	// soft delete. Consequential enough to pin down.
	t.Run("omitting active reactivates a deleted user", func(t *testing.T) {
		created := createUser(t, newUser())

		if rr := do(t, http.MethodDelete, "/Users/"+created.ID, nil); rr.Code != http.StatusNoContent {
			t.Fatalf("DELETE = %d, want 204", rr.Code)
		}

		in := newUser()
		rr := do(t, http.MethodPut, "/Users/"+created.ID, in)
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT = %d, want 200: %s", rr.Code, rr.Body)
		}
		if !active(t, decodeBody[User](t, rr)) {
			t.Error("active = false, want true after a replace that omits it")
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		rr := do(t, http.MethodPut, "/Users/"+nonexistentID, newUser())

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
		}
	})

	t.Run("taking another user's name is 409", func(t *testing.T) {
		taken := createUser(t, newUser())
		mover := createUser(t, newUser())

		in := newUser()
		in.UserName = taken.UserName

		rr := do(t, http.MethodPut, "/Users/"+mover.ID, in)
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[Error](t, rr).ScimType; got != "uniqueness" {
			t.Errorf("scimType = %q, want uniqueness", got)
		}
	})
}

func TestDeleteUser(t *testing.T) {
	requireDB(t)

	// DELETE is a soft delete: 204, but the resource stays fetchable, inactive.
	t.Run("204 and the user survives as inactive", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := do(t, http.MethodDelete, "/Users/"+created.ID, nil)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", rr.Code, rr.Body)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rr.Body)
		}

		rr = do(t, http.MethodGet, "/Users/"+created.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET after delete = %d, want 200 — the row must survive: %s", rr.Code, rr.Body)
		}

		got := decodeBody[User](t, rr)
		if active(t, got) {
			t.Error("active = true, want false")
		}
		if got.UserName != created.UserName {
			t.Errorf("userName = %q, want %q", got.UserName, created.UserName)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		rr := do(t, http.MethodDelete, "/Users/"+nonexistentID, nil)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
		}
	})
}

// net/http's own 404/405 bodies are plain text, which a SCIM client can't parse.
func TestUnroutedRequests(t *testing.T) {
	requireDB(t)

	for _, tc := range []struct {
		name, method, target string
		want                 int
	}{
		{"PATCH on the Users collection", http.MethodPatch, "/Users", http.StatusMethodNotAllowed},
		{"DELETE on the Users collection", http.MethodDelete, "/Users", http.StatusMethodNotAllowed},
		{"PATCH on the Groups collection", http.MethodPatch, "/Groups", http.StatusMethodNotAllowed},
		{"DELETE on the Groups collection", http.MethodDelete, "/Groups", http.StatusMethodNotAllowed},
		{"unknown resource", http.MethodGet, "/Devices", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, tc.method, tc.target, nil)

			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body)
			}
			if got := rr.Header().Get("Content-Type"); got != contentType {
				t.Errorf("Content-Type = %q, want %q", got, contentType)
			}

			scimErr := decodeBody[Error](t, rr)
			if scimErr.Status != strconv.Itoa(tc.want) {
				t.Errorf("status = %q, want %q", scimErr.Status, strconv.Itoa(tc.want))
			}
		})
	}
}
