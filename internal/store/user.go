package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Optional attributes are pointers: the columns are nullable and SCIM
// distinguishes an absent attribute from an empty one.
type User struct {
	ID         string
	UserName   string
	GivenName  *string
	FamilyName *string
	Email      *string
	Active     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Order must match scanUser.
const userColumns = `id, user_name, given_name, family_name, email, active, created_at, updated_at`

// MaxPageSize caps ListUsers. limit sizes the result slice before any row is
// read, so an unbounded client-supplied count is a memory-exhaustion vector.
// RFC 7644 §3.4.2.4 allows a provider maximum.
const MaxPageSize = 200

// Named in migrations/000001_create_users_table.up.sql. Matching on it keeps a
// unique index added later from being misreported as a userName clash.
const uniqueUserNameIndex = "idx_users_user_name_lower"

// pgx.Rows satisfies pgx.Row, so single- and multi-row queries share this.
func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID,
		&u.UserName,
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

// CreateUser returns the stored row, so the caller gets the server-assigned id
// and timestamps. The clash is case-insensitive: RFC 7643 makes userName
// caseExact=false, so "bjensen" and "BJensen" are the same identity.
func (s *Store) CreateUser(ctx context.Context, u *User) (*User, error) {
	const q = `INSERT INTO users (user_name, given_name, family_name, email, active)
	           VALUES ($1, $2, $3, $4, $5)
	           RETURNING ` + userColumns

	created, err := scanUser(s.pool.QueryRow(ctx, q,
		u.UserName, u.GivenName, u.FamilyName, u.Email, u.Active))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create user %q: %w", u.UserName, ErrDuplicateUserName)
		}
		return nil, fmt.Errorf("create user %q: %w", u.UserName, err)
	}
	return created, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	u, err := scanUser(s.pool.QueryRow(ctx, q, id))
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
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]User, int, error) {
	if limit < 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list users: limit and offset must not be negative (got %d, %d)", limit, offset)
	}
	limit = min(limit, MaxPageSize)

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	const q = `SELECT ` + userColumns + `
	           FROM users
	           ORDER BY created_at, id
	           LIMIT $1 OFFSET $2`

	rows, err := s.pool.Query(ctx, q, limit, offset)
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
// left alone; updated_at is set here rather than by a trigger.
func (s *Store) UpdateUser(ctx context.Context, id string, u *User) (*User, error) {
	const q = `UPDATE users
	           SET user_name = $2,
	               given_name = $3,
	               family_name = $4,
	               email = $5,
	               active = $6,
	               updated_at = now()
	           WHERE id = $1
	           RETURNING ` + userColumns

	updated, err := scanUser(s.pool.QueryRow(ctx, q,
		id, u.UserName, u.GivenName, u.FamilyName, u.Email, u.Active))
	if err != nil {
		switch {
		case isMissingRow(err):
			return nil, fmt.Errorf("update user %q: %w", id, ErrNotFound)
		case isUniqueViolation(err):
			return nil, fmt.Errorf("update user %q: %w", id, ErrDuplicateUserName)
		default:
			return nil, fmt.Errorf("update user %q: %w", id, err)
		}
	}
	return updated, nil
}

// DeactivateUser is the soft delete behind DELETE /Users/{id}: the row stays so
// audit history keeps pointing at a real user. Returns the updated row for the
// audit log's after-state, and is idempotent so a retried delete succeeds.
func (s *Store) DeactivateUser(ctx context.Context, id string) (*User, error) {
	const q = `UPDATE users
	           SET active = false, updated_at = now()
	           WHERE id = $1
	           RETURNING ` + userColumns

	deactivated, err := scanUser(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("deactivate user %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("deactivate user %q: %w", id, err)
	}
	return deactivated, nil
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
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == uniqueUserNameIndex
}
