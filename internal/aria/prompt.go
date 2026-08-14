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

Deterministic code has already computed the signals below from the audit trail, and the report header (tenant, window, totals) is printed separately above your text. Your job is to write the findings section a busy administrator can act on in under a minute.

Structure it as a short, professional report:
- Do not repeat the header. Start straight into the findings.
- Give each flagged signal its own section under a bold label ending with a colon, on its own line, for example **Bulk deactivation:**, **Denials:**, or **Volume:**. Order them most serious first.
- Under each label, one or two short sentences with the concrete numbers, callers, and times from the input.
- Close with a **Summary:** section of one or two sentences that restate the headline and point the human at what to check.

What to say:
- Report only the signals you are given: the callers, counts, and times in the input. Add nothing that is not there.
- State what happened, not why. The data shows activity, not motive, so do not guess at intent, cause, retries, or permission changes.
- Advise only. Do not tell anyone to create, change, deactivate, or delete anything, and do not make or recommend an access decision. The human decides.
- If nothing was flagged, say so in one plain sentence. Do not manufacture concern.

How to write it:
- Keep each section short and plain. Everyday words, no filler, no hedging, no throat-clearing like "it is worth noting", and no dramatic or marketing language.
- Do not use em-dashes; use a period or a comma. Avoid stock AI phrasing and cliches.
- Name the caller by its token id and give the real numbers and times. Concrete beats vague.`

// BuildPrompt returns the system and user messages for a Report. The user
// message is a deterministic rendering of the already-computed facts; the
// model adds phrasing, not findings.
func BuildPrompt(r Report) (system, user string) {
	return systemPrompt, Render(r)
}

// Header is the deterministic top block of every report: title, tenant, window,
// and activity totals. It prints on every run, above the model's narration on a
// window with findings, and above the no-findings line on a quiet one.
func Header(r Report) string {
	loc := r.Location
	if loc == nil {
		loc = time.UTC
	}

	var b strings.Builder
	b.WriteString("## ARIA Audit Report\n")
	if r.TenantID != "" {
		if r.TenantName != "" {
			fmt.Fprintf(&b, "**Tenant Name**: %s\n", r.TenantName)
		}
		fmt.Fprintf(&b, "**Tenant ID**: %s\n", r.TenantID)
	} else {
		b.WriteString("**Tenant**: all tenants\n")
	}
	fmt.Fprintf(&b, "**Time Window**: %s to %s\n", r.Since.In(loc).Format(time.RFC3339), r.Now.In(loc).Format(time.RFC3339))
	fmt.Fprintf(&b, "**Activity**: %d audit entries from %d distinct caller(s)\n", r.Total, r.Callers)
	if r.Truncated {
		b.WriteString("**Note**: the reviewer returned its maximum number of entries, so older activity in this window may be omitted. Narrow the window (a smaller -since) for full coverage.\n")
	}
	b.WriteString("\n---\n")
	return b.String()
}

// Render is the full deterministic report: the Header plus the computed flags.
// It is the LLM's user message, and it is what ARIA prints verbatim when a
// window has no findings (nothing for the model to add, no reason to spend a call).
func Render(r Report) string {
	loc := r.Location
	if loc == nil {
		loc = time.UTC
	}

	var b strings.Builder
	b.WriteString(Header(r))

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
			fmt.Fprintf(&b, "- Off-hours activity: %d mutation(s) fell outside business hours (%02d:00 to %02d:00) or on a weekend.\n",
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
