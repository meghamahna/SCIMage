package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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

// refusalAuditTimeout bounds a refusal insert, which no longer ends when the
// request does: a stuck database must not hold the goroutine forever.
const refusalAuditTimeout = 5 * time.Second

// RecordRefusal records a mutation that didn't happen. It runs outside any
// transaction because there is nothing to be atomic with — no row changed.
// The insert ignores ctx's cancellation, so a client hanging up on its refusal
// can't drop the row. A failure here can't roll anything back, so it is logged
// and swallowed.
func (s *Store) RecordRefusal(ctx context.Context, rec AuditRecord, resourceType, action, targetID, detail string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refusalAuditTimeout)
	defer cancel()

	// target_id is a uuid column and a refused request's id is client input:
	// keep the row without its target rather than lose it to a cast error.
	if !isUUID(targetID) {
		targetID = ""
	}

	if err := insertAudit(ctx, s.pool, rec, resourceType, action, targetID, ResultDenied, detail, nil, nil); err != nil {
		slog.Error("could not record a refused mutation",
			"resource_type", resourceType, "action", action, "target_id", targetID, "error", err)
	}
}

// refuseTx ends tx before recording a refusal, so the insert never waits for a
// second pool connection while tx holds one and maybe a row lock. The caller's
// deferred Rollback is then a no-op.
func (s *Store) refuseTx(ctx context.Context, tx pgx.Tx, rec AuditRecord, resourceType, action, targetID, detail string) {
	// A failed Rollback has pgx close the connection, which ends tx just as
	// well, so its error changes nothing here.
	_ = tx.Rollback(ctx)
	s.RecordRefusal(ctx, rec, resourceType, action, targetID, detail)
}

// isUUID accepts the canonical 8-4-4-4-12 hex form, a subset of what Postgres
// parses, so anything it passes casts cleanly.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
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

// ListAuditEntriesSince returns entries at or after `since`, newest first.
// An empty tenantID lists across every tenant, for a deployment-wide review;
// a set one scopes to that tenant, the same isolation ListAuditEntries holds.
// This is what ARIA reads: a time window suits "bulk in a short span" and
// "off-hours" better than a bare row count. The result still clamps to
// MaxPageSize, so a wide window on a busy deployment can't pull the whole table.
func (s *Store) ListAuditEntriesSince(ctx context.Context, tenantID string, since time.Time) ([]AuditEntry, error) {
	where, args := auditSinceWhere(tenantID, since)
	q := `SELECT id, at, tenant_id, resource_type, actor_token, actor_ip, action, result, detail, target_id, before, after
	      FROM audit_log` + where + ` ORDER BY at DESC, id DESC LIMIT ` + strconv.Itoa(MaxPageSize)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries since: %w", err)
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("list audit entries since: %w", err)
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit entries since: %w", err)
	}

	return entries, nil
}

// CountAuditEntriesSince counts the rows ListAuditEntriesSince reads, without
// its cap, so ARIA can tell a full batch that is the whole window from one cut
// short. The predicate is shared with the list on purpose: with no upper bound
// on either, a row written between the two reads only raises the count.
func (s *Store) CountAuditEntriesSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	where, args := auditSinceWhere(tenantID, since)
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count audit entries since: %w", err)
	}
	return n, nil
}

// auditSinceWhere is the window ListAuditEntriesSince and CountAuditEntriesSince
// share; an empty tenantID spans every tenant.
func auditSinceWhere(tenantID string, since time.Time) (string, []any) {
	if tenantID == "" {
		return ` WHERE at >= $1`, []any{since}
	}
	return ` WHERE at >= $1 AND tenant_id = $2`, []any{since, tenantID}
}

// AuditWindowStats counts audit entries and distinct callers in [start, end),
// for the console's window-over-window comparison on the ARIA page. It counts in
// SQL rather than fetching rows, so it isn't bounded by MaxPageSize the way
// ListAuditEntriesSince is. An empty tenantID counts across every tenant.
func (s *Store) AuditWindowStats(ctx context.Context, tenantID string, start, end time.Time) (total, callers int, err error) {
	q := `SELECT count(*), count(DISTINCT actor_token) FROM audit_log WHERE at >= $1 AND at < $2`
	args := []any{start, end}
	if tenantID != "" {
		q += ` AND tenant_id = $3`
		args = append(args, tenantID)
	}
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&total, &callers); err != nil {
		return 0, 0, fmt.Errorf("audit window stats: %w", err)
	}
	return total, callers, nil
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
