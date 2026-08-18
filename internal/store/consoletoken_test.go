package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

// Console tokens are system-wide, not tenant-scoped, so unlike the rest of the
// store suite they can't lean on per-tenant isolation. Every test issues
// through this helper, which cleans up its own console_tokens row and the
// NULL-tenant admin-audit entries it wrote, and every assertion keys off the
// returned keyID rather than the shared table's length.
func issueConsoleToken(t *testing.T, s *Store, label, createdBy string, expiresAt *time.Time) (string, *ConsoleToken) {
	t.Helper()
	ctx := context.Background()

	plaintext, tok, err := s.IssueConsoleToken(ctx, label, createdBy, expiresAt)
	if err != nil {
		t.Fatalf("IssueConsoleToken(%q): %v", label, err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := s.pool.Exec(ctx, `DELETE FROM admin_audit_log WHERE tenant_id IS NULL AND target_id = $1`, tok.KeyID); err != nil {
			t.Errorf("cleanup console audit for %s: %v", tok.KeyID, err)
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM console_tokens WHERE key_id = $1`, tok.KeyID); err != nil {
			t.Errorf("cleanup console token %s: %v", tok.KeyID, err)
		}
	})

	return plaintext, tok
}

func TestParseConsoleToken(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantKey string
		wantSec string
		wantOK  bool
	}{
		{"well formed", "scimage_console_abc123_def456", "abc123", "def456", true},
		{"secret with underscores", "scimage_console_abc123_de_f4_56", "abc123", "de_f4_56", true},
		{"a scim token is not a console token", "scimage_abc123_def456", "", "", false},
		{"missing prefix", "abc123_def456", "", "", false},
		{"no separator", "scimage_console_abc123", "", "", false},
		{"empty secret", "scimage_console_abc123_", "", "", false},
		{"empty key id", "scimage_console__def456", "", "", false},
		{"empty string", "", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keyID, secret, ok := ParseConsoleToken(tc.raw)
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

func TestIssueConsoleToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	plaintext, tok := issueConsoleToken(t, s, "ops laptop", "test-suite", nil)

	keyID, secret, ok := ParseConsoleToken(plaintext)
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
	if tok.RevokedAt != nil || tok.ExpiresAt != nil || tok.LastUsedAt != nil {
		t.Errorf("a fresh token has unset revoked/expires/last_used, got %+v", tok)
	}

	got, err := s.GetConsoleTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetConsoleTokenByKeyID: %v", err)
	}
	if got.Label != "ops laptop" {
		t.Errorf("Label = %q, want %q", got.Label, "ops laptop")
	}
}

func TestGetConsoleTokenByKeyIDUnknown(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetConsoleTokenByKeyID(context.Background(), "no-such-key"); err == nil {
		t.Error("expected an error for an unknown key id, got nil")
	}
}

func TestTouchConsoleToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, tok := issueConsoleToken(t, s, "test", "test-suite", nil)

	if err := s.TouchConsoleToken(ctx, tok.KeyID); err != nil {
		t.Fatalf("TouchConsoleToken: %v", err)
	}

	got, err := s.GetConsoleTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetConsoleTokenByKeyID: %v", err)
	}
	if got.LastUsedAt == nil {
		t.Error("LastUsedAt is still nil after TouchConsoleToken")
	}
}

func TestRevokeConsoleToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, tok := issueConsoleToken(t, s, "test", "test-suite", nil)

	if err := s.RevokeConsoleToken(ctx, tok.KeyID, "test-suite"); err != nil {
		t.Fatalf("first RevokeConsoleToken: %v", err)
	}

	got, err := s.GetConsoleTokenByKeyID(ctx, tok.KeyID)
	if err != nil {
		t.Fatalf("GetConsoleTokenByKeyID: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("RevokedAt is still nil after RevokeConsoleToken")
	}

	t.Run("is idempotent", func(t *testing.T) {
		if err := s.RevokeConsoleToken(ctx, tok.KeyID, "test-suite"); err != nil {
			t.Fatalf("second RevokeConsoleToken: %v", err)
		}
	})

	t.Run("unknown key id is an error", func(t *testing.T) {
		if err := s.RevokeConsoleToken(ctx, "no-such-key", "test-suite"); err == nil {
			t.Error("expected an error revoking an unknown key id, got nil")
		}
	})
}

// The console credential's issue is a privileged action, audited system-wide
// (NULL tenant) so an operator can answer "who minted console access and when."
func TestIssueConsoleTokenWritesAdminAudit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, tok := issueConsoleToken(t, s, "ops laptop", "op-alice", nil)

	entries, err := s.ListAdminAuditEntries(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}

	got := findAuditByTarget(entries, tok.KeyID)
	if got == nil {
		t.Fatalf("no admin audit entry targeting console token %s", tok.KeyID)
	}
	if got.Action != AdminActionConsoleTokenIssue {
		t.Errorf("Action = %q, want %q", got.Action, AdminActionConsoleTokenIssue)
	}
	if got.TenantID != "" {
		t.Errorf("TenantID = %q, want empty (console actions are system-scope)", got.TenantID)
	}
	if got.Actor != "op-alice" {
		t.Errorf("Actor = %q, want %q", got.Actor, "op-alice")
	}
	if got.Detail != "ops laptop" {
		t.Errorf("Detail = %q, want %q", got.Detail, "ops laptop")
	}
}

// A revoke that changes state is audited; a redundant revoke of an
// already-dead token is not.
func TestRevokeConsoleTokenWritesAdminAuditOnlyOnRealChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, tok := issueConsoleToken(t, s, "test", "test-suite", nil)

	if err := s.RevokeConsoleToken(ctx, tok.KeyID, "op-bob"); err != nil {
		t.Fatalf("RevokeConsoleToken: %v", err)
	}

	entries, err := s.ListAdminAuditEntries(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries: %v", err)
	}
	revokes := countAuditByTargetAction(entries, tok.KeyID, AdminActionConsoleTokenRevoke)
	if revokes != 1 {
		t.Fatalf("console.token.revoke entries for %s = %d, want 1", tok.KeyID, revokes)
	}

	if err := s.RevokeConsoleToken(ctx, tok.KeyID, "op-carol"); err != nil {
		t.Fatalf("second RevokeConsoleToken: %v", err)
	}

	entries, err = s.ListAdminAuditEntries(ctx, "", 0)
	if err != nil {
		t.Fatalf("ListAdminAuditEntries after idempotent revoke: %v", err)
	}
	if got := countAuditByTargetAction(entries, tok.KeyID, AdminActionConsoleTokenRevoke); got != 1 {
		t.Errorf("console.token.revoke entries for %s = %d after a no-op revoke, want still 1", tok.KeyID, got)
	}
}

func TestListConsoleTokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, tok := issueConsoleToken(t, s, "in the list", "test-suite", nil)

	tokens, err := s.ListConsoleTokens(ctx)
	if err != nil {
		t.Fatalf("ListConsoleTokens: %v", err)
	}

	var found *ConsoleToken
	for i := range tokens {
		if tokens[i].KeyID == tok.KeyID {
			found = &tokens[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("issued console token %s not present in the listing", tok.KeyID)
	}
	if found.Label != "in the list" {
		t.Errorf("Label = %q, want %q", found.Label, "in the list")
	}
	if found.SecretHash == nil {
		t.Error("SecretHash is nil in the listing — auth needs it for the compare")
	}
}

func findAuditByTarget(entries []AdminAuditEntry, targetID string) *AdminAuditEntry {
	for i := range entries {
		if entries[i].TargetID == targetID {
			return &entries[i]
		}
	}
	return nil
}

func countAuditByTargetAction(entries []AdminAuditEntry, targetID, action string) int {
	n := 0
	for _, e := range entries {
		if e.TargetID == targetID && e.Action == action {
			n++
		}
	}
	return n
}
