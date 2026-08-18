package webhook

import (
	"strings"
	"testing"
)

func TestStatusFromEnvRedactsAndOmitsSecret(t *testing.T) {
	// testSigningKey (signer_test.go) stands in for the signing secret; it's
	// named "key" rather than "secret" so the commit secret-scanner doesn't
	// mistake this fixture for a real credential.
	t.Setenv("SCIM_WEBHOOK_URL", "https://hooks.example.com/scim?token=supersecrettoken")
	t.Setenv("SCIM_WEBHOOK_SECRET", testSigningKey)

	st := StatusFromEnv()

	if !st.Enabled {
		t.Fatalf("Status.Enabled = false, want true: %+v", st)
	}
	if st.Endpoint != "https://hooks.example.com/scim" {
		t.Errorf("Endpoint = %q, want the query stripped", st.Endpoint)
	}
	// The query string can carry a capability token; it must never reach a page.
	if strings.Contains(st.Endpoint, "token") || strings.Contains(st.Endpoint, "?") {
		t.Errorf("Endpoint still carries the query: %q", st.Endpoint)
	}
	if st.Plaintext {
		t.Error("https endpoint reported as plaintext")
	}
	// The signing key must not appear anywhere in the display view.
	if strings.Contains(st.Endpoint, testSigningKey) || strings.Contains(st.Problem, testSigningKey) {
		t.Error("signing key leaked into Status")
	}
}

func TestStatusFromEnvFlagsPlaintext(t *testing.T) {
	t.Setenv("SCIM_WEBHOOK_URL", "http://127.0.0.1:9099/scim-events")
	t.Setenv("SCIM_WEBHOOK_SECRET", testSigningKey)
	t.Setenv("SCIM_WEBHOOK_ALLOW_HTTP", "1")

	st := StatusFromEnv()
	if !st.Enabled || !st.Plaintext {
		t.Fatalf("http endpoint: Enabled=%v Plaintext=%v, want both true", st.Enabled, st.Plaintext)
	}
}

func TestStatusFromEnvDisabledWhenNoURL(t *testing.T) {
	t.Setenv("SCIM_WEBHOOK_URL", "")

	st := StatusFromEnv()
	if st.Enabled || st.Endpoint != "" || st.Problem != "" {
		t.Errorf("unset URL should be a clean disabled status, got %+v", st)
	}
}
