// Package store is the Postgres-backed persistence layer for SCIM users.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The HTTP layer maps these onto SCIM status codes: 404 and 409.
var (
	ErrNotFound          = errors.New("user not found")
	ErrDuplicateUserName = errors.New("userName already exists")
)

type Store struct {
	pool *pgxpool.Pool
}

// New pings the database so a bad DATABASE_URL fails at startup, not on the
// first request.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}
