package scim

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// maxLoggedBody bounds what one entry records.
const maxLoggedBody = 2048

// logRequests records what a client actually sent — method, path, query, status
// and body — which is how an identity provider's real behaviour gets diagnosed
// rather than inferred from status codes.
//
// Off unless SCIM_LOG_REQUESTS=1, because bodies carry user attributes.
// Authorization is never recorded.
func logRequests(next http.Handler) http.Handler {
	if os.Getenv("SCIM_LOG_REQUESTS") != "1" {
		return next
	}

	slog.Warn("request logging is on; entries include user attributes",
		"env", "SCIM_LOG_REQUESTS=1")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		if r.Body != nil {
			raw, err := io.ReadAll(io.LimitReader(r.Body, maxLoggedBody))
			if err == nil {
				body = strings.TrimSpace(string(raw))
				// Hand the handler a fresh reader, since this one is spent.
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"user_agent", r.UserAgent(),
		}
		if q := r.URL.RawQuery; q != "" {
			attrs = append(attrs, "query", q)
		}
		if body != "" {
			attrs = append(attrs, "body", body)
		}

		slog.Info("request", attrs...)
	})
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
