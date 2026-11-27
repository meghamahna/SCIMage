package scim

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// sentinel stands in for a password a client sends; it must never reach a log
// line.
const sentinel = "fake-0001-must-not-appear"

// logChain wires the request logger around auth the way Routes does, with a
// handler that reads the whole body and records what it got.
func logChain(t *testing.T, got *[]byte) http.Handler {
	t.Helper()
	t.Setenv("SCIM_LOG_REQUESTS", "1")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		*got = b
		w.WriteHeader(http.StatusOK)
	})
	return logRequests(requireToken(fakeTokenStore{tok: validFakeToken()})(captureBody(next)))
}

func sendLogged(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/scim/v2/"+fakeTenantID+"/Users", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+fakeToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// oneRequestRecord returns the single "request" log entry.
func oneRequestRecord(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()

	recs := logRecords(t, logs, "request")
	if len(recs) != 1 {
		t.Fatalf("got %d request lines, want 1", len(recs))
	}
	return recs[0]
}

func TestRequestLogRedactsSecrets(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
	}{
		{"top-level password", map[string]any{"userName": "ada@example.com", "password": sentinel}},
		{"mixed-case key", map[string]any{"userName": "ada@example.com", "PassWord": sentinel}},
		{"secret-like key at depth", map[string]any{"userName": "ada@example.com",
			"urn:example:ext": map[string]any{"apiToken": sentinel}}},
		{"patch op with a password path", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace", "path": "password", "value": sentinel}}}},
		{"patch op with mixed-case keys", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace", "Path": "PASSWORD", "Value": sentinel}}}},
		{"patch op with a password in its value", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace", "value": map[string]any{"password": sentinel}}}}},
		{"patch op with a URN password path", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace",
				"path": "urn:ietf:params:scim:schemas:core:2.0:User:password", "value": sentinel}}}},
		{"patch op with a secret path and another sibling", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace", "path": "password", "extra": sentinel}}}},
		{"patch op with a non-string path", map[string]any{"userName": "ada@example.com",
			"Operations": []any{map[string]any{"op": "replace", "path": []any{"password"}, "value": sentinel}}}},
		// encoding/json matches keys under Unicode simple folding, so U+017F
		// (long s) reaches a password field as readily as "s" does.
		{"key with a long s", map[string]any{"userName": "ada@example.com", "pa\u017f\u017fword": sentinel}},
		{"key with a separator", map[string]any{"userName": "ada@example.com", "pass_word": sentinel}},
		{"api key", map[string]any{"userName": "ada@example.com", "apiKey": sentinel}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			var got []byte

			sendLogged(t, logChain(t, &got), mustJSON(t, tc.body))

			body, _ := oneRequestRecord(t, logs)["body"].(string)
			if !strings.Contains(body, "ada@example.com") || !strings.Contains(body, "[redacted]") {
				t.Errorf("body = %q, want it logged with the secret redacted", body)
			}
			for _, s := range []string{sentinel, fakePlaintext} {
				if strings.Contains(logs.String(), s) {
					t.Errorf("the log carries %q: %s", s, logs.String())
				}
			}
		})
	}
}

// Ordinary attributes must reach the log intact, or the log stops being useful
// for diagnosing a client.
func TestRequestLogKeepsOrdinaryValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
	}{
		// A filter inside a path is data, so a secret-like word there must not
		// hide the operation's value.
		{"patch op with a filtered path", map[string]any{"Operations": []any{map[string]any{
			"op": "replace", "path": `emails[type eq "token"].value`, "value": "ada@example.com"}}}},
		{"enterprise user", map[string]any{
			"schemas": []any{"urn:ietf:params:scim:schemas:core:2.0:User",
				"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"},
			"userName":    "ada@example.com",
			"displayName": "Ada Example",
			"externalId":  "ext-0001",
			"emails":      []any{map[string]any{"value": "ada@example.com", "type": "work", "primary": true}},
			"groups":      []any{map[string]any{"value": "group-0001", "display": "Engineering"}},
			"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User": map[string]any{
				"department": "Engineering",
				"manager":    map[string]any{"value": "mgr-0001"},
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			var got []byte

			sendLogged(t, logChain(t, &got), mustJSON(t, tc.body))

			logged, _ := oneRequestRecord(t, logs)["body"].(string)
			if !strings.Contains(logged, "ada@example.com") || strings.Contains(logged, redacted) {
				t.Errorf("body = %q, want it logged with nothing redacted", logged)
			}
		})
	}
}

// Goes through Routes, so the test covers where the logger and auth sit in the
// chain.
func TestRequestLogSkipsUnauthenticatedDetail(t *testing.T) {
	t.Setenv("SCIM_LOG_REQUESTS", "1")
	logs := captureLogs(t)
	routes := NewHandler(nil, nil, fakeTokenStore{tok: validFakeToken()}, nil).Routes()

	body := mustJSON(t, map[string]any{"userName": "ada@example.com", "password": sentinel})
	r := httptest.NewRequest(http.MethodPost,
		"/scim/v2/"+fakeTenantID+`/Users?filter=userName+eq+"filter-value-0001"`, strings.NewReader(body))
	rr := httptest.NewRecorder()
	routes.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	rec := oneRequestRecord(t, logs)
	for _, k := range []string{"body", "body_bytes", "query", "user_agent"} {
		if _, ok := rec[k]; ok {
			t.Errorf("unauthenticated entry has %q: %v", k, rec)
		}
	}
	for _, s := range []string{"ada@example.com", sentinel, "filter-value-0001"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("the log carries %q from an unauthenticated request", s)
		}
	}
}

func TestRequestLogPassesFullBody(t *testing.T) {
	logs := captureLogs(t)
	var got []byte
	body := mustJSON(t, map[string]any{"userName": "ada@example.com",
		"displayName": strings.Repeat("a", 4*maxLoggedBody)})

	rr := sendLogged(t, logChain(t, &got), body)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if string(got) != body {
		t.Errorf("handler got %d bytes, want the full %d", len(got), len(body))
	}
	if logged, _ := oneRequestRecord(t, logs)["body"].(string); logged == "" || len(logged) > maxLoggedBody {
		t.Errorf("logged body is %d bytes, want 1 to %d", len(logged), maxLoggedBody)
	}
}

func TestRequestLogNeverLogsUnparsedBody(t *testing.T) {
	logs := captureLogs(t)
	var got []byte
	body := `{"userName": "ada@example.com", "password": "` + sentinel + `"`

	sendLogged(t, logChain(t, &got), body)

	if string(got) != body {
		t.Errorf("handler got %q, want the body unchanged", got)
	}
	rec := oneRequestRecord(t, logs)
	if n, _ := rec["body_bytes"].(float64); int(n) != len(body) {
		t.Errorf("body_bytes = %v, want %d", rec["body_bytes"], len(body))
	}
	if strings.Contains(logs.String(), sentinel) || strings.Contains(logs.String(), "ada@example.com") {
		t.Errorf("the log carries the raw body: %s", logs.String())
	}
}

// Goes through Routes, so the handler's own size limit is in play.
func TestRequestLogRecordsLargeBodiesBySizeOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		size      int
		overLimit bool
	}{
		{"too large to decode", maxDecodedBody + 1, false},
		{"over the request limit", maxBodyBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCIM_LOG_REQUESTS", "1")
			discardLogs(t)
			logs := captureLogs(t)
			body := mustJSON(t, map[string]any{"userName": "ada@example.com", "password": sentinel,
				"displayName": strings.Repeat("a", tc.size)})

			request(t, fakeHandler(&fakeStore{}), http.MethodPost, "/Users", body)

			rec := oneRequestRecord(t, logs)
			if over, _ := rec["body_over_limit"].(bool); over != tc.overLimit {
				t.Errorf("body_over_limit = %v, want %v", rec["body_over_limit"], tc.overLimit)
			}
			if n, _ := rec["body_bytes"].(float64); !tc.overLimit && int(n) != len(body) {
				t.Errorf("body_bytes = %v, want %d", rec["body_bytes"], len(body))
			}
			for _, s := range []string{"ada@example.com", sentinel, fakePlaintext} {
				if strings.Contains(logs.String(), s) {
					t.Errorf("the log carries %q", s)
				}
			}
		})
	}
}

func TestRequestLogCapsUnauthenticatedMethodAndPath(t *testing.T) {
	t.Setenv("SCIM_LOG_REQUESTS", "1")
	logs := captureLogs(t)
	routes := NewHandler(nil, nil, fakeTokenStore{tok: validFakeToken()}, nil).Routes()

	// Two-byte runes, so the cut lands inside one.
	r := httptest.NewRequest(http.MethodGet, "/scim/v2/"+fakeTenantID+"/"+strings.Repeat("%C3%A9", 300), nil)
	r.Method = strings.Repeat("M", 1<<10)
	routes.ServeHTTP(httptest.NewRecorder(), r)

	rec := oneRequestRecord(t, logs)
	for _, tc := range []struct {
		key  string
		full string
		max  int
	}{
		{"method", r.Method, maxLoggedMethod},
		{"path", r.URL.Path, maxLoggedPath},
	} {
		logged, _ := rec[tc.key].(string)
		if len(logged) > tc.max || !utf8.ValidString(logged) {
			t.Errorf("%s is %d bytes (valid UTF-8: %v), want at most %d",
				tc.key, len(logged), utf8.ValidString(logged), tc.max)
		}
		if n, _ := rec[tc.key+"_bytes"].(float64); int(n) != len(tc.full) {
			t.Errorf("%s_bytes = %v, want %d", tc.key, rec[tc.key+"_bytes"], len(tc.full))
		}
	}
}
