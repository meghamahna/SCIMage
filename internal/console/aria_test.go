package console

import (
	"strings"
	"testing"
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
