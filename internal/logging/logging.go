// Package logging configures the process logger: structured JSON with RFC 3339
// timestamps, written to stdout and to a dated file.
//
// These are operational logs — startup, errors, and optionally the requests
// clients send. The audit trail is separate and lives in the audit_log table,
// because a mutation and its record have to commit together, which a file can
// never guarantee.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDir is where logs land unless LOG_DIR says otherwise. Setting LOG_DIR
// to an empty value writes to stdout alone, which is what a container wants —
// the runtime collects stdout, and a file inside a container grows unbounded
// with nothing to rotate it.
const DefaultDir = "logs"

// Setup configures the default slog logger and returns a closer for the file.
// Logs always go to stdout; the file is additional.
func Setup() (io.Closer, error) {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil && os.Getenv("LOG_LEVEL") != "" {
		return nil, fmt.Errorf("LOG_LEVEL %q is not one of debug, info, warn, error", os.Getenv("LOG_LEVEL"))
	}

	dir, ok := os.LookupEnv("LOG_DIR")
	if !ok {
		dir = DefaultDir
	}

	var (
		out    io.Writer = os.Stdout
		closer io.Closer = io.NopCloser(nil)
	)

	if strings.TrimSpace(dir) != "" {
		f, err := openLogFile(dir)
		if err != nil {
			return nil, err
		}
		out = io.MultiWriter(os.Stdout, f)
		closer = f
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})))
	return closer, nil
}

// openLogFile appends to one file per day. Dated files keep a long-running
// server from producing a single unbounded file without pulling in a rotation
// dependency; a deployment that wants size-based rotation should set LOG_DIR
// empty and let its platform handle stdout.
func openLogFile(dir string) (*os.File, error) {
	// 0700: request logging can record user attributes.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory %q: %w", dir, err)
	}

	name := filepath.Join(dir, "scimage-"+time.Now().UTC().Format(time.DateOnly)+".log")

	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", name, err)
	}
	return f, nil
}
