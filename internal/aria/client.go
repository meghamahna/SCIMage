package aria

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultTimeout   = 60 * time.Second
	defaultMaxTokens = 1024
	maxResponseBytes = 1 << 20 // 1 MiB is far more than a one-minute briefing needs
)

// Config points ARIA at an LLM. The endpoint is left to the operator so ARIA
// isn't bound to one provider: any OpenAI-compatible chat-completions API works
// — Anthropic's compatibility endpoint, OpenAI, OpenRouter, a local Ollama or
// vLLM, and so on.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// ConfigFromEnv reads ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY and ARIA_LLM_MODEL.
// The key comes from the environment only — never a flag, never a file, never
// logged — the same discipline the rest of the server holds for secrets.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("ARIA_LLM_BASE_URL")), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("ARIA_LLM_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("ARIA_LLM_MODEL")),
	}

	var missing []string
	if cfg.BaseURL == "" {
		missing = append(missing, "ARIA_LLM_BASE_URL")
	}
	if cfg.APIKey == "" {
		missing = append(missing, "ARIA_LLM_API_KEY")
	}
	if cfg.Model == "" {
		missing = append(missing, "ARIA_LLM_MODEL")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("aria: set %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// Client calls the configured chat-completions endpoint. The http.Client is a
// field rather than a global so a test can point it at an httptest server.
type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: defaultTimeout},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Summarize sends the advisory prompt and returns the model's text. It has no
// audit-log side effects: the returned string is printed for a human and is
// never fed back into the store or the auth path.
func (c *Client) Summarize(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: defaultMaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call llm: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("llm error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", errors.New("llm returned no content")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
