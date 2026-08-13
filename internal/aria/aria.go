// Package aria is the audit-review advisor (ARIA: Audit Risk Intelligence
// Advisor). It reads the audit trail, computes activity signals in
// deterministic Go, and hands those already-computed facts to an LLM that only
// narrates them.
//
// The split is deliberate and is the security design of this component: every
// figure a human sees is computed here, in code that can be read and tested;
// the model never detects a pattern, decides anything, or touches the store or
// the auth path. It writes prose about facts Go already found.
package aria

import (
	"sort"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

// Thresholds are constants rather than configuration: ARIA surfaces candidates
// for a human to judge, so a low bar that occasionally over-flags is safer than
// a high one that stays quiet. Tune here if a deployment's baseline is noisier.
const (
	bulkWindow  = 10 * time.Minute // a "short window" for clustered deactivations
	bulkCount   = 5                // deactivations/deletes by one caller in bulkWindow
	volumeCount = 50               // total mutations by one caller in the review window
	denialCount = 5                // refusals by one caller in the review window

	// The local working day. A mutation outside it, or on a weekend, is
	// off-hours: provisioning traffic normally tracks an IdP's business day.
	businessStartHour = 7
	businessEndHour   = 19

	maxExamples = 5 // representative rows carried per flag, to keep the prompt small
)

// FlagKind names the pattern a Flag reports. Kinds are ordered most-severe
// first in a Report.
type FlagKind string

const (
	FlagBulkDeactivation FlagKind = "bulk_deactivation"
	FlagDenialBurst      FlagKind = "denial_burst"
	FlagOffHours         FlagKind = "off_hours"
	FlagHighVolume       FlagKind = "high_volume"
)

// Event is one audit row reduced to what a briefing needs. Before/after images
// are dropped on purpose: ARIA reports that activity happened and its shape,
// not the user attributes that changed.
type Event struct {
	At           time.Time
	ActorToken   string
	TenantID     string
	ResourceType string
	Action       string
	Result       string
	TargetID     string
}

// Flag is one noteworthy pattern the deterministic pass found.
type Flag struct {
	Kind        FlagKind
	Actor       string    // actor_token responsible; "" when the flag isn't caller-specific
	Count       int       // the figure that tripped the threshold
	WindowStart time.Time // set for clustered flags (bulk deactivation)
	WindowEnd   time.Time
	Examples    []Event // a few representative rows
}

// Report is the deterministic read of one audit window. Everything in it was
// computed in Go; the LLM is handed this and asked only to narrate it.
type Report struct {
	TenantID  string         // "" means the review spans every tenant
	Since     time.Time      // window start
	Now       time.Time      // window end / "as of"
	Location  *time.Location // timezone the business-hours check and rendering use
	Total     int            // audit entries reviewed (includes refusals, not just successes)
	Callers   int            // distinct actor tokens seen
	Truncated bool           // the reader hit its cap, so older in-window entries may be missing
	Flags     []Flag         // noteworthy patterns, most severe first
}

// HasFindings reports whether anything tripped a threshold.
func (r Report) HasFindings() bool { return len(r.Flags) > 0 }

type tally struct {
	actor         string
	total         int
	denials       int
	mutationTimes []time.Time // deactivations/deletes, for cluster detection
}

// Detect computes the signals in entries. `now` stamps the window end;
// `loc` is the timezone the off-hours check evaluates against. entries may be
// in any order — Detect does not assume the store's newest-first ordering.
func Detect(entries []store.AuditEntry, tenantID string, since, now time.Time, loc *time.Location) Report {
	if loc == nil {
		loc = time.UTC
	}

	r := Report{
		TenantID: tenantID,
		Since:    since,
		Now:      now,
		Location: loc,
		Total:    len(entries),
	}

	tallies := map[string]*tally{}
	var offHours []Event

	for _, e := range entries {
		t := tallies[e.Actor.ActorToken]
		if t == nil {
			t = &tally{actor: e.Actor.ActorToken}
			tallies[e.Actor.ActorToken] = t
		}
		t.total++
		if e.Result == store.ResultDenied {
			t.denials++
		}
		if e.Action == store.ActionDeactivate || e.Action == store.ActionDelete {
			t.mutationTimes = append(t.mutationTimes, e.At)
		}
		if isOffHours(e.At, loc) {
			offHours = append(offHours, toEvent(e))
		}
	}
	r.Callers = len(tallies)

	// Stable output: walk callers in a deterministic order regardless of map
	// iteration, busiest first so the loudest signal reads first within a kind.
	callers := make([]*tally, 0, len(tallies))
	for _, t := range tallies {
		callers = append(callers, t)
	}
	sort.Slice(callers, func(i, j int) bool {
		if callers[i].total != callers[j].total {
			return callers[i].total > callers[j].total
		}
		return callers[i].actor < callers[j].actor
	})

	// Kinds are appended in severity order so a Report reads worst-first.
	for _, t := range callers {
		if n, start, end, ok := maxCluster(t.mutationTimes, bulkWindow, bulkCount); ok {
			r.Flags = append(r.Flags, Flag{
				Kind:        FlagBulkDeactivation,
				Actor:       t.actor,
				Count:       n,
				WindowStart: start,
				WindowEnd:   end,
				Examples:    examplesFor(entries, t.actor, isDeactivation),
			})
		}
	}
	for _, t := range callers {
		if t.denials >= denialCount {
			r.Flags = append(r.Flags, Flag{
				Kind:     FlagDenialBurst,
				Actor:    t.actor,
				Count:    t.denials,
				Examples: examplesFor(entries, t.actor, isDenial),
			})
		}
	}
	if len(offHours) > 0 {
		r.Flags = append(r.Flags, Flag{
			Kind:     FlagOffHours,
			Count:    len(offHours),
			Examples: capExamples(offHours),
		})
	}
	for _, t := range callers {
		if t.total >= volumeCount {
			r.Flags = append(r.Flags, Flag{
				Kind:     FlagHighVolume,
				Actor:    t.actor,
				Count:    t.total,
				Examples: examplesFor(entries, t.actor, anyEvent),
			})
		}
	}

	return r
}

func isOffHours(t time.Time, loc *time.Location) bool {
	lt := t.In(loc)
	if wd := lt.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return true
	}
	h := lt.Hour()
	return h < businessStartHour || h >= businessEndHour
}

// maxCluster finds the densest run of times within `window`. times may be
// unsorted; it returns the largest count seen in any window and its bounds when
// that count meets minCount.
func maxCluster(times []time.Time, window time.Duration, minCount int) (count int, start, end time.Time, ok bool) {
	if len(times) < minCount {
		return 0, time.Time{}, time.Time{}, false
	}

	sorted := make([]time.Time, len(times))
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	best, j := 0, 0
	var bStart, bEnd time.Time
	for i := range sorted {
		for sorted[i].Sub(sorted[j]) > window {
			j++
		}
		if n := i - j + 1; n > best {
			best, bStart, bEnd = n, sorted[j], sorted[i]
		}
	}
	if best >= minCount {
		return best, bStart, bEnd, true
	}
	return 0, time.Time{}, time.Time{}, false
}

func examplesFor(entries []store.AuditEntry, actor string, keep func(store.AuditEntry) bool) []Event {
	var out []Event
	for _, e := range entries {
		if e.Actor.ActorToken != actor || !keep(e) {
			continue
		}
		out = append(out, toEvent(e))
		if len(out) == maxExamples {
			break
		}
	}
	return out
}

func capExamples(events []Event) []Event {
	if len(events) <= maxExamples {
		return events
	}
	return events[:maxExamples]
}

func isDeactivation(e store.AuditEntry) bool {
	return e.Action == store.ActionDeactivate || e.Action == store.ActionDelete
}

func isDenial(e store.AuditEntry) bool { return e.Result == store.ResultDenied }

func anyEvent(store.AuditEntry) bool { return true }

func toEvent(e store.AuditEntry) Event {
	return Event{
		At:           e.At,
		ActorToken:   e.Actor.ActorToken,
		TenantID:     e.Actor.TenantID,
		ResourceType: e.ResourceType,
		Action:       e.Action,
		Result:       e.Result,
		TargetID:     e.TargetID,
	}
}
