package store

import (
	"testing"
	"time"
)

// Pure env-parsing helpers, no database needed — these run even without
// `make test`'s Postgres.

func TestEnvMaxConns(t *testing.T) {
	t.Run("unset leaves pgx's default alone", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONNS", "")

		if n := envMaxConns(); n != 0 {
			t.Errorf("envMaxConns() = %d, want 0 (unset)", n)
		}
	})

	t.Run("invalid leaves pgx's default alone", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONNS", "not-a-number")

		if n := envMaxConns(); n != 0 {
			t.Errorf("envMaxConns() = %d, want 0 (invalid)", n)
		}
	})

	t.Run("zero or negative leaves pgx's default alone", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONNS", "-5")

		if n := envMaxConns(); n != 0 {
			t.Errorf("envMaxConns() = %d, want 0 (non-positive)", n)
		}
	})

	t.Run("reads a configured value", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONNS", "10")

		if n := envMaxConns(); n != 10 {
			t.Errorf("envMaxConns() = %d, want 10", n)
		}
	})
}

func TestEnvMaxConnLifetime(t *testing.T) {
	t.Run("unset leaves pgx's default alone", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONN_LIFETIME", "")

		if d := envMaxConnLifetime(); d != 0 {
			t.Errorf("envMaxConnLifetime() = %v, want 0 (unset)", d)
		}
	})

	t.Run("invalid leaves pgx's default alone", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONN_LIFETIME", "not-a-duration")

		if d := envMaxConnLifetime(); d != 0 {
			t.Errorf("envMaxConnLifetime() = %v, want 0 (invalid)", d)
		}
	})

	t.Run("reads a configured value", func(t *testing.T) {
		t.Setenv("DATABASE_MAX_CONN_LIFETIME", "30m")

		if d := envMaxConnLifetime(); d != 30*time.Minute {
			t.Errorf("envMaxConnLifetime() = %v, want 30m", d)
		}
	})
}
