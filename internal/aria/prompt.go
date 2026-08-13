package aria

import (
	"fmt"
	"strings"
	"time"
)

// systemPrompt fixes ARIA's role. It is advisory-only by design: the model
// narrates signals Go already computed and must never make or recommend a
// provisioning or authorization decision. That boundary is the point of this
// whole component, so it is stated to the model as plainly as it is enforced in
// code (ARIA's output never re-enters the store or the auth path).
const systemPrompt = `You are ARIA (Audit Risk Intelligence Advisor), a read-only assistant for a SCIM 2.0 provisioning server's audit log.

You are given signals that deterministic code has ALREADY computed from the audit trail. Your only job is to turn those signals into a short, plain-English briefing a human reviewer can read in under a minute.

Rules:
- Advise only. Never instruct anyone to create, update, deactivate, or delete a user or group, and never make or recommend an access, authorization, or provisioning decision. Describe what the activity shows; a human decides what to do about it.
- Use only the signals provided. Do not invent counts, times, callers, or patterns that are not in the input, and do not speculate about intent beyond what the data supports.
- If there are no flagged signals, say so plainly in a sentence — do not manufacture concern.
- Be concrete: name the caller (by its token id), the counts, and the times you were given. Group related signals and lead with the most serious. Keep it to a few short paragraphs or bullets.`

// BuildPrompt returns the system and user messages for a Report. The user
// message is a deterministic rendering of the already-computed facts — the
// model adds phrasing, not findings.
func BuildPrompt(r Report) (system, user string) {
	return systemPrompt, Render(r)
}

// Render is the deterministic text form of a Report. It is both the LLM's user
// message and the output ARIA prints directly when a window has no findings —
// in that case the facts are trivial, so there's nothing for the model to add
// and no reason to spend a call.
func Render(r Report) string {
	loc := r.Location
	if loc == nil {
		loc = time.UTC
	}

	var b strings.Builder

	scope := "all tenants"
	if r.TenantID != "" {
		scope = "tenant " + r.TenantID
	}
	fmt.Fprintf(&b, "Audit review for %s\n", scope)
	fmt.Fprintf(&b, "Window: %s to %s\n", r.Since.In(loc).Format(time.RFC3339), r.Now.In(loc).Format(time.RFC3339))
	fmt.Fprintf(&b, "Audit entries reviewed: %d, from %d distinct caller(s)\n", r.Total, r.Callers)
	if r.Truncated {
		b.WriteString("Note: the reviewer returned its maximum number of entries, so older activity in this window may be omitted — narrow the window (a smaller -since) for full coverage.\n")
	}

	if !r.HasFindings() {
		b.WriteString("\nNo flagged signals: nothing tripped the review thresholds in this window.\n")
		return b.String()
	}

	b.WriteString("\nFlagged signals (most serious first):\n")
	for _, f := range r.Flags {
		switch f.Kind {
		case FlagBulkDeactivation:
			fmt.Fprintf(&b, "- Bulk deactivation: caller %s ran %d deactivations/deletes between %s and %s.\n",
				f.Actor, f.Count, f.WindowStart.In(loc).Format("15:04:05 MST"), f.WindowEnd.In(loc).Format("15:04:05 MST"))
		case FlagDenialBurst:
			fmt.Fprintf(&b, "- Denial burst: caller %s was refused %d time(s) in the window.\n", f.Actor, f.Count)
		case FlagOffHours:
			fmt.Fprintf(&b, "- Off-hours activity: %d mutation(s) fell outside business hours (%02d:00–%02d:00) or on a weekend.\n",
				f.Count, businessStartHour, businessEndHour)
		case FlagHighVolume:
			fmt.Fprintf(&b, "- High volume: caller %s made %d mutations in the window.\n", f.Actor, f.Count)
		}
		for _, e := range f.Examples {
			fmt.Fprintf(&b, "    e.g. %s  %s %s (%s)  target=%s  caller=%s\n",
				e.At.In(loc).Format(time.RFC3339), e.ResourceType, e.Action, e.Result, dashIfEmpty(e.TargetID), e.ActorToken)
		}
	}

	return b.String()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
