package aria

import (
	"strings"
	"testing"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// A fixed Monday used as the clock so business-hours and weekend logic is
// deterministic regardless of when the test runs. 2025-06-16 is a Monday.
var monday = time.Date(2025, 6, 16, 12, 0, 0, 0, time.UTC)

func entry(actor string, at time.Time, action, result string) store.AuditEntry {
	return store.AuditEntry{
		At:           at,
		Actor:        store.AuditRecord{ActorToken: actor, TenantID: "tenant_x"},
		ResourceType: store.ResourceUser,
		Action:       action,
		Result:       result,
		TargetID:     "user-1",
	}
}

func hasKind(r Report, k FlagKind) *Flag {
	for i := range r.Flags {
		if r.Flags[i].Kind == k {
			return &r.Flags[i]
		}
	}
	return nil
}

func TestDetectNoFindings(t *testing.T) {
	entries := []store.AuditEntry{
		entry("tok_a", monday.Add(-2*time.Hour), store.ActionCreate, store.ResultSuccess),
		entry("tok_a", monday.Add(-time.Hour), store.ActionReplace, store.ResultSuccess),
	}

	r := Detect(entries, "tenant_x", monday.Add(-24*time.Hour), monday, time.UTC)

	if r.HasFindings() {
		t.Fatalf("HasFindings = true, want false; flags: %+v", r.Flags)
	}
	if r.Total != 2 || r.Callers != 1 {
		t.Errorf("total/callers = %d/%d, want 2/1", r.Total, r.Callers)
	}
}

func TestDetectBulkDeactivation(t *testing.T) {
	base := monday
	var entries []store.AuditEntry
	for i := 0; i < 6; i++ {
		entries = append(entries, entry("tok_bulk", base.Add(time.Duration(i)*time.Minute), store.ActionDeactivate, store.ResultSuccess))
	}

	r := Detect(entries, "tenant_x", base.Add(-time.Hour), base.Add(time.Hour), time.UTC)

	f := hasKind(r, FlagBulkDeactivation)
	if f == nil {
		t.Fatalf("no bulk_deactivation flag; flags: %+v", r.Flags)
	}
	if f.Actor != "tok_bulk" || f.Count != 6 {
		t.Errorf("actor/count = %q/%d, want tok_bulk/6", f.Actor, f.Count)
	}
	if len(f.Examples) == 0 {
		t.Error("bulk flag carries no examples")
	}
}

// Deactivations spread out beyond the window must not cluster.
func TestDetectNoBulkWhenSpreadOut(t *testing.T) {
	var entries []store.AuditEntry
	for i := 0; i < 6; i++ {
		entries = append(entries, entry("tok_slow", monday.Add(time.Duration(i)*time.Hour), store.ActionDeactivate, store.ResultSuccess))
	}

	r := Detect(entries, "tenant_x", monday.Add(-time.Hour), monday.Add(12*time.Hour), time.UTC)

	if f := hasKind(r, FlagBulkDeactivation); f != nil {
		t.Errorf("spread-out deactivations flagged as bulk: %+v", f)
	}
}

func TestDetectDenialBurst(t *testing.T) {
	var entries []store.AuditEntry
	for i := 0; i < denialCount; i++ {
		entries = append(entries, entry("tok_denied", monday.Add(time.Duration(i)*time.Minute), store.ActionCreate, store.ResultDenied))
	}

	r := Detect(entries, "tenant_x", monday.Add(-time.Hour), monday.Add(time.Hour), time.UTC)

	f := hasKind(r, FlagDenialBurst)
	if f == nil {
		t.Fatalf("no denial_burst flag; flags: %+v", r.Flags)
	}
	if f.Count != denialCount {
		t.Errorf("count = %d, want %d", f.Count, denialCount)
	}
}

func TestDetectOffHours(t *testing.T) {
	entries := []store.AuditEntry{
		entry("tok_a", time.Date(2025, 6, 16, 3, 0, 0, 0, time.UTC), store.ActionReplace, store.ResultSuccess),  // Monday 3am
		entry("tok_a", time.Date(2025, 6, 21, 12, 0, 0, 0, time.UTC), store.ActionReplace, store.ResultSuccess), // Saturday noon
		entry("tok_a", time.Date(2025, 6, 16, 12, 0, 0, 0, time.UTC), store.ActionReplace, store.ResultSuccess), // Monday noon (in-hours)
	}

	r := Detect(entries, "tenant_x", monday.Add(-7*24*time.Hour), monday.Add(time.Hour), time.UTC)

	f := hasKind(r, FlagOffHours)
	if f == nil {
		t.Fatalf("no off_hours flag; flags: %+v", r.Flags)
	}
	if f.Count != 2 {
		t.Errorf("off-hours count = %d, want 2 (3am + weekend)", f.Count)
	}
}

func TestDetectHighVolume(t *testing.T) {
	var entries []store.AuditEntry
	for i := 0; i < volumeCount; i++ {
		entries = append(entries, entry("tok_busy", monday.Add(time.Duration(i)*time.Minute), store.ActionReplace, store.ResultSuccess))
	}

	r := Detect(entries, "tenant_x", monday.Add(-time.Hour), monday.Add(2*time.Hour), time.UTC)

	f := hasKind(r, FlagHighVolume)
	if f == nil {
		t.Fatalf("no high_volume flag; flags: %+v", r.Flags)
	}
	if f.Count != volumeCount {
		t.Errorf("count = %d, want %d", f.Count, volumeCount)
	}
}

// The worst signal must read first, so the briefing leads with it.
func TestFlagsOrderedMostSevereFirst(t *testing.T) {
	var entries []store.AuditEntry
	// One caller: a dense deactivation cluster (bulk) that also denies a lot.
	for i := 0; i < denialCount; i++ {
		entries = append(entries, entry("tok_x", monday.Add(time.Duration(i)*time.Minute), store.ActionCreate, store.ResultDenied))
	}
	for i := 0; i < bulkCount; i++ {
		entries = append(entries, entry("tok_x", monday.Add(time.Duration(i)*time.Minute), store.ActionDeactivate, store.ResultSuccess))
	}

	r := Detect(entries, "tenant_x", monday.Add(-time.Hour), monday.Add(time.Hour), time.UTC)

	if len(r.Flags) < 2 {
		t.Fatalf("want at least bulk + denial flags, got %+v", r.Flags)
	}
	if r.Flags[0].Kind != FlagBulkDeactivation {
		t.Errorf("first flag = %q, want bulk_deactivation", r.Flags[0].Kind)
	}
}

func TestBuildPromptRendersFacts(t *testing.T) {
	entries := []store.AuditEntry{}
	for i := 0; i < bulkCount; i++ {
		entries = append(entries, entry("tok_render", monday.Add(time.Duration(i)*time.Minute), store.ActionDeactivate, store.ResultSuccess))
	}

	r := Detect(entries, "tenant_x", monday.Add(-time.Hour), monday.Add(time.Hour), time.UTC)
	system, user := BuildPrompt(r)

	if !strings.Contains(system, "Advise only") {
		t.Error("system prompt is missing the advisory-only rule")
	}
	for _, want := range []string{"tenant_x", "Bulk deactivation", "tok_render"} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt is missing %q; got:\n%s", want, user)
		}
	}
}

func TestRenderTruncationNote(t *testing.T) {
	r := Detect(nil, "tenant_x", monday.Add(-time.Hour), monday, time.UTC)
	if strings.Contains(Render(r), "may be omitted") {
		t.Error("a complete window should carry no truncation note")
	}

	r.Truncated = true
	if !strings.Contains(Render(r), "may be omitted") {
		t.Error("a truncated window should warn that older activity may be omitted")
	}
}

func TestBuildPromptNoFindings(t *testing.T) {
	r := Detect(nil, "", monday.Add(-time.Hour), monday, time.UTC)
	_, user := BuildPrompt(r)

	if !strings.Contains(user, "No flagged signals") {
		t.Errorf("empty report should say nothing was flagged; got:\n%s", user)
	}
	if !strings.Contains(user, "all tenants") {
		t.Errorf("empty tenant scope should render as all tenants; got:\n%s", user)
	}
}
