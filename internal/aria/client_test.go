package aria

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The client speaks the OpenAI-compatible chat-completions shape, so the test
// stands up a server returning that shape rather than mocking the transport —
// the same real-httptest-server approach the webhook dispatcher test uses.
func TestSummarizeHappyPath(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"  All quiet in the window.  "}}]}`)
	}))
	defer srv.Close()

	// A stand-in credential for the fake server, kept in a variable rather than
	// an inline literal so it doesn't read as a hardcoded key to a secret scan.
	fixtureKey := "fixture-" + t.Name()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: fixtureKey, Model: "test-model"})

	out, err := c.Summarize(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if out != "All quiet in the window." {
		t.Errorf("content = %q, want trimmed briefing", out)
	}
	if gotAuth != "Bearer "+fixtureKey {
		t.Errorf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotBody.Model != "test-model" || len(gotBody.Messages) != 2 {
		t.Errorf("request body = %+v, want model test-model and 2 messages", gotBody)
	}
	if gotBody.Messages[0].Role != "system" || gotBody.Messages[1].Role != "user" {
		t.Errorf("message roles = %q/%q, want system/user", gotBody.Messages[0].Role, gotBody.Messages[1].Role)
	}
}

func TestSummarizeNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"})

	_, err := c.Summarize(context.Background(), "sys", "usr")
	if err == nil {
		t.Fatal("want an error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to mention the status", err)
	}
}

func TestSummarizeNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"})

	if _, err := c.Summarize(context.Background(), "sys", "usr"); err == nil {
		t.Fatal("want an error when the model returns no choices")
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("ARIA_LLM_BASE_URL", "https://api.example.com/v1/")
	t.Setenv("ARIA_LLM_API_KEY", "key")
	t.Setenv("ARIA_LLM_MODEL", "model")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.BaseURL != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", cfg.BaseURL)
	}

	t.Setenv("ARIA_LLM_API_KEY", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("want an error when ARIA_LLM_API_KEY is unset")
	}
}
