// aria is ARIA (Audit Risk Intelligence Advisor): a read-only reviewer that
// reads the audit_log, computes activity signals in deterministic Go, and asks
// an LLM to narrate them into a plain-English briefing.
//
// It is advisory only. Nothing it produces is written back to the store or
// consulted by the auth path — the summary is printed for a human, who decides
// what, if anything, to do about it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/meghamahna/SCIMage/internal/aria"
	"github.com/meghamahna/SCIMage/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "aria:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("aria", flag.ContinueOnError)
	tenantID := fs.String("tenant", "", "scope the review to one tenant (all tenants if omitted)")
	since := fs.String("since", "24h", `how far back to review, e.g. "24h", "7d", or a Go duration`)
	tz := fs.String("timezone", "", "IANA timezone for the business-hours check (defaults to $ARIA_TIMEZONE, then the host's local zone)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	window, err := parseSince(*since)
	if err != nil {
		return err
	}
	loc, err := resolveLocation(*tz)
	if err != nil {
		return err
	}

	dsn, err := store.DSNFromEnv()
	if err != nil {
		return err
	}

	ctx := context.Background()
	s, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now()
	entries, err := s.ListAuditEntriesSince(ctx, *tenantID, now.Add(-window))
	if err != nil {
		return err
	}

	report := aria.Detect(entries, *tenantID, now.Add(-window), now, loc)

	// The reader clamps to store.MaxPageSize newest-first, so a full batch means
	// the window may hold more than was read. Say so rather than quietly review a
	// partial window — an audit tool that silently drops rows is worse than one
	// that admits its bound.
	report.Truncated = len(entries) >= store.MaxPageSize

	// A quiet window has nothing for the model to add, so print the
	// deterministic summary and don't spend an LLM call (or even require the
	// LLM credentials).
	if !report.HasFindings() {
		fmt.Print(aria.Render(report))
		return nil
	}

	cfg, err := aria.ConfigFromEnv()
	if err != nil {
		return err
	}

	system, user := aria.BuildPrompt(report)
	summary, err := aria.NewClient(cfg).Summarize(ctx, system, user)
	if err != nil {
		return err
	}

	fmt.Println(summary)
	return nil
}

// parseSince accepts a bare day count ("7d") alongside anything
// time.ParseDuration understands, since "how many days back" is a unit an
// operator thinks in — the same convenience scimage-admin's -expires offers.
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("-since is required")
	}

	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid -since %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid -since %q", s)
	}
	return d, nil
}

// resolveLocation picks the timezone for the off-hours check: the flag, then
// $ARIA_TIMEZONE, then the host's local zone.
func resolveLocation(flagTZ string) (*time.Location, error) {
	name := strings.TrimSpace(flagTZ)
	if name == "" {
		name = strings.TrimSpace(os.Getenv("ARIA_TIMEZONE"))
	}
	if name == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", name, err)
	}
	return loc, nil
}
