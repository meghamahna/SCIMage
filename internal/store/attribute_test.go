package store

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterAttribute(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("registers and lists", func(t *testing.T) {
		a, err := s.RegisterAttribute(ctx, tenantID, "displayName", "string", "op")
		if err != nil {
			t.Fatalf("RegisterAttribute: %v", err)
		}
		if a.Name != "displayName" || a.Type != "string" {
			t.Errorf("got %+v, want displayName/string", a)
		}

		attrs, err := s.ListAttributes(ctx, tenantID)
		if err != nil {
			t.Fatalf("ListAttributes: %v", err)
		}
		if len(attrs) != 1 || attrs[0].Name != "displayName" {
			t.Errorf("listed %+v, want just displayName", attrs)
		}
	})

	t.Run("empty type defaults to string", func(t *testing.T) {
		a, err := s.RegisterAttribute(ctx, tenantID, "customField", "", "op")
		if err != nil {
			t.Fatalf("RegisterAttribute: %v", err)
		}
		if a.Type != "string" {
			t.Errorf("type = %q, want the string default", a.Type)
		}
	})

	t.Run("a duplicate is rejected", func(t *testing.T) {
		if _, err := s.RegisterAttribute(ctx, tenantID, "dup", "string", "op"); err != nil {
			t.Fatalf("first register: %v", err)
		}
		if _, err := s.RegisterAttribute(ctx, tenantID, "dup", "string", "op"); !errors.Is(err, ErrDuplicateAttribute) {
			t.Fatalf("error = %v, want ErrDuplicateAttribute", err)
		}
	})

	// A registered value would shadow a typed core attribute on read, so the
	// core names are refused — case-insensitively, since SCIM names are.
	t.Run("a reserved core name is rejected", func(t *testing.T) {
		for _, name := range []string{"userName", "USERNAME", "id", "emails", "active", "schemas", "meta", "password"} {
			if _, err := s.RegisterAttribute(ctx, tenantID, name, "string", "op"); !errors.Is(err, ErrReservedAttribute) {
				t.Errorf("RegisterAttribute(%q) error = %v, want ErrReservedAttribute", name, err)
			}
		}
	})

	t.Run("registration writes an admin-audit entry", func(t *testing.T) {
		if _, err := s.RegisterAttribute(ctx, tenantID, "auditedAttr", "string", "op"); err != nil {
			t.Fatalf("RegisterAttribute: %v", err)
		}
		entries, err := s.ListAdminAuditEntries(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("ListAdminAuditEntries: %v", err)
		}
		if len(entries) == 0 || entries[0].Action != AdminActionAttributeRegister || entries[0].TargetID != "auditedAttr" {
			t.Errorf("latest admin-audit entry = %+v, want register of auditedAttr", entries)
		}
	})
}

func TestUnregisterAttribute(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	if _, err := s.RegisterAttribute(ctx, tenantID, "title", "string", "op"); err != nil {
		t.Fatalf("RegisterAttribute: %v", err)
	}

	t.Run("removes it and audits the removal", func(t *testing.T) {
		if err := s.UnregisterAttribute(ctx, tenantID, "title", "op"); err != nil {
			t.Fatalf("UnregisterAttribute: %v", err)
		}

		attrs, err := s.ListAttributes(ctx, tenantID)
		if err != nil {
			t.Fatalf("ListAttributes: %v", err)
		}
		if len(attrs) != 0 {
			t.Errorf("still registered after unregister: %+v", attrs)
		}

		entries, err := s.ListAdminAuditEntries(ctx, tenantID, 1)
		if err != nil {
			t.Fatalf("ListAdminAuditEntries: %v", err)
		}
		if len(entries) == 0 || entries[0].Action != AdminActionAttributeUnregister {
			t.Errorf("latest admin-audit entry = %+v, want an unregister", entries)
		}
	})

	t.Run("unregistering a missing name is ErrNotFound", func(t *testing.T) {
		if err := s.UnregisterAttribute(ctx, tenantID, "neverRegistered", "op"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
}

// The registry is per-tenant: one customer's declared attributes are invisible
// to another's, the same isolation every other tenant-scoped table holds.
func TestAttributesScopedByTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	if _, err := s.RegisterAttribute(ctx, tenantA, "onlyForA", "string", "op"); err != nil {
		t.Fatalf("RegisterAttribute: %v", err)
	}

	attrs, err := s.ListAttributes(ctx, tenantB)
	if err != nil {
		t.Fatalf("ListAttributes: %v", err)
	}
	if len(attrs) != 0 {
		t.Errorf("tenant B sees tenant A's attributes: %+v", attrs)
	}

	// The same name is registerable independently in the other tenant.
	if _, err := s.RegisterAttribute(ctx, tenantB, "onlyForA", "string", "op"); err != nil {
		t.Fatalf("registering the same name in another tenant: %v, want success", err)
	}
}
