package store

// Integration tests against the real compose Postgres, never a mock. Run with
// `make test`, which loads .env; they skip when no database is configured.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const nonexistentID = "00000000-0000-4000-8000-000000000000"

func dsn(t *testing.T) string {
	t.Helper()

	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}

	user, pass, name := os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_DB")
	if user == "" || pass == "" || name == "" {
		t.Skip("no database configured — run `make test`")
	}

	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}

	// url.UserPassword percent-encodes, so a password with reserved characters
	// can't corrupt the DSN.
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     net.JoinHostPort("localhost", port),
		Path:     name,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	s, err := New(context.Background(), dsn(t))
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

var userNameSeq atomic.Int64

func uniqueUserName() string {
	return fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), userNameSeq.Add(1))
}

// The store only soft-deletes, so tests need a hard delete to leave the shared
// database as they found it. A failed cleanup would skew the count assertions.
func (s *Store) hardDelete(ctx context.Context, t *testing.T, id string) {
	t.Helper()

	if _, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		t.Errorf("cleanup: delete user %q: %v", id, err)
	}
}

func createUser(t *testing.T, s *Store, u *User) *User {
	t.Helper()

	created, err := s.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", u.UserName, err)
	}
	t.Cleanup(func() { s.hardDelete(context.Background(), t, created.ID) })
	return created
}

// One statement, so every row shares a created_at — the case the (created_at,
// id) tiebreak exists for. Ids go in out of sort order, so a query missing the
// tiebreak comes back differently.
func createUsersSharingTimestamp(t *testing.T, s *Store, ids []string) {
	t.Helper()

	values := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		values = append(values, fmt.Sprintf("($%d, $%d)", i*2+1, i*2+2))
		args = append(args, id, uniqueUserName())
	}

	q := `INSERT INTO users (id, user_name) VALUES ` + strings.Join(values, ", ")
	if _, err := s.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("insert users sharing a timestamp: %v", err)
	}

	for _, id := range ids {
		t.Cleanup(func() { s.hardDelete(context.Background(), t, id) })
	}
}

// Pages the whole table, so assertions don't depend on where a row lands.
func allUsers(t *testing.T, s *Store) ([]User, int) {
	t.Helper()

	var all []User
	total := 0

	for offset := 0; ; offset += 50 {
		page, pageTotal, err := s.ListUsers(context.Background(), 50, offset)
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

	t.Run("returns the stored row", func(t *testing.T) {
		name := uniqueUserName()
		created := createUser(t, s, &User{
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
		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})

		if created.GivenName != nil || created.FamilyName != nil || created.Email != nil {
			t.Errorf("expected nil optional attributes, got given=%v family=%v email=%v",
				created.GivenName, created.FamilyName, created.Email)
		}
	})

	t.Run("honours active=false", func(t *testing.T) {
		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: false})

		if created.Active {
			t.Error("Active = true, want false")
		}
	})
}

// userName is caseExact=false, so a differently-cased name is the same user and
// must conflict. This is what POST /Users turns into a 409.
func TestCreateUserDuplicateUserName(t *testing.T) {
	s := newTestStore(t)

	base := uniqueUserName()
	createUser(t, s, &User{UserName: base, Active: true})

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
			got, err := s.CreateUser(context.Background(), &User{UserName: tc.userName, Active: true})
			if got != nil {
				s.hardDelete(context.Background(), t, got.ID)
			}
			if !errors.Is(err, ErrDuplicateUserName) {
				t.Fatalf("CreateUser(%q) error = %v, want ErrDuplicateUserName", tc.userName, err)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created := createUser(t, s, &User{
		UserName:  uniqueUserName(),
		GivenName: ptr("Barbara"),
		Active:    true,
	})

	t.Run("returns an existing user", func(t *testing.T) {
		got, err := s.GetUser(ctx, created.ID)
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
			if _, err := s.GetUser(ctx, tc.id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetUser(%q) error = %v, want ErrNotFound", tc.id, err)
			}
		})
	}
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Total counts every row in the shared database, so assertions are relative
	// to what was already there.
	_, before, err := s.ListUsers(ctx, 1, 0)
	if err != nil {
		t.Fatalf("ListUsers baseline: %v", err)
	}

	const created = 3
	for range created {
		createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
	}

	t.Run("total reflects every row", func(t *testing.T) {
		_, total, err := s.ListUsers(ctx, 1, 0)
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if want := before + created; total != want {
			t.Errorf("total = %d, want %d", total, want)
		}
	})

	t.Run("limit caps the page", func(t *testing.T) {
		users, _, err := s.ListUsers(ctx, 2, 0)
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("got %d users, want 2", len(users))
		}
	})

	t.Run("paging covers every row exactly once", func(t *testing.T) {
		all, total := allUsers(t, s)

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
		createUsersSharingTimestamp(t, s, ids)

		want := []string{
			"aaaaaaaa-0000-4000-8000-000000000001",
			"bbbbbbbb-0000-4000-8000-000000000002",
			"cccccccc-0000-4000-8000-000000000003",
		}

		all, _ := allUsers(t, s)
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
		u := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})

		_, totalBefore := allUsers(t, s)

		if _, err := s.DeactivateUser(ctx, u.ID); err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}

		all, totalAfter := allUsers(t, s)

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
		users, total, err := s.ListUsers(ctx, 10, before+created+10)
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != 0 {
			t.Errorf("got %d users, want 0", len(users))
		}
		if want := before + created; total != want {
			t.Errorf("total = %d, want %d", total, want)
		}
	})

	t.Run("rejects a negative limit", func(t *testing.T) {
		if _, _, err := s.ListUsers(ctx, -1, 0); err == nil {
			t.Error("expected an error for a negative limit, got nil")
		}
	})

	t.Run("clamps limit to MaxPageSize", func(t *testing.T) {
		ids := make([]string, MaxPageSize+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		}
		createUsersSharingTimestamp(t, s, ids)

		users, _, err := s.ListUsers(ctx, MaxPageSize*100, 0)
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

	t.Run("replaces every settable attribute", func(t *testing.T) {
		created := createUser(t, s, &User{
			UserName:   uniqueUserName(),
			GivenName:  ptr("Barbara"),
			FamilyName: ptr("Jensen"),
			Email:      ptr("bjensen@example.com"),
			Active:     true,
		})

		newName := uniqueUserName()
		updated, err := s.UpdateUser(ctx, created.ID, &User{
			UserName:   newName,
			GivenName:  ptr("Barb"),
			FamilyName: nil, // a full replace clears omitted attributes
			Email:      ptr("barb@example.com"),
			Active:     false,
		})
		if err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

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
		_, err := s.UpdateUser(ctx, nonexistentID, &User{UserName: uniqueUserName(), Active: true})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("taking another user's name is a conflict", func(t *testing.T) {
		taken := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})
		mover := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})

		_, err := s.UpdateUser(ctx, mover.ID, &User{UserName: taken.UserName, Active: true})
		if !errors.Is(err, ErrDuplicateUserName) {
			t.Fatalf("error = %v, want ErrDuplicateUserName", err)
		}
	})
}

func TestDeactivateUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	t.Run("clears active but keeps the row", func(t *testing.T) {
		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})

		deactivated, err := s.DeactivateUser(ctx, created.ID)
		if err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}
		if deactivated.Active {
			t.Error("Active = true, want false")
		}

		got, err := s.GetUser(ctx, created.ID)
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
		created := createUser(t, s, &User{UserName: uniqueUserName(), Active: true})

		if _, err := s.DeactivateUser(ctx, created.ID); err != nil {
			t.Fatalf("first DeactivateUser: %v", err)
		}
		if _, err := s.DeactivateUser(ctx, created.ID); err != nil {
			t.Fatalf("second DeactivateUser: %v", err)
		}
	})

	t.Run("unknown id is ErrNotFound", func(t *testing.T) {
		if _, err := s.DeactivateUser(ctx, nonexistentID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
}
