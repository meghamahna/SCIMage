package store

import (
	"context"
	"fmt"
	"time"
)

// Privileged CLI actions. Read/list operations are not audited here, the
// same reasoning audit_log uses for reads: they would bury the mutations a
// reviewer is actually looking for.
const (
	AdminActionTenantCreate = "tenant.create"
	AdminActionTokenIssue   = "token.issue"
	AdminActionTokenRevoke  = "token.revoke"
)

// AdminAuditEntry is a row read back out of admin_audit_log, for review.
type AdminAuditEntry struct {
	ID       int64
	At       time.Time
	TenantID string
	Actor    string
	Action   string
	TargetID string
	Detail   string
}

// insertAdminAudit writes one entry, in the same transaction as the change it
// describes, mirroring insertAudit's own discipline: a tenant or token cannot
// be created, issued or revoked without a record of who did it and when.
func insertAdminAudit(ctx context.Context, q querier, tenantID, actor, action, targetID, detail string) error {
	const stmt = `INSERT INTO admin_audit_log (tenant_id, actor, action, target_id, detail)
	              VALUES ($1, $2, $3, $4, $5)`

	if _, err := q.Exec(ctx, stmt, tenantID, actor, action, targetID, nullable(detail)); err != nil {
		return fmt.Errorf("insert admin audit entry: %w", err)
	}
	return nil
}

// ListAdminAuditEntries returns privileged actions, newest first. An empty
// tenantID lists across every tenant, for an operator reviewing the whole
// deployment rather than one customer.
func (s *Store) ListAdminAuditEntries(ctx context.Context, tenantID string, limit int) ([]AdminAuditEntry, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	q := `SELECT id, at, tenant_id, actor, action, target_id, detail FROM admin_audit_log`
	args := []any{limit}
	if tenantID != "" {
		q += ` WHERE tenant_id = $2`
		args = append(args, tenantID)
	}
	q += ` ORDER BY at DESC, id DESC LIMIT $1`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin audit entries: %w", err)
	}
	defer rows.Close()

	var entries []AdminAuditEntry
	for rows.Next() {
		var (
			e      AdminAuditEntry
			detail *string
		)
		if err := rows.Scan(&e.ID, &e.At, &e.TenantID, &e.Actor, &e.Action, &e.TargetID, &detail); err != nil {
			return nil, fmt.Errorf("list admin audit entries: scan row: %w", err)
		}
		e.Detail = deref(detail)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list admin audit entries: read rows: %w", err)
	}
	return entries, nil
}
