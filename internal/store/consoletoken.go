package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// consoleTokenPrefix is deliberately distinct from tokenPrefix so a leaked
// console credential is recognisable on sight — by a human and by secret
// scanning — and never confusable with a tenant-scoped SCIM token. The two
// live in different tables and authenticate on different listeners; the
// prefix keeps that separation legible in a log or a paste.
const consoleTokenPrefix = "scimage_console_"

// ConsoleToken is a row read back out. Like Token, SecretHash is exposed so
// the console's auth middleware runs the constant-time compare itself — the
// store hands back data, not a verdict. There is no TenantID: a console
// credential authenticates the operator, who works across every tenant.
type ConsoleToken struct {
	KeyID      string
	SecretHash []byte
	Label      string
	CreatedAt  time.Time
	CreatedBy  string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// ParseConsoleToken splits scimage_console_<keyID>_<secret> into its two
// halves, the same fixed-length-keyID reasoning ParseToken uses.
func ParseConsoleToken(raw string) (keyID, secret string, ok bool) {
	rest, ok := strings.CutPrefix(raw, consoleTokenPrefix)
	if !ok {
		return "", "", false
	}
	keyID, secret, ok = strings.Cut(rest, "_")
	if !ok || keyID == "" || secret == "" {
		return "", "", false
	}
	return keyID, secret, true
}

// IssueConsoleToken mirrors IssueToken: it generates a keyID and secret,
// stores sha256(secret), and returns the full plaintext once. The
// admin-audit entry is written in the same transaction as the insert, so a
// console credential can't exist without a record of who issued it. The entry
// carries no tenant (console.token.issue is system-scope; see insertAdminAudit).
func (s *Store) IssueConsoleToken(ctx context.Context, label, createdBy string, expiresAt *time.Time) (plaintext string, tok *ConsoleToken, err error) {
	keyID, err := randomHex(keyIDBytes)
	if err != nil {
		return "", nil, fmt.Errorf("issue console token: %w", err)
	}
	secret, err := randomHex(secretBytes)
	if err != nil {
		return "", nil, fmt.Errorf("issue console token: %w", err)
	}
	hash := sha256.Sum256([]byte(secret))

	const q = `INSERT INTO console_tokens (key_id, secret_hash, label, created_by, expires_at)
	           VALUES ($1, $2, $3, $4, $5)
	           RETURNING key_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("issue console token: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	t, err := scanConsoleToken(tx.QueryRow(ctx, q, keyID, hash[:], label, nullable(createdBy), expiresAt))
	if err != nil {
		return "", nil, fmt.Errorf("issue console token: %w", err)
	}

	if err := insertAdminAudit(ctx, tx, "", createdBy, AdminActionConsoleTokenIssue, keyID, label); err != nil {
		return "", nil, fmt.Errorf("issue console token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("issue console token: commit: %w", err)
	}
	return consoleTokenPrefix + keyID + "_" + secret, t, nil
}

func (s *Store) GetConsoleTokenByKeyID(ctx context.Context, keyID string) (*ConsoleToken, error) {
	const q = `SELECT key_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at
	           FROM console_tokens WHERE key_id = $1`

	t, err := scanConsoleToken(s.pool.QueryRow(ctx, q, keyID))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("get console token %q: %w", keyID, ErrNotFound)
		}
		return nil, fmt.Errorf("get console token %q: %w", keyID, err)
	}
	return t, nil
}

// ListConsoleTokens never returns the secret — only what IssueConsoleToken
// already returned once and the metadata needed to decide whether to revoke.
func (s *Store) ListConsoleTokens(ctx context.Context) ([]ConsoleToken, error) {
	const q = `SELECT key_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at
	           FROM console_tokens ORDER BY created_at, key_id`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list console tokens: %w", err)
	}
	defer rows.Close()

	var tokens []ConsoleToken
	for rows.Next() {
		t, err := scanConsoleToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list console tokens: scan row: %w", err)
		}
		tokens = append(tokens, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list console tokens: read rows: %w", err)
	}
	return tokens, nil
}

// TouchConsoleToken records last use. Best-effort, exactly like TouchToken: it
// describes the token, not the request, so a failure here is a log line, never
// a reason to reject a caller already authenticated.
func (s *Store) TouchConsoleToken(ctx context.Context, keyID string) error {
	const q = `UPDATE console_tokens SET last_used_at = now() WHERE key_id = $1`

	if _, err := s.pool.Exec(ctx, q, keyID); err != nil {
		return fmt.Errorf("touch console token %q: %w", keyID, err)
	}
	return nil
}

// RevokeConsoleToken is idempotent, like RevokeToken: revoking an
// already-revoked token is not an error, and an audit entry is written only
// for an actual state change.
func (s *Store) RevokeConsoleToken(ctx context.Context, keyID, actor string) error {
	const q = `UPDATE console_tokens SET revoked_at = now() WHERE key_id = $1 AND revoked_at IS NULL RETURNING key_id`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("revoke console token %q: begin: %w", keyID, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	var changed string
	if err := tx.QueryRow(ctx, q, keyID).Scan(&changed); err != nil {
		if !isMissingRow(err) {
			return fmt.Errorf("revoke console token %q: %w", keyID, err)
		}
		// No row changed: the key doesn't exist, or it's already revoked.
		// Only the first is an error.
		if _, err := s.GetConsoleTokenByKeyID(ctx, keyID); err != nil {
			return fmt.Errorf("revoke console token %q: %w", keyID, err)
		}
		return nil // already revoked
	}

	if err := insertAdminAudit(ctx, tx, "", actor, AdminActionConsoleTokenRevoke, keyID, ""); err != nil {
		return fmt.Errorf("revoke console token %q: %w", keyID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("revoke console token %q: commit: %w", keyID, err)
	}
	return nil
}

func scanConsoleToken(row pgx.Row) (*ConsoleToken, error) {
	var (
		t                            ConsoleToken
		createdBy                    *string
		lastUsedAt, expiresAt, revAt *time.Time
	)
	if err := row.Scan(&t.KeyID, &t.SecretHash, &t.Label,
		&t.CreatedAt, &createdBy, &lastUsedAt, &expiresAt, &revAt); err != nil {
		return nil, err
	}
	t.CreatedBy = deref(createdBy)
	t.LastUsedAt = lastUsedAt
	t.ExpiresAt = expiresAt
	t.RevokedAt = revAt
	return &t, nil
}
