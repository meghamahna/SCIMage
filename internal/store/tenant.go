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
	CreatedBy string
	CreatedAt time.Time
}

// Named in migrations/000006_admin_governance.up.sql. Matching on it is what
// lets CreateTenant tell a duplicate name apart from any other insert error.
const uniqueTenantNameIndex = "idx_tenants_name_lower"

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

// CreateTenant assigns the id; the caller supplies the display name and who
// is running the command. The admin-audit entry is written in the same
// transaction as the insert, so a tenant can't exist without a record of who
// created it.
func (s *Store) CreateTenant(ctx context.Context, name, createdBy string) (*Tenant, error) {
	id, err := newTenantID()
	if err != nil {
		return nil, err
	}

	const q = `INSERT INTO tenants (id, name, created_by) VALUES ($1, $2, $3)
	           RETURNING id, name, created_by, created_at`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create tenant %q: begin: %w", name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	t, err := scanTenant(tx.QueryRow(ctx, q, id, name, nullable(createdBy)))
	if err != nil {
		if isUniqueViolationOn(err, uniqueTenantNameIndex) {
			return nil, fmt.Errorf("create tenant %q: %w", name, ErrDuplicateTenantName)
		}
		return nil, fmt.Errorf("create tenant %q: %w", name, err)
	}

	if err := insertAdminAudit(ctx, tx, t.ID, createdBy, AdminActionTenantCreate, t.ID, name); err != nil {
		return nil, fmt.Errorf("create tenant %q: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create tenant %q: commit: %w", name, err)
	}
	return t, nil
}

func (s *Store) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	const q = `SELECT id, name, created_by, created_at FROM tenants WHERE id = $1`

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
	const q = `SELECT id, name, created_by, created_at FROM tenants ORDER BY created_at, id`

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
	var (
		t         Tenant
		createdBy *string
	)
	if err := row.Scan(&t.ID, &t.Name, &createdBy, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.CreatedBy = deref(createdBy)
	return &t, nil
}
