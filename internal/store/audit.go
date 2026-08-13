package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The mutating calls. Reads are not audited — they would bury the changes.
// ActionDelete is distinct from ActionDeactivate: a group has no `active`
// attribute to deactivate into, so removing one is a real deletion.
const (
	ActionCreate     = "create"
	ActionReplace    = "replace"
	ActionDeactivate = "deactivate"
	ActionDelete     = "delete"
)

// One audit_log table serves every resource kind, so ARIA reads one
// trail rather than several that can drift apart in shape. resource_type
// says which struct before/after decode as.
const (
	ResourceUser  = "user"
	ResourceGroup = "group"
)

// A refused mutation is as interesting as a successful one: a burst of denials
// is a signal, and it is invisible if only successes are recorded.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
)

// AuditRecord is the part of an entry the store can't know: who is calling.
// ActorToken identifies the caller's issued token (its key id, never the
// secret) and TenantID is that token's resolved tenant — both come from the
// auth middleware, not from the mutation's own arguments, so the audit trail
// always names the caller that authenticated even if it ever disagreed with
// a query's own tenantID parameter.
type AuditRecord struct {
	ActorToken string
	ActorIP    string
	TenantID   string
}

// AuditEntry is a row read back out, for review and for ARIA. Actor.TenantID
// is populated from the row's own tenant_id column.
//
// Before/After stay raw JSON rather than a typed *User: one row might
// describe a User and the next a Group, and a reviewer (or ARIA)
// decodes into whichever ResourceType says it is.
type AuditEntry struct {
	ID           int64
	At           time.Time
	Actor        AuditRecord
	ResourceType string
	Action       string
	Result       string
	Detail       string
	TargetID     string
	Before       json.RawMessage
	After        json.RawMessage
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so an entry — or a
// group's membership — can be read or written inside a mutation's
// transaction or on its own.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insertAudit(ctx context.Context, q querier, rec AuditRecord, resourceType, action, targetID, result, detail string, before, after any) error {
	const stmt = `INSERT INTO audit_log
	              (tenant_id, resource_type, actor_token, actor_ip, action, result, detail, target_id, before, after)
	              VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	beforeJSON, err := marshalAudited(before)
	if err != nil {
		return err
	}
	afterJSON, err := marshalAudited(after)
	if err != nil {
		return err
	}

	// Empty strings become NULL so "no target" and "no detail" read as absent
	// rather than as an empty value.
	_, err = q.Exec(ctx, stmt,
		rec.TenantID, resourceType, rec.ActorToken, nullable(rec.ActorIP), action, result,
		nullable(detail), nullable(targetID), beforeJSON, afterJSON)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

// auditRefusal records a mutation that didn't happen. It runs outside any
// transaction because there is nothing to be atomic with — no row changed.
// A failure here can't roll anything back, so it is logged and swallowed.
func (s *Store) auditRefusal(ctx context.Context, rec AuditRecord, resourceType, action, targetID, detail string) {
	if err := insertAudit(ctx, s.pool, rec, resourceType, action, targetID, ResultDenied, detail, nil, nil); err != nil {
		slog.Error("could not record a refused mutation",
			"resource_type", resourceType, "action", action, "target_id", targetID, "error", err)
	}
}

// ListAuditEntries returns one tenant's most recent entries, newest first.
func (s *Store) ListAuditEntries(ctx context.Context, tenantID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	const q = `SELECT id, at, tenant_id, resource_type, actor_token, actor_ip, action, result, detail, target_id, before, after
	           FROM audit_log WHERE tenant_id = $2 ORDER BY at DESC, id DESC LIMIT $1`

	rows, err := s.pool.Query(ctx, q, limit, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()

	entries := make([]AuditEntry, 0, limit)
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("list audit entries: %w", err)
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}

	return entries, nil
}

func scanAuditEntry(row pgx.Row) (*AuditEntry, error) {
	var (
		e                    AuditEntry
		ip, detail, targetID *string
	)

	if err := row.Scan(&e.ID, &e.At, &e.Actor.TenantID, &e.ResourceType, &e.Actor.ActorToken, &ip, &e.Action,
		&e.Result, &detail, &targetID, &e.Before, &e.After); err != nil {
		return nil, err
	}

	e.Actor.ActorIP = deref(ip)
	e.Detail = deref(detail)
	e.TargetID = deref(targetID)

	return &e, nil
}

// marshalAudited marshals the before/after image insertAudit is given. A
// plain `v == nil` check is not enough: a typed nil pointer (e.g. a *User
// left nil) boxed into an `any` is a non-nil interface, so each resource
// type that can appear here checks its own nil-ness before marshaling.
func marshalAudited(v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case *User:
		if t == nil {
			return nil, nil
		}
	case *Group:
		if t == nil {
			return nil, nil
		}
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal audited %T: %w", v, err)
	}
	return b, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
