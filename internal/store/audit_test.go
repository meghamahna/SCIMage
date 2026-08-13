package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// decodeAuditUser unmarshals a User audit image, failing the test if it's
// missing or malformed — callers assert on it, so a nil image here means the
// wrong thing was asked for.
func decodeAuditUser(t *testing.T, raw json.RawMessage) User {
	t.Helper()

	if raw == nil {
		t.Fatal("image is nil")
	}
	var u User
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatalf("unmarshal audited user: %v", err)
	}
	return u
}

func decodeAuditGroup(t *testing.T, raw json.RawMessage) Group {
	t.Helper()

	if raw == nil {
		t.Fatal("image is nil")
	}
	var g Group
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("unmarshal audited group: %v", err)
	}
	return g
}

func countAudit(t *testing.T, s *Store, tenantID string) int {
	t.Helper()

	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count audit entries: %v", err)
	}
	return n
}

// newFaultInjectingStore builds a *Store backed by a dedicated, single
// connection pool, so a session-level setting on it can't leak onto — or be
// disturbed by — any other test's connection. MaxConns: 1 guarantees every
// Begin() a mutation opens against this pool lands on that one connection,
// which is what lets a plain session-scoped SET (not SET LOCAL) reach a
// mutation's own internally-opened transaction: the caller can't wrap it in
// an outer transaction of its own, since Postgres has no nested transactions
// and the store methods don't accept an external pgx.Tx.
//
// Closing the pool at test end tears down that one connection outright, so
// the setting never has to be reset — nothing else was ever able to see it.
func newFaultInjectingStore(t *testing.T) *Store {
	t.Helper()

	dsn, err := DSNFromEnv()
	if err != nil {
		t.Skipf("no database configured — run `make test` (%v)", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open single-connection pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return &Store{pool: pool}
}

// This is the guarantee the whole design turns on: a mutation and its audit
// entry share one transaction, so a user cannot be changed without a record.
// audit_log_fault_injection (migrations/000009) raises on insert while this
// session's scimage.simulate_audit_failure flag is set, forcing the entry
// insert to fail so the mutation has to roll back rather than commit
// unrecorded — with no footprint outside this test's own connection.
func TestMutationRollsBackWhenAuditFails(t *testing.T) {
	s := newFaultInjectingStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	if _, err := s.pool.Exec(ctx, `SET scimage.simulate_audit_failure = 'true'`); err != nil {
		t.Fatalf("enable audit fault injection: %v", err)
	}

	t.Run("create rolls back", func(t *testing.T) {
		name := uniqueUserName()

		if _, err := s.CreateUser(ctx, tenantID, &User{UserName: name, Active: true}, testAudit(tenantID)); err == nil {
			t.Fatal("CreateUser succeeded despite the injected audit failure — the mutation was not rolled back")
		}

		var n int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM users WHERE lower(user_name) = lower($1)`, name).Scan(&n); err != nil {
			t.Fatalf("count users: %v", err)
		}
		if n != 0 {
			t.Errorf("found %d rows for %q — the create committed without an audit entry", n, name)
		}
	})
}

func TestAuditEntryWrittenWithMutation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("create writes exactly one entry", func(t *testing.T) {
		before := countAudit(t, s, tenantID)

		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		if after := countAudit(t, s, tenantID); after != before+1 {
			t.Fatalf("audit rows went %d -> %d, want one more", before, after)
		}

		entries, err := s.ListAuditEntries(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("ListAuditEntries: %v", err)
		}
		got := entries[0]

		if got.Action != ActionCreate || got.Result != ResultSuccess {
			t.Errorf("action/result = %q/%q, want create/success", got.Action, got.Result)
		}
		if got.TargetID != created.ID {
			t.Errorf("targetId = %q, want %q", got.TargetID, created.ID)
		}
		if got.Actor.ActorToken != testAudit(tenantID).ActorToken {
			t.Errorf("actor = %q, want %q", got.Actor.ActorToken, testAudit(tenantID).ActorToken)
		}
		if got.Actor.TenantID != tenantID {
			t.Errorf("tenant = %q, want %q", got.Actor.TenantID, tenantID)
		}
		if got.ResourceType != ResourceUser {
			t.Errorf("resourceType = %q, want %q", got.ResourceType, ResourceUser)
		}
		if got.Before != nil {
			t.Errorf("before = %s, want nil on a create", got.Before)
		}
		if after := decodeAuditUser(t, got.After); after.UserName != created.UserName {
			t.Errorf("after.userName = %q, want %q", after.UserName, created.UserName)
		}
	})

	// before/after survive the jsonb round trip, including the nullable columns.
	t.Run("replace round-trips both images", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{
			UserName:   uniqueUserName(),
			GivenName:  ptr("Barbara"),
			FamilyName: ptr("Jensen"),
			Active:     true,
		})

		if _, err := s.UpdateUser(ctx, tenantID, created.ID, &User{
			UserName: uniqueUserName(),
			Active:   false,
		}, testAudit(tenantID)); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}

		entries, err := s.ListAuditEntries(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("ListAuditEntries: %v", err)
		}
		got := entries[0]

		if got.Before == nil || got.After == nil {
			t.Fatalf("before/after = %s/%s, want both", got.Before, got.After)
		}
		before := decodeAuditUser(t, got.Before)
		after := decodeAuditUser(t, got.After)

		if before.GivenName == nil || *before.GivenName != "Barbara" {
			t.Errorf("before.givenName = %v, want Barbara", before.GivenName)
		}
		if after.GivenName != nil {
			t.Errorf("after.givenName = %v, want nil after a full replace", *after.GivenName)
		}
		if !before.Active || after.Active {
			t.Errorf("active went %v -> %v, want true -> false", before.Active, after.Active)
		}
	})

	t.Run("a refusal is recorded with no images", func(t *testing.T) {
		existing := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		before := countAudit(t, s, tenantID)

		if _, err := s.CreateUser(ctx, tenantID, &User{UserName: existing.UserName, Active: true}, testAudit(tenantID)); !errors.Is(err, ErrDuplicateUserName) {
			t.Fatalf("error = %v, want ErrDuplicateUserName", err)
		}

		if after := countAudit(t, s, tenantID); after != before+1 {
			t.Fatalf("audit rows went %d -> %d, want one more for the refusal", before, after)
		}

		entries, err := s.ListAuditEntries(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("ListAuditEntries: %v", err)
		}
		got := entries[0]

		if got.Result != ResultDenied {
			t.Errorf("result = %q, want %q", got.Result, ResultDenied)
		}
		if got.Detail == "" {
			t.Error("detail is empty — a refusal should say why")
		}
		if got.Before != nil || got.After != nil {
			t.Errorf("before/after = %+v/%+v, want neither on a refusal", got.Before, got.After)
		}
	})
}

// Groups share audit_log with users rather than a table of their own, so a
// mutation's resource_type is what tells a reviewer (or SCIMTrace AI) which
// struct the before/after images decode as.
func TestGroupAuditEntryWrittenWithMutation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("create records resourceType group with no before-image", func(t *testing.T) {
		created, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName()}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("CreateGroup: %v", err)
		}

		got := entriesFor(t, s, tenantID)[0]
		if got.ResourceType != ResourceGroup || got.Action != ActionCreate {
			t.Errorf("resourceType/action = %q/%q, want group/create", got.ResourceType, got.Action)
		}
		if got.TargetID != created.ID {
			t.Errorf("targetId = %q, want %q", got.TargetID, created.ID)
		}
		if got.Before != nil {
			t.Errorf("before = %s, want nil on a create", got.Before)
		}
		if after := decodeAuditGroup(t, got.After); after.DisplayName != created.DisplayName {
			t.Errorf("after.displayName = %q, want %q", after.DisplayName, created.DisplayName)
		}
	})

	t.Run("delete is action delete, not deactivate, with a before-image", func(t *testing.T) {
		created, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName()}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("CreateGroup: %v", err)
		}

		if _, err := s.DeleteGroup(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
			t.Fatalf("DeleteGroup: %v", err)
		}

		got := entriesFor(t, s, tenantID)[0]
		if got.Action != ActionDelete {
			t.Errorf("action = %q, want %q", got.Action, ActionDelete)
		}
		if got.After != nil {
			t.Errorf("after = %s, want nil on a delete", got.After)
		}
		if before := decodeAuditGroup(t, got.Before); before.ID != created.ID {
			t.Errorf("before.id = %q, want %q", before.ID, created.ID)
		}
	})

	t.Run("a duplicate displayName refusal names the group action", func(t *testing.T) {
		existing, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName()}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("CreateGroup: %v", err)
		}

		if _, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: existing.DisplayName}, testAudit(tenantID)); !errors.Is(err, ErrDuplicateGroupName) {
			t.Fatalf("error = %v, want ErrDuplicateGroupName", err)
		}

		got := entriesFor(t, s, tenantID)[0]
		if got.ResourceType != ResourceGroup || got.Result != ResultDenied {
			t.Errorf("resourceType/result = %q/%q, want group/denied", got.ResourceType, got.Result)
		}
	})
}

func entriesFor(t *testing.T, s *Store, tenantID string) []AuditEntry {
	t.Helper()

	entries, err := s.ListAuditEntries(context.Background(), tenantID, 1)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no audit entry was written")
	}
	return entries
}

// A tenant's audit history is invisible to another tenant's review, the same
// isolation the users table itself holds.
func TestListAuditEntriesScopedByTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	createUser(t, s, tenantA, &User{UserName: uniqueUserName(), Active: true})

	entries, err := s.ListAuditEntries(ctx, tenantB, MaxPageSize)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("tenant B sees %d of tenant A's audit entries, want 0", len(entries))
	}
}
