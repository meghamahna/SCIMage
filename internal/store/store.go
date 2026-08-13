// Package store is the Postgres-backed persistence layer for SCIM users.
package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The HTTP layer maps these onto SCIM status codes: 404 and 409.
var (
	ErrNotFound          = errors.New("user not found")
	ErrDuplicateUserName = errors.New("userName already exists")
	// ErrDuplicateTenantName is the admin CLI's equivalent: two customers
	// sharing a display name, even by casing alone, is a support incident
	// waiting to happen.
	ErrDuplicateTenantName = errors.New("tenant name already exists")

	ErrGroupNotFound      = errors.New("group not found")
	ErrDuplicateGroupName = errors.New("displayName already exists")
	// ErrInvalidMember is a members reference to a user id that doesn't
	// exist, or that belongs to another tenant — rejected rather than
	// silently dropped, the same fail-closed reasoning every other
	// cross-tenant check in this codebase uses.
	ErrInvalidMember = errors.New("invalid member reference")
)

type Store struct {
	pool         *pgxpool.Pool
	changeEvents bool
}

type Option func(*Store)

// WithChangeEvents queues an outbound webhook delivery for every mutation, in
// the mutation's own transaction. It is off unless a dispatcher is configured
// to drain the queue, since otherwise the table would only ever grow.
func WithChangeEvents() Option {
	return func(s *Store) { s.changeEvents = true }
}

// New pings the database so a bad DATABASE_URL fails at startup, not on the
// first request.
func New(ctx context.Context, databaseURL string, opts ...Option) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &Store{pool: pool}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

// DSNFromEnv prefers DATABASE_URL and otherwise assembles one from the
// POSTGRES_* variables docker-compose.yml already uses.
func DSNFromEnv() (string, error) {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}

	user, pass, name := os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_DB")
	if user == "" || pass == "" || name == "" {
		return "", errors.New("set DATABASE_URL, or POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB")
	}

	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}

	// url.UserPassword percent-encodes, so reserved characters in the password
	// can't corrupt the DSN.
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     net.JoinHostPort("localhost", port),
		Path:     name,
		RawQuery: "sslmode=disable",
	}
	return u.String(), nil
}
