package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Tenant is one of the app owner's own customers: an isolated slice of users,
// audit history and issued tokens, distinguished only by tenant_id.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// newTenantID is a prefixed opaque string rather than a plain UUID: it is
// pasted once into a customer's identity provider as part of the SCIM base
// URL, so the prefix makes it recognisable in logs and admin CLI output, and
// its randomness keeps it immutable — nothing about it should ever need to
// change once an IdP has it configured.
func newTenantID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate tenant id: %w", err)
	}
	return "tenant_" + hex.EncodeToString(b), nil
}

// CreateTenant assigns the id; the caller only supplies the display name.
func (s *Store) CreateTenant(ctx context.Context, name string) (*Tenant, error) {
	id, err := newTenantID()
	if err != nil {
		return nil, err
	}

	const q = `INSERT INTO tenants (id, name) VALUES ($1, $2) RETURNING id, name, created_at`

	t, err := scanTenant(s.pool.QueryRow(ctx, q, id, name))
	if err != nil {
		return nil, fmt.Errorf("create tenant %q: %w", name, err)
	}
	return t, nil
}

func (s *Store) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	const q = `SELECT id, name, created_at FROM tenants WHERE id = $1`

	t, err := scanTenant(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("get tenant %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("get tenant %q: %w", id, err)
	}
	return t, nil
}

// ListTenants returns every tenant, oldest first. There is no pagination:
// this is an admin-CLI-only path, not a network-facing listing a caller could
// use to exhaust memory.
func (s *Store) ListTenants(ctx context.Context) ([]Tenant, error) {
	const q = `SELECT id, name, created_at FROM tenants ORDER BY created_at, id`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, fmt.Errorf("list tenants: scan row: %w", err)
		}
		tenants = append(tenants, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tenants: read rows: %w", err)
	}
	return tenants, nil
}

func scanTenant(row pgx.Row) (*Tenant, error) {
	var t Tenant
	if err := row.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
