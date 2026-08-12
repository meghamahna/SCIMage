package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// tokenPrefix lets automated secret scanning (GitHub's included) recognise a
// leaked SCIMage token on sight.
const tokenPrefix = "scimage_"

// keyIDBytes/secretBytes are the entropy behind each half, before hex
// encoding doubles their length. 32 bytes of secret is generous for a
// machine-generated, high-entropy credential — the same reasoning a
// bcrypt/scrypt KDF exists for low-entropy human passwords, which this isn't.
const (
	keyIDBytes  = 8
	secretBytes = 32
)

// Token is a row read back out. SecretHash is exposed so the caller (the
// auth middleware) can run the constant-time compare itself, the same place
// that decision already lives for the single-token model this replaces —
// the store hands back data, not an authorization verdict.
type Token struct {
	KeyID      string
	TenantID   string
	SecretHash []byte
	Label      string
	CreatedAt  time.Time
	CreatedBy  string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// ParseToken splits scimage_<keyID>_<secret> into its two halves. keyID is
// fixed-length hex, so splitting on the first underscore after the prefix is
// unambiguous even though the secret half also contains no underscores.
func ParseToken(raw string) (keyID, secret string, ok bool) {
	rest, ok := strings.CutPrefix(raw, tokenPrefix)
	if !ok {
		return "", "", false
	}
	keyID, secret, ok = strings.Cut(rest, "_")
	if !ok || keyID == "" || secret == "" {
		return "", "", false
	}
	return keyID, secret, true
}

// IssueToken generates a new keyID and secret, stores sha256(secret), and
// returns the full plaintext token. That's the only time it exists in full —
// the row keeps only the hash, so a database dump can't be replayed as a
// live credential.
func (s *Store) IssueToken(ctx context.Context, tenantID, label, createdBy string, expiresAt *time.Time) (plaintext string, tok *Token, err error) {
	keyID, err := randomHex(keyIDBytes)
	if err != nil {
		return "", nil, fmt.Errorf("issue token: %w", err)
	}
	secret, err := randomHex(secretBytes)
	if err != nil {
		return "", nil, fmt.Errorf("issue token: %w", err)
	}
	hash := sha256.Sum256([]byte(secret))

	const q = `INSERT INTO scim_tokens (key_id, tenant_id, secret_hash, label, created_by, expires_at)
	           VALUES ($1, $2, $3, $4, $5, $6)
	           RETURNING key_id, tenant_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at`

	t, err := scanToken(s.pool.QueryRow(ctx, q, keyID, tenantID, hash[:], label, nullable(createdBy), expiresAt))
	if err != nil {
		return "", nil, fmt.Errorf("issue token for tenant %q: %w", tenantID, err)
	}
	return tokenPrefix + keyID + "_" + secret, t, nil
}

func (s *Store) GetTokenByKeyID(ctx context.Context, keyID string) (*Token, error) {
	const q = `SELECT key_id, tenant_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at
	           FROM scim_tokens WHERE key_id = $1`

	t, err := scanToken(s.pool.QueryRow(ctx, q, keyID))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("get token %q: %w", keyID, ErrNotFound)
		}
		return nil, fmt.Errorf("get token %q: %w", keyID, err)
	}
	return t, nil
}

// ListTokens never returns the secret — only what IssueToken already
// returned once and the metadata needed to decide whether to revoke it.
func (s *Store) ListTokens(ctx context.Context, tenantID string) ([]Token, error) {
	const q = `SELECT key_id, tenant_id, secret_hash, label, created_at, created_by, last_used_at, expires_at, revoked_at
	           FROM scim_tokens WHERE tenant_id = $1 ORDER BY created_at, key_id`

	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list tokens for tenant %q: %w", tenantID, err)
	}
	defer rows.Close()

	var tokens []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list tokens for tenant %q: scan row: %w", tenantID, err)
		}
		tokens = append(tokens, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tokens for tenant %q: read rows: %w", tenantID, err)
	}
	return tokens, nil
}

// TouchToken records last use. Best-effort by design: it describes the
// token, not the request's data, so a caller treats a failure here as a log
// line, never a reason to fail the request it's already authorized.
func (s *Store) TouchToken(ctx context.Context, keyID string) error {
	const q = `UPDATE scim_tokens SET last_used_at = now() WHERE key_id = $1`

	if _, err := s.pool.Exec(ctx, q, keyID); err != nil {
		return fmt.Errorf("touch token %q: %w", keyID, err)
	}
	return nil
}

// RevokeToken is idempotent, like DeactivateUser: revoking an already-revoked
// token is not an error, so a retried admin command succeeds.
func (s *Store) RevokeToken(ctx context.Context, keyID string) error {
	const q = `UPDATE scim_tokens SET revoked_at = now() WHERE key_id = $1 AND revoked_at IS NULL`

	tag, err := s.pool.Exec(ctx, q, keyID)
	if err != nil {
		return fmt.Errorf("revoke token %q: %w", keyID, err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	if _, err := s.GetTokenByKeyID(ctx, keyID); err != nil {
		return fmt.Errorf("revoke token %q: %w", keyID, err)
	}
	return nil // already revoked
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func scanToken(row pgx.Row) (*Token, error) {
	var (
		t                            Token
		createdBy                    *string
		lastUsedAt, expiresAt, revAt *time.Time
	)
	if err := row.Scan(&t.KeyID, &t.TenantID, &t.SecretHash, &t.Label,
		&t.CreatedAt, &createdBy, &lastUsedAt, &expiresAt, &revAt); err != nil {
		return nil, err
	}
	t.CreatedBy = deref(createdBy)
	t.LastUsedAt = lastUsedAt
	t.ExpiresAt = expiresAt
	t.RevokedAt = revAt
	return &t, nil
}
