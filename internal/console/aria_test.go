package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// renderNarrative takes untrusted LLM output. It must never let that output
// inject live HTML, while still upgrading the model's **label:** headers to
// <strong>.
func TestRenderNarrativeEscapesButBolds(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantSub    []string
		wantNotSub []string
	}{
		{
			name:    "bold label becomes strong",
			in:      "**Bulk deactivation:** caller okta ran 6 deletes.",
			wantSub: []string{"<strong>Bulk deactivation:</strong>", "caller okta ran 6 deletes."},
		},
		{
			name:       "html in the model output is escaped, not rendered",
			in:         "watch <script>alert(1)</script> and </section>",
			wantSub:    []string{"&lt;script&gt;", "&lt;/section&gt;"},
			wantNotSub: []string{"<script>", "</section>"},
		},
		{
			name:       "markup inside a bold run is escaped, the strong tag is ours",
			in:         "**<img src=x onerror=alert(1)>:**",
			wantSub:    []string{"<strong>", "&lt;img", "onerror=alert(1)&gt;"},
			wantNotSub: []string{"<img", "<img src=x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(renderNarrative(tc.in))
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q\n got: %s", sub, got)
				}
			}
			for _, sub := range tc.wantNotSub {
				if strings.Contains(got, sub) {
					t.Errorf("output contains unescaped %q\n got: %s", sub, got)
				}
			}
		})
	}
}

// computeDelta must never invent a trend: two empty windows yield no badge.
func TestComputeDelta(t *testing.T) {
	if d := computeDelta(0, 0); d != nil {
		t.Errorf("both-empty delta = %+v, want nil (no fabricated trend)", d)
	}
	if d := computeDelta(3, 0); d == nil || d.Class != "up" {
		t.Errorf("first-activity delta = %+v, want an up badge", d)
	}
	if d := computeDelta(10, 5); d == nil || d.Class != "up" || d.Label != "+100% ↑" {
		t.Errorf("doubled delta = %+v, want up +100%%", d)
	}
	if d := computeDelta(5, 10); d == nil || d.Class != "down" || d.Label != "-50% ↓" {
		t.Errorf("halved delta = %+v, want down -50%%", d)
	}
	if d := computeDelta(7, 7); d == nil || d.Class != "flat" {
		t.Errorf("unchanged delta = %+v, want flat", d)
	}
}

func TestNarrativeCache(t *testing.T) {
	c := newNarrativeCache()

	if _, ok := c.get("k"); ok {
		t.Fatal("empty cache returned a hit")
	}

	c.put("k", "first briefing")
	e, ok := c.get("k")
	if !ok || e.text != "first briefing" {
		t.Fatalf("get after put = (%q, %v), want (first briefing, true)", e.text, ok)
	}
	if e.generatedAt.IsZero() {
		t.Error("cached entry has a zero generatedAt")
	}

	// A refresh overwrites in place; keys don't collide across tenant+window.
	c.put("k", "second briefing")
	if e, _ := c.get("k"); e.text != "second briefing" {
		t.Errorf("get after refresh = %q, want second briefing", e.text)
	}
	if _, ok := c.get("other"); ok {
		t.Error("unrelated key returned a hit")
	}
}

// narrativeKey must not let a tenant id and a window run together into the same
// string as a different pair — the separator is what guarantees that.
func TestNarrativeKeyNoCollision(t *testing.T) {
	if narrativeKey("ab", "c") == narrativeKey("a", "bc") {
		t.Error("distinct tenant+window pairs produced the same cache key")
	}
}

// ariaWindowStore fakes the audit reads ARIA makes over one window of inWindow
// rows: the list clamps to store.MaxPageSize, as the real store does, and the
// count reads the same rows uncapped. Every other Store method stays nil, so a
// stray call fails loudly.
type ariaWindowStore struct {
	Store
	inWindow int
	countErr error
}

func (f ariaWindowStore) ListTenants(context.Context) ([]store.Tenant, error) { return nil, nil }

func (f ariaWindowStore) ListAuditEntriesSince(_ context.Context, _ string, since time.Time) ([]store.AuditEntry, error) {
	entries := make([]store.AuditEntry, min(f.inWindow, store.MaxPageSize))
	for i := range entries {
		entries[i] = store.AuditEntry{At: since.Add(time.Minute), Action: store.ActionCreate, Result: store.ResultSuccess}
	}
	return entries, nil
}

func (f ariaWindowStore) CountAuditEntriesSince(context.Context, string, time.Time) (int, error) {
	return f.inWindow, f.countErr
}

func (f ariaWindowStore) AuditWindowStats(context.Context, string, time.Time, time.Time) (int, int, error) {
	return f.inWindow, 1, nil
}

// The page only reviews the windows it offers, so a crafted since can't make
// each page load count the whole audit log.
func TestARIAWindowLimitedToOffered(t *testing.T) {
	tests := []struct {
		since     string
		wantSince string
		want      time.Duration
	}{
		{"1h", "1h", time.Hour},
		{"24h", "24h", 24 * time.Hour},
		{"7d", "7d", 7 * 24 * time.Hour},
		{"30d", "30d", 30 * 24 * time.Hour},
		{"90d", "24h", 24 * time.Hour},
		{"36500d", "24h", 24 * time.Hour},
		{"2h", "24h", 24 * time.Hour},
		{"bogus", "24h", 24 * time.Hour},
	}
	srv, err := NewServer(ariaWindowStore{}, "test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.since, func(t *testing.T) {
			in, err := srv.ariaInputs(httptest.NewRequest("GET", "/console/aria?since="+tc.since, nil))
			if err != nil {
				t.Fatalf("ariaInputs: %v", err)
			}
			if got := in.report.Now.Sub(in.report.Since); in.since != tc.wantSince || got != tc.want {
				t.Errorf("since %q reviewed %q over %v, want %q over %v", tc.since, in.since, got, tc.wantSince, tc.want)
			}
		})
	}
}

// The ARIA page must say when it reviewed only the newest slice of a window,
// and how big the window really was when it can count it.
func TestARIAPagePartialReview(t *testing.T) {
	tests := []struct {
		name     string
		inWindow int
		countErr error
		want     string // "" means no partial-review banner
	}{
		{"under the cap", 150, nil, ""},
		{"exactly the cap", store.MaxPageSize, nil, ""},
		{"over the cap", 1234, nil, "ARIA reviewed the newest 200 of 1234 entries in this window."},
		{"count fails", 1234, errors.New("db down"), "ARIA reviewed the newest 200 entries in this window, the most it reads at once."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewServer(ariaWindowStore{inWindow: tc.inWindow, countErr: tc.countErr}, "test")
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			rec := httptest.NewRecorder()
			srv.handleARIA(rec, httptest.NewRequest("GET", "/console/aria?since=24h", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if tc.want == "" {
				if strings.Contains(body, "Partial review") {
					t.Error("a fully reviewed window should show no partial-review banner")
				}
				return
			}
			if !strings.Contains(body, "Partial review") || !strings.Contains(body, tc.want) {
				t.Errorf("banner missing %q", tc.want)
			}
		})
	}
}
