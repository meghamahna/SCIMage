package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// cleanupTenant removes a tenant and its admin-audit trail, for tests that
// need the created *Tenant back (newTestTenant only returns the id).
func cleanupTenant(t *testing.T, s *Store, id string) {
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := s.pool.Exec(ctx, `DELETE FROM admin_audit_log WHERE tenant_id = $1`, id); err != nil {
			t.Errorf("cleanup admin audit for %s: %v", id, err)
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup tenant %s: %v", id, err)
		}
	})
}

func TestCreateTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("test-tenant-create-%d", tenantSeq.Add(1))
	tenant, err := s.CreateTenant(ctx, name, "op-alice")
	if err != nil {
		t.Fatalf("CreateTenant(%q): %v", name, err)
	}
	cleanupTenant(t, s, tenant.ID)

	if tenant.ID == "" || !strings.HasPrefix(tenant.ID, "tenant_") {
		t.Errorf("ID = %q, want a tenant_ prefixed value", tenant.ID)
	}
	if tenant.Name != name {
		t.Errorf("Name = %q, want %q", tenant.Name, name)
	}
	if tenant.CreatedBy != "op-alice" {
		t.Errorf("CreatedBy = %q, want %q", tenant.CreatedBy, "op-alice")
	}
	if tenant.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

// Two customers silently sharing a display name, even by casing alone, is
// the exact ambiguity this index exists to rule out.
func TestCreateTenantDuplicateName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := fmt.Sprintf("Acme Corp %d", tenantSeq.Add(1))
	first, err := s.CreateTenant(ctx, base, "op-alice")
	if err != nil {
		t.Fatalf("CreateTenant(%q): %v", base, err)
	}
	cleanupTenant(t, s, first.ID)

	for _, tc := range []struct {
		name string
		try  string
	}{
		{"exact duplicate", base},
		{"uppercased", strings.ToUpper(base)},
		{"mixed case", strings.Replace(base, "Acme", "aCmE", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateTenant(ctx, tc.try, "op-bob"); !errors.Is(err, ErrDuplicateTenantName) {
				t.Fatalf("CreateTenant(%q) error = %v, want ErrDuplicateTenantName", tc.try, err)
			}
		})
	}
}

func TestGetTenantUnknown(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetTenant(context.Background(), "tenant_does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTenant error = %v, want ErrNotFound", err)
	}
}

// CreateTenant is the first privileged action a tenant's history has, so its
// own admin-audit entry has to exist from the start, not just for later
// token issue/revoke actions.
func TestCreateTenantWritesAdminAudit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	entries, err := s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d admin audit entries, want 1", len(entries))
	}

	got := entries[0]
	if got.Action != AdminActionTenantCreate {
		t.Errorf("Action = %q, want %q", got.Action, AdminActionTenantCreate)
	}
	if got.TargetID != tenantID {
		t.Errorf("TargetID = %q, want %q", got.TargetID, tenantID)
	}
	if got.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenantID)
	}
	if got.Actor != "test-suite" {
		t.Errorf("Actor = %q, want %q", got.Actor, "test-suite")
	}
}
