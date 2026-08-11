package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readEntries parses the JSON lines written to the day's file.
func readEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()

	name := filepath.Join(dir, "scimage-"+time.Now().UTC().Format(time.DateOnly)+".log")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestSetupWritesDatedJSONFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	t.Setenv("LOG_DIR", dir)
	t.Setenv("LOG_LEVEL", "")

	closer, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer closer.Close()

	slog.Info("hello", "answer", 42)

	entries := readEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	got := entries[0]
	if got["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", got["msg"])
	}
	if got["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got["level"])
	}
	if got["answer"] != float64(42) {
		t.Errorf("answer = %v, want 42 — structured fields should survive", got["answer"])
	}

	// The timestamp is what makes a log usable after the fact.
	ts, ok := got["time"].(string)
	if !ok {
		t.Fatalf("time = %v, want an RFC 3339 string", got["time"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("time %q is not RFC 3339: %v", ts, err)
	}
}

// A container collects stdout and has nothing to rotate a file, so an empty
// LOG_DIR has to mean "stdout only" rather than "use the default".
func TestSetupWithEmptyLogDirWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOG_DIR", "")

	closer, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer closer.Close()

	slog.Info("hello")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("wrote %d files, want none", len(entries))
	}
}

func TestSetupRejectsAnUnknownLevel(t *testing.T) {
	t.Setenv("LOG_DIR", "")
	t.Setenv("LOG_LEVEL", "chatty")

	if _, err := Setup(); err == nil {
		t.Error("expected an error for an unknown LOG_LEVEL")
	}
}

// The directory and file can hold user attributes when request logging is on.
func TestLogFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	t.Setenv("LOG_DIR", dir)
	t.Setenv("LOG_LEVEL", "")

	closer, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer closer.Close()

	slog.Info("hello")

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 700", perm)
	}

	name := filepath.Join(dir, "scimage-"+time.Now().UTC().Format(time.DateOnly)+".log")
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}
