package store

// Integration tests against the real compose Postgres, never a mock. Run with
// `make test`, which loads .env; they skip when no database is configured.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const nonexistentID = "00000000-0000-4000-8000-000000000000"

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dsn, err := DSNFromEnv()
	if err != nil {
		t.Skipf("no database configured — run `make test` (%v)", err)
	}

	s, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

var tenantSeq atomic.Int64

// newTestTenant creates a throwaway tenant and deletes everything under it
// (its users, audit entries, deliveries, tokens and admin-audit entries, then
// the tenant row itself) when the test ends. Giving every test its own
// tenant is what makes the suite safe to run without -p 1: two tests' rows
// can never collide, because every store query is scoped to one tenant.
func newTestTenant(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()

	name := fmt.Sprintf("test-tenant-%d-%d", time.Now().UnixNano(), tenantSeq.Add(1))
	tenant, err := s.CreateTenant(ctx, name, "test-suite")
	if err != nil {
		t.Fatalf("CreateTenant(%q): %v", name, err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM webhook_deliveries WHERE tenant_id = $1`,
			`DELETE FROM audit_log WHERE tenant_id = $1`,
			`DELETE FROM admin_audit_log WHERE tenant_id = $1`,
			`DELETE FROM scim_tokens WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
			`DELETE FROM tenants WHERE id = $1`,
		} {
			if _, err := s.pool.Exec(ctx, q, tenant.ID); err != nil {
				t.Errorf("cleanup tenant %s: %v", tenant.ID, err)
			}
		}
	})

	return tenant.ID
}

var userNameSeq atomic.Int64

func uniqueUserName() string {
	return fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), userNameSeq.Add(1))
}

// testAudit is the actor every store test writes entries as, for the given
// test's own tenant.
func testAudit(tenantID string) AuditRecord {
	return AuditRecord{TenantID: tenantID, ActorToken: "tok_storetest", ActorIP: "127.0.0.1"}
}

func createUser(t *testing.T, s *Store, tenantID string, u *User) *User {
	t.Helper()

	created, err := s.CreateUser(context.Background(), tenantID, u, testAudit(tenantID))
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", u.UserName, err)
	}
	return created
}

// One statement, so every row shares a created_at — the case the (created_at,
// id) tiebreak exists for. Ids go in out of sort order, so a query missing the
// tiebreak comes back differently.
func createUsersSharingTimestamp(t *testing.T, s *Store, tenantID string, ids []string) {
	t.Helper()

	values := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids)*3)
	for i, id := range ids {
		values = append(values, fmt.Sprintf("($%d, $%d, $%d)", i*3+1, i*3+2, i*3+3))
		args = append(args, id, tenantID, uniqueUserName())
	}

	q := `INSERT INTO users (id, tenant_id, user_name) VALUES ` + strings.Join(values, ", ")
	if _, err := s.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("insert users sharing a timestamp: %v", err)
	}
}

// Pages the whole table, so assertions don't depend on where a row lands.
func allUsers(t *testing.T, s *Store, tenantID string) ([]User, int) {
	t.Helper()

	var all []User
	total := 0

	for offset := 0; ; offset += 50 {
		page, pageTotal, err := s.ListUsers(context.Background(), tenantID, 50, offset, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers(50, %d): %v", offset, err)
		}
		total = pageTotal
		if len(page) == 0 {
			return all, total
		}
		all = append(all, page...)
	}
}

func ptr(s string) *string { return &s }

func TestCreateUser(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)

	t.Run("returns the stored row", func(t *testing.T) {
		name := uniqueUserName()
		created := createUser(t, s, tenantID, &User{
			UserName:   name,
			GivenName:  ptr("Barbara"),
			FamilyName: ptr("Jensen"),
			Email:      ptr("bjensen@example.com"),
			Active:     true,
		})

		if created.ID == "" {
			t.Error("ID is empty — the database should have assigned one")
		}
		if created.UserName != name {
			t.Errorf("UserName = %q, want %q", created.UserName, name)
		}
		if created.GivenName == nil || *created.GivenName != "Barbara" {
			t.Errorf("GivenName = %v, want Barbara", created.GivenName)
		}
		if !created.Active {
			t.Error("Active = false, want true")
		}
		if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
			t.Errorf("timestamps not set: created=%v updated=%v", created.CreatedAt, created.UpdatedAt)
		}
	})

	t.Run("keeps nullable attributes null", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		if created.GivenName != nil || created.FamilyName != nil || created.Email != nil {
			t.Errorf("expected nil optional attributes, got given=%v family=%v email=%v",
				created.GivenName, created.FamilyName, created.Email)
		}
	})

	t.Run("honours active=false", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: false})

		if created.Active {
			t.Error("Active = true, want false")
		}
	})
}

// userName is caseExact=false, so a differently-cased name is the same user and
// must conflict within a tenant. This is what POST /Users turns into a 409.
func TestCreateUserDuplicateUserName(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)

	base := uniqueUserName()
	createUser(t, s, tenantID, &User{UserName: base, Active: true})

	tests := []struct {
		name     string
		userName string
	}{
		{"exact duplicate", base},
		{"uppercased", strings.ToUpper(base)},
		{"mixed case", strings.Replace(base, "test", "TeSt", 1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateUser(context.Background(), tenantID, &User{UserName: tc.userName, Active: true}, testAudit(tenantID))
			if !errors.Is(err, ErrDuplicateUserName) {
				t.Fatalf("CreateUser(%q) error = %v, want ErrDuplicateUserName", tc.userName, err)
			}
		})
	}
}

// The same userName is not a conflict across two different tenants — that's
// the entire point of scoping uniqueness by tenant_id rather than server-wide.
func TestCreateUserSameNameDifferentTenants(t *testing.T) {
	s := newTestStore(t)
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	name := uniqueUserName()
	createUser(t, s, tenantA, &User{UserName: name, Active: true})

	if _, err := s.CreateUser(context.Background(), tenantB, &User{UserName: name, Active: true}, testAudit(tenantB)); err != nil {
		t.Fatalf("CreateUser(%q) in a different tenant: %v, want success", name, err)
	}
}

func TestGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	created := createUser(t, s, tenantID, &User{
		UserName:  uniqueUserName(),
		GivenName: ptr("Barbara"),
		Active:    true,
	})

	t.Run("returns an existing user", func(t *testing.T) {
		got, err := s.GetUser(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetUser(%q): %v", created.ID, err)
		}
		if got.UserName != created.UserName {
			t.Errorf("UserName = %q, want %q", got.UserName, created.UserName)
		}
		if got.GivenName == nil || *got.GivenName != "Barbara" {
			t.Errorf("GivenName = %v, want Barbara", got.GivenName)
		}
	})

	// A junk id is a 404, not a 500.
	for _, tc := range []struct {
		name string
		id   string
	}{
		{"unknown id", nonexistentID},
		{"malformed id", "not-a-uuid"},
		{"empty id", ""},
	} {
		t.Run(tc.name+" is ErrNotFound", func(t *testing.T) {
			if _, err := s.GetUser(ctx, tenantID, tc.id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetUser(%q) error = %v, want ErrNotFound", tc.id, err)
			}
		})
	}

	t.Run("another tenant's real id is ErrNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		if _, err := s.GetUser(ctx, otherTenant, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetUser(%q) from another tenant error = %v, want ErrNotFound", created.ID, err)
		}
	})
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	_, before, err := s.ListUsers(ctx, tenantID, 1, 0, UserFilter{})
	if err != nil {
		t.Fatalf("ListUsers baseline: %v", err)
	}
	if before != 0 {
		t.Fatalf("baseline total = %d, want 0 for a fresh tenant", before)
	}

	const created = 3
	for range created {
		createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	}

	t.Run("total reflects every row", func(t *testing.T) {
		_, total, err := s.ListUsers(ctx, tenantID, 1, 0, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if total != created {
			t.Errorf("total = %d, want %d", total, created)
		}
	})

	t.Run("limit caps the page", func(t *testing.T) {
		users, _, err := s.ListUsers(ctx, tenantID, 2, 0, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("got %d users, want 2", len(users))
		}
	})

	t.Run("paging covers every row exactly once", func(t *testing.T) {
		all, total := allUsers(t, s, tenantID)

		if len(all) != total {
			t.Errorf("paged over %d rows, but total is %d", len(all), total)
		}

		seen := make(map[string]bool, len(all))
		for _, u := range all {
			if seen[u.ID] {
				t.Errorf("user %s appeared on more than one page", u.ID)
			}
			seen[u.ID] = true
		}
	})

	t.Run("breaks created_at ties by id", func(t *testing.T) {
		ids := []string{
			"cccccccc-0000-4000-8000-000000000003",
			"aaaaaaaa-0000-4000-8000-000000000001",
			"bbbbbbbb-0000-4000-8000-000000000002",
		}
		createUsersSharingTimestamp(t, s, tenantID, ids)

		want := []string{
			"aaaaaaaa-0000-4000-8000-000000000001",
			"bbbbbbbb-0000-4000-8000-000000000002",
			"cccccccc-0000-4000-8000-000000000003",
		}

		all, _ := allUsers(t, s, tenantID)
		var got []string
		for _, u := range all {
			for _, id := range want {
				if u.ID == id {
					got = append(got, u.ID)
				}
			}
		}

		if len(got) != len(want) {
			t.Fatalf("found %d of the %d inserted rows: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("tied rows came back as %v, want %v", got, want)
			}
		}
	})

	// The point of the soft delete: the row survives, so the listing keeps
	// showing it and totalResults keeps counting it.
	t.Run("includes deactivated users", func(t *testing.T) {
		u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		_, totalBefore := allUsers(t, s, tenantID)

		if _, err := s.DeactivateUser(ctx, tenantID, u.ID, testAudit(tenantID)); err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}

		all, totalAfter := allUsers(t, s, tenantID)

		found := false
		for _, got := range all {
			if got.ID == u.ID {
				found = true
				if got.Active {
					t.Error("listed user is still active after deactivation")
				}
			}
		}
		if !found {
			t.Errorf("deactivated user %s disappeared from the listing", u.ID)
		}
		if totalAfter != totalBefore {
			t.Errorf("total changed after deactivation: %d -> %d", totalBefore, totalAfter)
		}
	})

	t.Run("offset past the end keeps the total", func(t *testing.T) {
		_, total, err := s.ListUsers(ctx, tenantID, 1, 0, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}

		users, total2, err := s.ListUsers(ctx, tenantID, 10, total+10, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != 0 {
			t.Errorf("got %d users, want 0", len(users))
		}
		if total2 != total {
			t.Errorf("total = %d, want %d", total2, total)
		}
	})

	t.Run("rejects a negative limit", func(t *testing.T) {
		if _, _, err := s.ListUsers(ctx, tenantID, -1, 0, UserFilter{}); err == nil {
			t.Error("expected an error for a negative limit, got nil")
		}
	})

	t.Run("clamps limit to MaxPageSize", func(t *testing.T) {
		capTenant := newTestTenant(t, s)

		ids := make([]string, MaxPageSize+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		}
		createUsersSharingTimestamp(t, s, capTenant, ids)

		users, _, err := s.ListUsers(ctx, capTenant, MaxPageSize*100, 0, UserFilter{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != MaxPageSize {
			t.Errorf("got %d users for an oversized limit, want the %d cap", len(users), MaxPageSize)
		}
	})
}

func TestUpdateUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("replaces every settable attribute", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{
			UserName:   uniqueUserName(),
			GivenName:  ptr("Barbara"),
			FamilyName: ptr("Jensen"),
			Email:      ptr("bjensen@example.com"),
			Active:     true,
		})

		newName := uniqueUserName()
		changed, err := s.UpdateUser(ctx, tenantID, created.ID, &User{
			UserName:   newName,
			GivenName:  ptr("Barb"),
			FamilyName: nil, // a full replace clears omitted attributes
			Email:      ptr("barb@example.com"),
			Active:     false,
		}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

		// The before-image is what the audit log records as the prior state, so
		// it has to be the row as it stood before this statement, not after.
		if changed.Before == nil || changed.Before.UserName != created.UserName {
			t.Errorf("Before.UserName = %v, want %q", changed.Before, created.UserName)
		}
		if changed.Before != nil && !changed.Before.Active {
			t.Error("Before.Active = false, want the pre-update value true")
		}

		updated := changed.After
		if updated.ID != created.ID {
			t.Errorf("ID changed: %q -> %q", created.ID, updated.ID)
		}
		if updated.UserName != newName {
			t.Errorf("UserName = %q, want %q", updated.UserName, newName)
		}
		if updated.FamilyName != nil {
			t.Errorf("FamilyName = %v, want nil after a full replace", *updated.FamilyName)
		}
		if updated.Active {
			t.Error("Active = true, want false")
		}
		if !updated.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("CreatedAt changed: %v -> %v", created.CreatedAt, updated.CreatedAt)
		}
		// Must advance, not merely "not go backwards" — the latter holds even if
		// the UPDATE never touches updated_at.
		if !updated.UpdatedAt.After(created.UpdatedAt) {
			t.Errorf("UpdatedAt not advanced: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
		}
	})

	t.Run("unknown id is ErrNotFound", func(t *testing.T) {
		_, err := s.UpdateUser(ctx, tenantID, nonexistentID, &User{UserName: uniqueUserName(), Active: true}, testAudit(tenantID))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("taking another user's name is a conflict", func(t *testing.T) {
		taken := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		mover := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		_, err := s.UpdateUser(ctx, tenantID, mover.ID, &User{UserName: taken.UserName, Active: true}, testAudit(tenantID))
		if !errors.Is(err, ErrDuplicateUserName) {
			t.Fatalf("error = %v, want ErrDuplicateUserName", err)
		}
	})

	t.Run("another tenant's real id is ErrNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		_, err := s.UpdateUser(ctx, otherTenant, created.ID, &User{UserName: uniqueUserName(), Active: true}, testAudit(otherTenant))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
}

func TestDeactivateUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("clears active but keeps the row", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		changed, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID))
		if err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}
		if changed.After.Active {
			t.Error("After.Active = true, want false")
		}
		if changed.Before == nil || !changed.Before.Active {
			t.Error("Before.Active = false, want the pre-delete value true")
		}

		got, err := s.GetUser(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetUser after deactivate: %v", err)
		}
		if got.Active {
			t.Error("user is still active after deactivation")
		}
		if got.UserName != created.UserName {
			t.Errorf("UserName = %q, want %q", got.UserName, created.UserName)
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		if _, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
			t.Fatalf("first DeactivateUser: %v", err)
		}
		if _, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
			t.Fatalf("second DeactivateUser: %v", err)
		}
	})

	t.Run("unknown id is ErrNotFound", func(t *testing.T) {
		if _, err := s.DeactivateUser(ctx, tenantID, nonexistentID, testAudit(tenantID)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("another tenant's real id is ErrNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		if _, err := s.DeactivateUser(ctx, otherTenant, created.ID, testAudit(otherTenant)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
}
