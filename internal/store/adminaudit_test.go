package store

import (
	"context"
	"testing"
)

// A tenant's privileged-action history is invisible to another tenant's
// review, the same isolation the users table itself holds.
func TestListAdminAuditEntriesScopedByTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	if _, _, err := s.IssueToken(ctx, tenantA, "a", "test-suite", nil); err != nil {
		t.Fatalf("IssueToken for tenant A: %v", err)
	}

	entries, err := s.ListAdminAuditEntries(ctx, tenantB, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}

	// Only tenant B's own creation, never tenant A's token issue.
	if len(entries) != 1 || entries[0].Action != AdminActionTenantCreate || entries[0].TenantID != tenantB {
		t.Errorf("tenant B's entries = %+v, want exactly its own tenant.create", entries)
	}
}

// Omitting tenantID is the operator view: everything, across every tenant.
func TestListAdminAuditEntriesUnscopedSeesEveryTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	all, err := s.ListAdminAuditEntries(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range all {
		seen[e.TenantID] = true
	}
	if !seen[tenantA] || !seen[tenantB] {
		t.Errorf("unscoped list is missing at least one of the two tenants just created: seen=%v", seen)
	}
}
