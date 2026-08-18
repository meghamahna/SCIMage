package webhook

import (
	"net/url"
	"strings"
	"time"
)

// Status is a read-only, secret-free snapshot of the webhook configuration for
// display in the admin console. It never carries the signing secret, and the
// endpoint is reduced to scheme://host/path so a capability token in the URL's
// query string can't leak into a page — the same redaction the dispatcher
// applies to its startup log.
type Status struct {
	Enabled       bool   // a valid endpoint is configured, so the dispatcher runs
	Endpoint      string // scheme://host/path, query stripped; empty when disabled
	Plaintext     bool   // endpoint is http:// (allowed only with SCIM_WEBHOOK_ALLOW_HTTP)
	MaxAttempts   int
	RetentionDays int    // 0 means delivered rows are kept indefinitely (sweep off)
	Problem       string // set only if a URL is configured but the config is invalid
}

// StatusFromEnv reads the same environment ConfigFromEnv does and reduces it to
// a display-safe Status. The secret is read for validation but never returned.
//
// A running server has already passed ConfigFromEnv at startup, so in practice
// this reports either Enabled or disabled; Problem is defensive, for the case a
// setting changed under a live process.
func StatusFromEnv() Status {
	cfg, enabled, err := ConfigFromEnv()
	if err != nil {
		return Status{Problem: err.Error()}
	}
	if !enabled {
		return Status{}
	}
	return Status{
		Enabled:       true,
		Endpoint:      redactEndpoint(cfg.URL),
		Plaintext:     strings.HasPrefix(cfg.URL, "http://"),
		MaxAttempts:   cfg.MaxAttempts,
		RetentionDays: int(cfg.Retention / (24 * time.Hour)),
	}
}

// redactEndpoint keeps only scheme, host, and path — never the query, which can
// carry a token. Matches the dispatcher's startup-log redaction.
func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.Path
}
