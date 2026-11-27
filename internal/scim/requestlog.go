package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"unicode"
)

const (
	// maxLoggedBody bounds what one entry records.
	maxLoggedBody = 2048

	// maxDecodedBody bounds the body the logger decodes to redact it: decoding
	// JSON into `any` allocates dozens of times the input size.
	maxDecodedBody = 64 << 10

	// maxLoggedPath and maxLoggedMethod bound the path and method of a request
	// auth rejects. Both come from an unauthenticated caller, and the server
	// accepts a method token of about 1 MiB, so otherwise one request could
	// write an arbitrarily long line.
	maxLoggedPath   = 256
	maxLoggedMethod = 16
)

// redacted replaces the value of any secret-like attribute in a logged body;
// notLogged stands in for a body that can't be redacted or is too large.
const (
	redacted  = "[redacted]"
	notLogged = "[not logged]"
)

// loggedRequest carries what captureBody, inside auth, hands back to
// logRequests, outside it.
type loggedRequest struct {
	authenticated bool
	body          cappedBuffer
}

type loggedRequestKey struct{}

// logRequests records what a client actually sent: method, path, query, status
// and body, which is how an identity provider's real behaviour gets diagnosed
// rather than inferred from status codes.
//
// Off unless SCIM_LOG_REQUESTS=1, because bodies carry user attributes.
// Authorization is never recorded. A request auth rejects gets method, path
// and status only, since nothing it sent has been vouched for.
func logRequests(next http.Handler) http.Handler {
	if os.Getenv("SCIM_LOG_REQUESTS") != "1" {
		return next
	}

	slog.Warn("request logging is on; entries include user attributes",
		"env", "SCIM_LOG_REQUESTS=1")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := &loggedRequest{}
		r = r.WithContext(context.WithValue(r.Context(), loggedRequestKey{}, entry))

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		var attrs []any
		if entry.authenticated {
			attrs = append(attrs, "method", r.Method, "path", r.URL.Path)
		} else {
			attrs = append(attrs, cutAttr("method", r.Method, maxLoggedMethod)...)
			attrs = append(attrs, cutAttr("path", r.URL.Path, maxLoggedPath)...)
		}
		attrs = append(attrs, "status", rec.status)
		if entry.authenticated {
			attrs = append(attrs, "user_agent", r.UserAgent())
			if q := r.URL.RawQuery; q != "" {
				attrs = append(attrs, "query", q)
			}
			attrs = append(attrs, entry.body.logAttrs()...)
		}

		slog.Info("request", attrs...)
	})
}

// cutAttr returns key and s, with s cut to at most limit bytes of valid UTF-8
// and key_bytes giving the full length when it was cut.
func cutAttr(key, s string, limit int) []any {
	if len(s) <= limit {
		return []any{key, s}
	}
	return []any{key, strings.ToValidUTF8(s[:limit], ""), key + "_bytes", len(s)}
}

// captureBody sits inside requireToken, so only an authenticated request is
// marked for full logging. It copies the body as the handler reads it rather
// than reading ahead, so the handler gets the stream untouched and the count
// stops where the handler's own size limit stops reading.
func captureBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entry, ok := r.Context().Value(loggedRequestKey{}).(*loggedRequest); ok {
			entry.authenticated = true
			if r.Body != nil {
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.TeeReader(r.Body, &entry.body), r.Body}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// cappedBuffer keeps at most maxDecodedBody, the most the logger will decode,
// and counts everything written.
type cappedBuffer struct {
	buf bytes.Buffer
	n   int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.n += len(p)
	if room := maxDecodedBody - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// logAttrs returns the body as redacted JSON, cut to maxLoggedBody. A body
// that doesn't parse is logged by size only: redaction can't see inside it, so
// its raw bytes could carry a password. So is one too large to decode.
func (c *cappedBuffer) logAttrs() []any {
	switch {
	case c.n == 0:
		return nil
	case c.n > maxBodyBytes:
		// The handler stopped reading at its limit, so the full size is unknown.
		return []any{"body", notLogged, "body_over_limit", true}
	case c.n > maxDecodedBody:
		return []any{"body", notLogged, "body_bytes", c.n}
	}

	var v any
	if json.Unmarshal(c.buf.Bytes(), &v) != nil {
		return []any{"body", notLogged, "body_bytes", c.n}
	}

	out, err := json.Marshal(redact(v))
	if err != nil {
		return []any{"body", notLogged, "body_bytes", c.n, "error", err}
	}

	s := string(out)
	if len(s) > maxLoggedBody {
		s = strings.ToValidUTF8(s[:maxLoggedBody], "")
	}
	return []any{"body", s}
}

// redact replaces secret-like values in a decoded JSON body, in place. A PATCH
// operation names its target in path and carries the new value apart from it,
// so a secret path redacts every sibling but op and path. A path that isn't a
// string can't be read, so it redacts every sibling but op.
func redact(v any) any {
	switch v := v.(type) {
	case map[string]any:
		secretPath, opaquePath := false, false
		for k, val := range v {
			if !strings.EqualFold(k, "path") {
				continue
			}
			switch p := val.(type) {
			case nil:
			case string:
				secretPath = secretPath || isSecretPath(p)
			default:
				opaquePath = true
			}
		}
		for k, val := range v {
			isOp, isPath := strings.EqualFold(k, "op"), strings.EqualFold(k, "path")
			switch {
			case isSecretName(k), opaquePath && !isOp, secretPath && !isOp && !isPath:
				v[k] = redacted
			default:
				v[k] = redact(val)
			}
		}
	case []any:
		for i, val := range v {
			v[i] = redact(val)
		}
	}
	return v
}

// secretWords are matched against a name after foldName.
var secretWords = []string{
	"password", "passwd", "pwd", "passphrase", "secret", "token", "apikey", "privatekey", "credential",
}

func isSecretName(name string) bool {
	folded := foldName(name)
	for _, w := range secretWords {
		if strings.Contains(folded, w) {
			return true
		}
	}
	return false
}

// foldName maps each rune to the lowest member of its simple case-folding
// orbit, lowercased, and drops everything but letters and digits. encoding/json
// matches keys under the same folding, so "paſſword" (U+017F) reaches a
// password field; folding this way also turns "Pass_Word" and "api-key" into
// "password" and "apikey".
func foldName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			lowest = min(lowest, f)
		}
		b.WriteRune(unicode.ToLower(lowest))
	}
	return b.String()
}

// isSecretPath reports whether a PATCH path names a secret attribute, in plain
// or URN form. Text inside a filter is data, so `emails[type eq "token"].value`
// doesn't count. A "]" quoted inside a filter only ends the skip early, which
// errs toward redacting.
func isSecretPath(path string) bool {
	var b strings.Builder
	inFilter := false
	for _, r := range path {
		switch {
		case r == '[':
			inFilter = true
		case r == ']':
			inFilter = false
		case !inFilter:
			b.WriteRune(r)
		}
	}
	return isSecretName(b.String())
}

// statusRecorder captures the status for the log entry. WriteHeader may go
// uncalled on a 200, which is why the zero value starts at StatusOK.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
