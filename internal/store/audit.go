package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The mutating calls. Reads are not audited — they would bury the changes.
const (
	ActionCreate     = "create"
	ActionReplace    = "replace"
	ActionDeactivate = "deactivate"
)

// A refused mutation is as interesting as a successful one: a burst of denials
// is a signal, and it is invisible if only successes are recorded.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
)

// AuditRecord is the part of an entry the store can't know: who is calling.
// ActorToken is a fingerprint, never the token itself.
type AuditRecord struct {
	ActorToken string
	ActorIP    string
}

// AuditEntry is a row read back out, for review and for SAGE.
type AuditEntry struct {
	ID       int64
	At       time.Time
	Actor    AuditRecord
	Action   string
	Result   string
	Detail   string
	TargetID string
	Before   *User
	After    *User
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so an entry can be
// written inside a mutation's transaction or on its own.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertAudit(ctx context.Context, q querier, rec AuditRecord, action, targetID, result, detail string, before, after *User) error {
	const stmt = `INSERT INTO audit_log
	              (actor_token, actor_ip, action, result, detail, target_id, before, after)
	              VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	beforeJSON, err := marshalUser(before)
	if err != nil {
		return err
	}
	afterJSON, err := marshalUser(after)
	if err != nil {
		return err
	}

	// Empty strings become NULL so "no target" and "no detail" read as absent
	// rather than as an empty value.
	_, err = q.Exec(ctx, stmt,
		rec.ActorToken, nullable(rec.ActorIP), action, result,
		nullable(detail), nullable(targetID), beforeJSON, afterJSON)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

// auditRefusal records a mutation that didn't happen. It runs outside any
// transaction because there is nothing to be atomic with — no row changed.
// A failure here can't roll anything back, so it is logged and swallowed.
func (s *Store) auditRefusal(ctx context.Context, rec AuditRecord, action, targetID, detail string) {
	if err := insertAudit(ctx, s.pool, rec, action, targetID, ResultDenied, detail, nil, nil); err != nil {
		log.Printf("store: could not record refused %s on %q: %v", action, targetID, err)
	}
}

// ListAuditEntries returns the most recent entries, newest first.
func (s *Store) ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}

	const q = `SELECT id, at, actor_token, actor_ip, action, result, detail, target_id, before, after
	           FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`

	rows, err := s.pool.Query(ctx, q, limit)
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
		e                     AuditEntry
		ip, detail, targetID  *string
		beforeJSON, afterJSON []byte
	)

	if err := row.Scan(&e.ID, &e.At, &e.Actor.ActorToken, &ip, &e.Action,
		&e.Result, &detail, &targetID, &beforeJSON, &afterJSON); err != nil {
		return nil, err
	}

	e.Actor.ActorIP = deref(ip)
	e.Detail = deref(detail)
	e.TargetID = deref(targetID)

	var err error
	if e.Before, err = unmarshalUser(beforeJSON); err != nil {
		return nil, err
	}
	if e.After, err = unmarshalUser(afterJSON); err != nil {
		return nil, err
	}

	return &e, nil
}

func marshalUser(u *User) ([]byte, error) {
	if u == nil {
		return nil, nil
	}

	b, err := json.Marshal(u)
	if err != nil {
		return nil, fmt.Errorf("marshal audited user: %w", err)
	}
	return b, nil
}

func unmarshalUser(b []byte) (*User, error) {
	if len(b) == 0 {
		return nil, nil
	}

	var u User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("unmarshal audited user: %w", err)
	}
	return &u, nil
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
