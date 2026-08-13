package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TenantAttribute is one entry in a tenant's extensible-attribute registry:
// an extra top-level SCIM attribute the tenant's identity provider sends that
// the server should capture into users.extended_attributes rather than drop.
// Type is only used to declare the attribute in the /Schemas document.
type TenantAttribute struct {
	Name      string
	Type      string
	CreatedBy string
	CreatedAt time.Time
}

// Named by Postgres for the tenant_attributes primary key, so a duplicate
// registration is told apart from any other insert error.
const uniqueAttributeIndex = "tenant_attributes_pkey"

// reservedAttributes are the core User attributes the server already models.
// Registering one would let a captured value shadow a typed field on read, so
// they're refused. Matched case-insensitively.
var reservedAttributes = map[string]bool{
	"schemas": true, "id": true, "username": true, "externalid": true,
	"name": true, "emails": true, "active": true, "meta": true, "password": true,
}

// RegisterAttribute adds a name to the tenant's registry, writing an
// admin_audit_log row in the same transaction — the same discipline tenant
// and token mutations use, so an attribute can't be registered without a
// record of who did it. An empty type defaults to "string".
func (s *Store) RegisterAttribute(ctx context.Context, tenantID, name, typ, actor string) (*TenantAttribute, error) {
	name = strings.TrimSpace(name)
	if reservedAttributes[strings.ToLower(name)] {
		return nil, fmt.Errorf("register attribute %q: %w", name, ErrReservedAttribute)
	}
	if strings.TrimSpace(typ) == "" {
		typ = "string"
	}

	const q = `INSERT INTO tenant_attributes (tenant_id, name, type, created_by)
	           VALUES ($1, $2, $3, $4)
	           RETURNING name, type, created_by, created_at`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("register attribute %q: begin: %w", name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	a, err := scanAttribute(tx.QueryRow(ctx, q, tenantID, name, typ, nullable(actor)))
	if err != nil {
		if isUniqueViolationOn(err, uniqueAttributeIndex) {
			return nil, fmt.Errorf("register attribute %q: %w", name, ErrDuplicateAttribute)
		}
		return nil, fmt.Errorf("register attribute %q: %w", name, err)
	}

	if err := insertAdminAudit(ctx, tx, tenantID, actor, AdminActionAttributeRegister, name, typ); err != nil {
		return nil, fmt.Errorf("register attribute %q: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("register attribute %q: commit: %w", name, err)
	}
	return a, nil
}

// UnregisterAttribute removes a name from the registry so the server stops
// capturing it and stops advertising it. It deliberately does not touch the
// values already stored in any user's extended_attributes — unregistering is
// about future captures, not a destructive edit of existing rows; a stored
// value simply stops being re-captured the next time that user is written.
func (s *Store) UnregisterAttribute(ctx context.Context, tenantID, name, actor string) error {
	name = strings.TrimSpace(name)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("unregister attribute %q: begin: %w", name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	tag, err := tx.Exec(ctx, `DELETE FROM tenant_attributes WHERE tenant_id = $1 AND name = $2`, tenantID, name)
	if err != nil {
		return fmt.Errorf("unregister attribute %q: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unregister attribute %q: %w", name, ErrNotFound)
	}

	if err := insertAdminAudit(ctx, tx, tenantID, actor, AdminActionAttributeUnregister, name, ""); err != nil {
		return fmt.Errorf("unregister attribute %q: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("unregister attribute %q: commit: %w", name, err)
	}
	return nil
}

// ListAttributes returns a tenant's registered attributes, by name. It is the
// lookup the write path uses to decide which body keys to capture, and the
// discovery layer uses to advertise them.
func (s *Store) ListAttributes(ctx context.Context, tenantID string) ([]TenantAttribute, error) {
	const q = `SELECT name, type, created_by, created_at FROM tenant_attributes WHERE tenant_id = $1 ORDER BY name`

	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list attributes: %w", err)
	}
	defer rows.Close()

	var attrs []TenantAttribute
	for rows.Next() {
		a, err := scanAttribute(rows)
		if err != nil {
			return nil, fmt.Errorf("list attributes: scan row: %w", err)
		}
		attrs = append(attrs, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attributes: read rows: %w", err)
	}
	return attrs, nil
}

func scanAttribute(row pgx.Row) (*TenantAttribute, error) {
	var (
		a         TenantAttribute
		createdBy *string
	)
	if err := row.Scan(&a.Name, &a.Type, &createdBy, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.CreatedBy = deref(createdBy)
	return &a, nil
}
