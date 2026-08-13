package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Optional attributes are pointers: the columns are nullable and SCIM
// distinguishes an absent attribute from an empty one.
// Tags are for the audit log's before/after jsonb, the only place a User is
// serialised.
type User struct {
	ID         string    `json:"id"`
	UserName   string    `json:"userName"`
	ExternalID *string   `json:"externalId,omitempty"`
	GivenName  *string   `json:"givenName,omitempty"`
	FamilyName *string   `json:"familyName,omitempty"`
	Email      *string   `json:"email,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Order must match scanUser.
const userColumns = `id, user_name, external_id, given_name, family_name, email, active, created_at, updated_at`

// UserFilter narrows a listing to the equality matches SCIM clients use to
// reconcile. The zero value (besides tenantID) lists everything for that
// tenant.
//
// userName matches case-insensitively via the same lower(user_name) index that
// enforces uniqueness, so a lookup agrees with what a create would allow.
type UserFilter struct {
	UserName   string
	ExternalID string
}

// clause builds the WHERE fragment and its arguments, always anchored on
// tenant_id: every other condition narrows within one tenant, never across.
// Values are always bound, never interpolated.
func (f UserFilter) clause(tenantID string) (string, []any) {
	args := []any{tenantID}
	conds := []string{"tenant_id = $1"}

	if f.UserName != "" {
		args = append(args, f.UserName)
		conds = append(conds, "lower(user_name) = lower($"+strconv.Itoa(len(args))+")")
	}
	if f.ExternalID != "" {
		args = append(args, f.ExternalID)
		conds = append(conds, "external_id = $"+strconv.Itoa(len(args)))
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

// Change is the before/after pair every mutation hands to the audit log.
// Before is nil for a create.
type Change struct {
	Before *User
	After  *User
}

// Postgres only gained OLD/NEW in RETURNING at 18, so on 16 the before-image
// comes from a CTE reading the row in the same statement as the UPDATE. Both
// sub-statements see one snapshot, so this is atomic — no read-then-write gap
// where a concurrent update could slip between the two halves of an audit entry.
var (
	beforeColumns = qualify("b")
	afterColumns  = qualify("a")
)

func qualify(alias string) string {
	cols := strings.Split(userColumns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// MaxPageSize caps ListUsers. limit sizes the result slice before any row is
// read, so an unbounded client-supplied count is a memory-exhaustion vector.
// RFC 7644 §3.4.2.4 allows a provider maximum.
const MaxPageSize = 200

// Named in migrations/000005_multi_tenancy.up.sql. Matching on it keeps a
// unique index added later from being misreported as a userName clash.
const uniqueUserNameIndex = "idx_users_tenant_username"

// pgx.Rows satisfies pgx.Row, so single- and multi-row queries share this.
func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID,
		&u.UserName,
		&u.ExternalID,
		&u.GivenName,
		&u.FamilyName,
		&u.Email,
		&u.Active,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func scanChange(row pgx.Row) (*Change, error) {
	var before, after User
	err := row.Scan(
		&before.ID, &before.UserName, &before.ExternalID, &before.GivenName,
		&before.FamilyName, &before.Email, &before.Active, &before.CreatedAt, &before.UpdatedAt,
		&after.ID, &after.UserName, &after.ExternalID, &after.GivenName,
		&after.FamilyName, &after.Email, &after.Active, &after.CreatedAt, &after.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &Change{Before: &before, After: &after}, nil
}

// CreateUser returns the stored row, so the caller gets the server-assigned id
// and timestamps. The clash is case-insensitive: RFC 7643 makes userName
// caseExact=false, so "bjensen" and "BJensen" are the same identity — within
// one tenant; two tenants can each provision their own "bjensen".
func (s *Store) CreateUser(ctx context.Context, tenantID string, u *User, rec AuditRecord) (*User, error) {
	const q = `INSERT INTO users (tenant_id, user_name, external_id, given_name, family_name, email, active)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)
	           RETURNING ` + userColumns

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create user %q: begin: %w", u.UserName, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	created, err := scanUser(tx.QueryRow(ctx, q,
		tenantID, u.UserName, u.ExternalID, u.GivenName, u.FamilyName, u.Email, u.Active))
	if err != nil {
		if isUniqueViolation(err) {
			s.auditRefusal(ctx, rec, ResourceUser, ActionCreate, "", "duplicate userName")
			return nil, fmt.Errorf("create user %q: %w", u.UserName, ErrDuplicateUserName)
		}
		return nil, fmt.Errorf("create user %q: %w", u.UserName, err)
	}

	if err := insertAudit(ctx, tx, rec, ResourceUser, ActionCreate, created.ID, ResultSuccess, "", nil, created); err != nil {
		return nil, fmt.Errorf("create user %q: %w", u.UserName, err)
	}

	if err := s.enqueueChange(ctx, tx, tenantID, EventUserCreated, created.ID, nil, created); err != nil {
		return nil, fmt.Errorf("create user %q: %w", u.UserName, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create user %q: commit: %w", u.UserName, err)
	}
	return created, nil
}

// GetUser is scoped by tenant as well as id, so a token from one tenant
// naming another tenant's real user id gets the same 404 as a made-up one —
// isolation holds even when the caller already knows a valid UUID.
func (s *Store) GetUser(ctx context.Context, tenantID, id string) (*User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE tenant_id = $1 AND id = $2`

	u, err := scanUser(s.pool.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("get user %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("get user %q: %w", id, err)
	}
	return u, nil
}

// ListUsers returns a page plus the total row count for SCIM's totalResults.
//
// The count is a separate query, not a count(*) OVER () window: a window
// returns nothing once the offset passes the end of the table, reporting 0 for
// a page a client can legitimately ask for. The two aren't in a transaction, so
// a concurrent insert can land between them — not worth repeatable-read here.
//
// created_at ties are broken by id, since a bulk insert shares one now() and
// paging would otherwise skip or repeat rows. Inactive users are included:
// DeactivateUser keeps the row precisely so it stays listed.
func (s *Store) ListUsers(ctx context.Context, tenantID string, limit, offset int, f UserFilter) ([]User, int, error) {
	if limit < 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list users: limit and offset must not be negative (got %d, %d)", limit, offset)
	}
	limit = min(limit, MaxPageSize)

	where, args := f.clause(tenantID)

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	q := `SELECT ` + userColumns + `
	      FROM users` + where + `
	      ORDER BY created_at, id
	      LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)

	rows, err := s.pool.Query(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0, limit)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("list users: scan row: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list users: read rows: %w", err)
	}

	return users, total, nil
}

// UpdateUser is the full replace behind PUT /Users/{id}. id and created_at are
// left alone; updated_at is set here rather than by a trigger. It returns both
// images so the audit log records what actually changed.
func (s *Store) UpdateUser(ctx context.Context, tenantID, id string, u *User, rec AuditRecord) (*Change, error) {
	q := `WITH b AS (
	          SELECT ` + userColumns + ` FROM users WHERE tenant_id = $1 AND id = $2
	      ), a AS (
	          UPDATE users
	          SET user_name = $3,
	              external_id = $4,
	              given_name = $5,
	              family_name = $6,
	              email = $7,
	              active = $8,
	              updated_at = now()
	          WHERE tenant_id = $1 AND id = $2
	          RETURNING ` + userColumns + `
	      )
	      SELECT ` + beforeColumns + `, ` + afterColumns + ` FROM b, a`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("update user %q: begin: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	change, err := scanChange(tx.QueryRow(ctx, q,
		tenantID, id, u.UserName, u.ExternalID, u.GivenName, u.FamilyName, u.Email, u.Active))
	if err != nil {
		switch {
		case isMissingRow(err):
			s.auditRefusal(ctx, rec, ResourceUser, ActionReplace, id, "no such user")
			return nil, fmt.Errorf("update user %q: %w", id, ErrNotFound)
		case isUniqueViolation(err):
			s.auditRefusal(ctx, rec, ResourceUser, ActionReplace, id, "duplicate userName")
			return nil, fmt.Errorf("update user %q: %w", id, ErrDuplicateUserName)
		default:
			return nil, fmt.Errorf("update user %q: %w", id, err)
		}
	}

	if err := insertAudit(ctx, tx, rec, ResourceUser, ActionReplace, id, ResultSuccess, "", change.Before, change.After); err != nil {
		return nil, fmt.Errorf("update user %q: %w", id, err)
	}

	if err := s.enqueueChange(ctx, tx, tenantID, changeEventType(change.Before, change.After), id, change.Before, change.After); err != nil {
		return nil, fmt.Errorf("update user %q: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("update user %q: commit: %w", id, err)
	}
	return change, nil
}

// DeactivateUser is the soft delete behind DELETE /Users/{id}: the row stays so
// audit history keeps pointing at a real user. Returns both images for the
// audit log, and is idempotent so a retried delete succeeds.
func (s *Store) DeactivateUser(ctx context.Context, tenantID, id string, rec AuditRecord) (*Change, error) {
	q := `WITH b AS (
	          SELECT ` + userColumns + ` FROM users WHERE tenant_id = $1 AND id = $2
	      ), a AS (
	          UPDATE users SET active = false, updated_at = now()
	          WHERE tenant_id = $1 AND id = $2
	          RETURNING ` + userColumns + `
	      )
	      SELECT ` + beforeColumns + `, ` + afterColumns + ` FROM b, a`

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("deactivate user %q: begin: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	change, err := scanChange(tx.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		if isMissingRow(err) {
			s.auditRefusal(ctx, rec, ResourceUser, ActionDeactivate, id, "no such user")
			return nil, fmt.Errorf("deactivate user %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("deactivate user %q: %w", id, err)
	}

	if err := insertAudit(ctx, tx, rec, ResourceUser, ActionDeactivate, id, ResultSuccess, "", change.Before, change.After); err != nil {
		return nil, fmt.Errorf("deactivate user %q: %w", id, err)
	}

	if err := s.enqueueChange(ctx, tx, tenantID, EventUserDeactivated, id, change.Before, change.After); err != nil {
		return nil, fmt.Errorf("deactivate user %q: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("deactivate user %q: commit: %w", id, err)
	}
	return change, nil
}

// 22P02 is invalid_text_representation. id is the only non-text parameter these
// queries bind, so it can only be a junk uuid — a 404 to a client, not a 500.
func isMissingRow(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

func isUniqueViolation(err error) bool {
	return isUniqueViolationOn(err, uniqueUserNameIndex)
}

// isUniqueViolationOn matches a specific unique index by name, so a 23505 on
// one constraint isn't misreported as a clash on another (userName vs.
// tenant name, say).
func isUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == constraint
}
