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

Deterministic code has already computed the signals below from the audit trail. Your job is to report them in clear, human language a busy administrator can act on in under a minute.

What to say:
- Report only the signals you are given: the callers, counts, and times in the input. Add nothing that is not there.
- State what happened, not why. The data shows activity, not motive, so do not guess at intent, cause, retries, or permission changes.
- Lead with the most serious signal and group related ones.
- Advise only. Do not tell anyone to create, change, deactivate, or delete anything, and do not make or recommend an access decision. Close with one short line on what a person may want to check, and let them decide.
- If nothing was flagged, say so in one plain sentence. Do not manufacture concern.

How to write it:
- Keep it simple and brief. Short sentences, everyday words. Cut anything that does not help the reader decide what to look at.
- Sound like a person, not a report generator. No filler, no hedging, no throat-clearing like "it is worth noting" or "it appears that", and no dramatic or marketing language.
- Do not use em-dashes; use a period or a comma. Avoid stock AI phrasing and cliches.
- Name the caller by its token id and give the real numbers and times. Concrete beats vague.
- A few short sentences or tight bullets is plenty. Skip a preamble, and do not repeat everything in a closing summary.`

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
