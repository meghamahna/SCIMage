// Package audit writes one JSON line per mutating SCIM call: who did what to
// which user, when, and what the row looked like either side of the change.
//
// JSON lines rather than a table because SAGE reads this stream, and because a
// line-oriented file is trivial to ship to any log pipeline.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Actions recorded. These are the mutating calls; reads are not audited.
const (
	ActionCreate     = "create"
	ActionReplace    = "replace"
	ActionDeactivate = "deactivate"
)

// Results distinguish a change that happened from one that was refused, so a
// burst of denials is visible rather than silently absent.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultError   = "error"
)

type Entry struct {
	Time     time.Time `json:"time"`
	Action   string    `json:"action"`
	Actor    Actor     `json:"actor"`
	TargetID string    `json:"targetId,omitempty"`
	Result   string    `json:"result"`
	Detail   string    `json:"detail,omitempty"`
	Before   *User     `json:"before,omitempty"`
	After    *User     `json:"after,omitempty"`
}

// Actor identifies the caller. Token is a short fingerprint, never the token
// itself: enough to tell two callers apart in the log without putting a
// credential in it.
type Actor struct {
	Token string `json:"token"`
	IP    string `json:"ip,omitempty"`
}

// User is the audited projection of a row. It deliberately mirrors the columns
// rather than reusing store.User, so a field added to the store isn't logged by
// accident — the audit stream is written to disk and read by an LLM in Phase 8.
type User struct {
	ID         string    `json:"id"`
	UserName   string    `json:"userName"`
	GivenName  string    `json:"givenName,omitempty"`
	FamilyName string    `json:"familyName,omitempty"`
	Email      string    `json:"email,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Logger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func New(w io.Writer) *Logger {
	return &Logger{enc: json.NewEncoder(w)}
}

// NewFromEnv appends to AUDIT_LOG_PATH, or writes to stderr when it is unset.
// The returned closer is a no-op for stderr.
func NewFromEnv() (*Logger, io.Closer, error) {
	path := os.Getenv("AUDIT_LOG_PATH")
	if path == "" {
		return New(os.Stderr), io.NopCloser(nil), nil
	}

	// 0600: the stream carries userName and email for every provisioned user.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open audit log %q: %w", path, err)
	}
	return New(f), f, nil
}

// Write emits one line. It never returns an error and never fails the request:
// the mutation is already durable in Postgres by this point, so refusing the
// response would tell the client a lie. A failed write is shouted to the
// standard log instead.
//
// That is a real gap, and the honest name for it is that Postgres and a file
// cannot be committed atomically. An audit table written in the same
// transaction as the mutation is the only way to close it.
func (l *Logger) Write(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.enc.Encode(e); err != nil {
		log.Printf("audit: FAILED to record %s on %q by %s: %v",
			e.Action, e.TargetID, e.Actor.Token, err)
	}
}
