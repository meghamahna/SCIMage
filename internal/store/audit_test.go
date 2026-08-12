package store

import (
	"context"
	"errors"
	"testing"
)

func countAudit(t *testing.T, s *Store, tenantID string) int {
	t.Helper()

	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count audit entries: %v", err)
	}
	return n
}

// This is the guarantee the whole design turns on: a mutation and its audit
// entry share one transaction, so a user cannot be changed without a record.
// Renaming audit_log away makes the entry insert fail; the mutation has to roll
// back rather than commit unrecorded.
func TestMutationRollsBackWhenAuditFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	if _, err := s.pool.Exec(ctx, `ALTER TABLE audit_log RENAME TO audit_log_hidden`); err != nil {
		t.Fatalf("hide audit_log: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `ALTER TABLE audit_log_hidden RENAME TO audit_log`); err != nil {
			t.Fatalf("restore audit_log: %v", err)
		}
	})

	t.Run("create rolls back", func(t *testing.T) {
		name := uniqueUserName()

		if _, err := s.CreateUser(ctx, tenantID, &User{UserName: name, Active: true}, testAudit(tenantID)); err == nil {
			t.Fatal("CreateUser succeeded with no audit table — the mutation was not rolled back")
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
		if got.Before != nil {
			t.Errorf("before = %+v, want nil on a create", got.Before)
		}
		if got.After == nil || got.After.UserName != created.UserName {
			t.Errorf("after = %+v, want the created user", got.After)
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
			t.Fatalf("before/after = %+v/%+v, want both", got.Before, got.After)
		}
		if got.Before.GivenName == nil || *got.Before.GivenName != "Barbara" {
			t.Errorf("before.givenName = %v, want Barbara", got.Before.GivenName)
		}
		if got.After.GivenName != nil {
			t.Errorf("after.givenName = %v, want nil after a full replace", *got.After.GivenName)
		}
		if !got.Before.Active || got.After.Active {
			t.Errorf("active went %v -> %v, want true -> false", got.Before.Active, got.After.Active)
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
