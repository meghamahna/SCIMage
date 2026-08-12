package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

func TestParseToken(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantKey string
		wantSec string
		wantOK  bool
	}{
		{"well formed", "scimage_abc123_def456", "abc123", "def456", true},
		{"secret with underscores", "scimage_abc123_de_f4_56", "abc123", "de_f4_56", true},
		{"missing prefix", "abc123_def456", "", "", false},
		{"no separator", "scimage_abc123", "", "", false},
		{"empty secret", "scimage_abc123_", "", "", false},
		{"empty key id", "scimage__def456", "", "", false},
		{"empty string", "", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keyID, secret, ok := ParseToken(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if keyID != tc.wantKey || secret != tc.wantSec {
				t.Errorf("got (%q, %q), want (%q, %q)", keyID, secret, tc.wantKey, tc.wantSec)
			}
		})
	}
}

func TestIssueToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	plaintext, tok, err := s.IssueToken(ctx, tenantID, "Okta prod", "test-suite", nil)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	keyID, secret, ok := ParseToken(plaintext)
	if !ok {
		t.Fatalf("issued token %q does not parse", plaintext)
	}
	if keyID != tok.KeyID {
		t.Errorf("plaintext key id = %q, want %q", keyID, tok.KeyID)
	}

	sum := sha256.Sum256([]byte(secret))
	if string(sum[:]) != string(tok.SecretHash) {
		t.Error("stored secret_hash does not match sha256 of the issued secret")
	}
	if tok.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", tok.TenantID, tenantID)
	}
	if tok.RevokedAt != nil || tok.ExpiresAt != nil || tok.LastUsedAt != nil {
		t.Errorf("a fresh token has unset revoked/expires/last_used, got %+v", tok)
	}

	got, err := s.GetTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetTokenByKeyID: %v", err)
	}
	if got.Label != "Okta prod" {
		t.Errorf("Label = %q, want %q", got.Label, "Okta prod")
	}
}

func TestGetTokenByKeyIDUnknown(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetTokenByKeyID(context.Background(), "no-such-key"); err == nil {
		t.Error("expected an error for an unknown key id, got nil")
	}
}

func TestTouchToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	_, tok, err := s.IssueToken(ctx, tenantID, "test", "test-suite", nil)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if err := s.TouchToken(ctx, tok.KeyID); err != nil {
		t.Fatalf("TouchToken: %v", err)
	}

	got, err := s.GetTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetTokenByKeyID: %v", err)
	}
	if got.LastUsedAt == nil {
		t.Error("LastUsedAt is still nil after TouchToken")
	}
}

func TestRevokeToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	_, tok, err := s.IssueToken(ctx, tenantID, "test", "test-suite", nil)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if err := s.RevokeToken(ctx, tok.KeyID, "test-suite"); err != nil {
		t.Fatalf("first RevokeToken: %v", err)
	}

	got, err := s.GetTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetTokenByKeyID: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("RevokedAt is still nil after RevokeToken")
	}

	t.Run("is idempotent", func(t *testing.T) {
		if err := s.RevokeToken(ctx, tok.KeyID, "test-suite"); err != nil {
			t.Fatalf("second RevokeToken: %v", err)
		}
	})

	t.Run("unknown key id is an error", func(t *testing.T) {
		if err := s.RevokeToken(ctx, "no-such-key", "test-suite"); err == nil {
			t.Error("expected an error revoking an unknown key id, got nil")
		}
	})
}

// IssueToken's admin-audit entry is what lets an operator answer "who issued
// this and why" without trusting the label alone.
func TestIssueTokenWritesAdminAudit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	before, err := s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries baseline: %v", err)
	}

	_, tok, err := s.IssueToken(ctx, tenantID, "Okta prod", "op-alice", nil)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	entries, err := s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	if len(entries) != len(before)+1 {
		t.Fatalf("admin audit rows went %d -> %d, want one more", len(before), len(entries))
	}

	got := entries[0] // newest first
	if got.Action != AdminActionTokenIssue {
		t.Errorf("Action = %q, want %q", got.Action, AdminActionTokenIssue)
	}
	if got.TargetID != tok.KeyID {
		t.Errorf("TargetID = %q, want %q", got.TargetID, tok.KeyID)
	}
	if got.Actor != "op-alice" {
		t.Errorf("Actor = %q, want %q", got.Actor, "op-alice")
	}
	if got.Detail != "Okta prod" {
		t.Errorf("Detail = %q, want %q", got.Detail, "Okta prod")
	}
}

// A revoke that actually changes state is audited; a redundant revoke of an
// already-dead token is not, so a retried admin command doesn't pollute the
// trail with duplicate entries.
func TestRevokeTokenWritesAdminAuditOnlyOnRealChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	_, tok, err := s.IssueToken(ctx, tenantID, "test", "test-suite", nil)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if err := s.RevokeToken(ctx, tok.KeyID, "op-bob"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	entries, err := s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	got := entries[0]
	if got.Action != AdminActionTokenRevoke || got.Actor != "op-bob" || got.TargetID != tok.KeyID {
		t.Fatalf("latest entry = %+v, want a token.revoke by op-bob targeting %s", got, tok.KeyID)
	}
	countAfterRevoke := len(entries)

	if err := s.RevokeToken(ctx, tok.KeyID, "op-carol"); err != nil {
		t.Fatalf("second RevokeToken: %v", err)
	}

	entries, err = s.ListAdminAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries after idempotent revoke: %v", err)
	}
	if len(entries) != countAfterRevoke {
		t.Errorf("admin audit rows went %d -> %d after a no-op revoke, want unchanged", countAfterRevoke, len(entries))
	}
}

func TestListTokensScopedByTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	if _, _, err := s.IssueToken(ctx, tenantA, "a", "test-suite", nil); err != nil {
		t.Fatalf("IssueToken for tenant A: %v", err)
	}

	tokens, err := s.ListTokens(ctx, tenantB)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("tenant B sees %d of tenant A's tokens, want 0", len(tokens))
	}
}

func TestTokenExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	// Postgres timestamptz has microsecond precision; time.Now() on Linux
	// (unlike macOS in practice) reliably carries real nanoseconds, so an
	// untruncated value never round-trips equal. Truncate before the compare,
	// not just at comparison time, so both sides describe the same instant.
	past := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	_, tok, err := s.IssueToken(ctx, tenantID, "expired", "test-suite", &past)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	got, err := s.GetTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetTokenByKeyID: %v", err)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(past) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, past)
	}
}
