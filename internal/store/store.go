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
