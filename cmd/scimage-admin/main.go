// scimage-admin manages tenants and their issued tokens directly against
// Postgres. It exists so the privileged surface — creating a tenant, minting
// or revoking a credential — never has to be a network endpoint: an operator
// with a shell on the database host is already trusted with more than that.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/meghamahna/SCIMage/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "scimage-admin:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return usageError()
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

	switch args[0] {
	case "tenant":
		return tenantCmd(ctx, s, args[1], args[2:])
	case "token":
		return tokenCmd(ctx, s, args[1], args[2:])
	case "audit":
		return auditCmd(ctx, s, args[1], args[2:])
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New(`usage:
  scimage-admin tenant create -name "Acme Corp" [-created-by "who"]
  scimage-admin tenant list
  scimage-admin token issue -tenant <tenantID> -label "Okta prod" [-expires 90d] [-created-by "who"]
  scimage-admin token list -tenant <tenantID>
  scimage-admin token revoke <keyID>
  scimage-admin audit list [-tenant <tenantID>]`)
}

// defaultActor is who ran the command, absent an explicit -created-by. USER
// is what every Unix shell sets; USERNAME is Windows' equivalent. Neither
// being set is unusual enough that "unknown" is an honest fallback rather
// than a guess at who this was.
func defaultActor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}

func tenantCmd(ctx context.Context, s *store.Store, action string, args []string) error {
	switch action {
	case "create":
		fs := flag.NewFlagSet("tenant create", flag.ContinueOnError)
		name := fs.String("name", "", "display name for the new tenant")
		createdBy := fs.String("created-by", "", "who's creating this, for the admin audit trail (defaults to $USER)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" {
			return errors.New("tenant create: -name is required")
		}
		if strings.TrimSpace(*createdBy) == "" {
			*createdBy = defaultActor()
		}

		t, err := s.CreateTenant(ctx, *name, *createdBy)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintf(tw, "TENANT ID\t%s\n", t.ID)
		fmt.Fprintf(tw, "NAME\t%s\n", t.Name)
		fmt.Fprintf(tw, "CREATED BY\t%s\n", t.CreatedBy)
		fmt.Fprintf(tw, "SCIM BASE URL\t%s\n", tenantBaseURL(t.ID))
		return tw.Flush()

	case "list":
		tenants, err := s.ListTenants(ctx)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCREATED BY\tCREATED")
		for _, t := range tenants {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Name, emptyDash(t.CreatedBy), t.CreatedAt.Format(time.RFC3339))
		}
		return tw.Flush()

	default:
		return usageError()
	}
}

func tokenCmd(ctx context.Context, s *store.Store, action string, args []string) error {
	switch action {
	case "issue":
		fs := flag.NewFlagSet("token issue", flag.ContinueOnError)
		tenantID := fs.String("tenant", "", "tenant to issue this token for")
		label := fs.String("label", "", "what this token is for, e.g. \"Okta prod\"")
		expires := fs.String("expires", "", `optional lifetime, e.g. "90d" or a Go duration like "720h"`)
		createdBy := fs.String("created-by", "", "who's issuing this, for the admin audit trail (defaults to $USER)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*label) == "" {
			return errors.New("token issue: -tenant and -label are required")
		}
		if strings.TrimSpace(*createdBy) == "" {
			*createdBy = defaultActor()
		}

		if _, err := s.GetTenant(ctx, *tenantID); err != nil {
			return fmt.Errorf("token issue: %w", err)
		}

		expiresAt, err := parseExpiry(*expires)
		if err != nil {
			return fmt.Errorf("token issue: %w", err)
		}

		plaintext, tok, err := s.IssueToken(ctx, *tenantID, *label, *createdBy, expiresAt)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintf(tw, "TOKEN ID\t%s\n", tok.KeyID)
		fmt.Fprintf(tw, "TENANT\t%s\n", tok.TenantID)
		fmt.Fprintf(tw, "LABEL\t%s\n", tok.Label)
		fmt.Fprintf(tw, "CREATED BY\t%s\n", tok.CreatedBy)
		if err := tw.Flush(); err != nil {
			return err
		}

		fmt.Println("\nShown once, not stored anywhere. Save it now:")
		fmt.Println(plaintext)
		return nil

	case "list":
		fs := flag.NewFlagSet("token list", flag.ContinueOnError)
		tenantID := fs.String("tenant", "", "tenant whose tokens to list")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*tenantID) == "" {
			return errors.New("token list: -tenant is required")
		}

		tokens, err := s.ListTokens(ctx, *tenantID)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY ID\tLABEL\tCREATED BY\tCREATED\tLAST USED\tEXPIRES\tREVOKED")
		for _, t := range tokens {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				t.KeyID, t.Label, emptyDash(t.CreatedBy), t.CreatedAt.Format(time.RFC3339),
				formatTimePtr(t.LastUsedAt), formatTimePtr(t.ExpiresAt), formatTimePtr(t.RevokedAt))
		}
		return tw.Flush()

	case "revoke":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return errors.New("usage: scimage-admin token revoke <keyID>")
		}
		if err := s.RevokeToken(ctx, args[0], defaultActor()); err != nil {
			return err
		}
		fmt.Printf("Revoked %s\n", args[0])
		return nil

	default:
		return usageError()
	}
}

// auditCmd is read-only: every privileged action tenant/token already takes
// is what populates admin_audit_log, in the same transaction as the change
// itself. This just surfaces it.
func auditCmd(ctx context.Context, s *store.Store, action string, args []string) error {
	switch action {
	case "list":
		fs := flag.NewFlagSet("audit list", flag.ContinueOnError)
		tenantID := fs.String("tenant", "", "scope to one tenant (all tenants if omitted)")
		if err := fs.Parse(args); err != nil {
			return err
		}

		entries, err := s.ListAdminAuditEntries(ctx, *tenantID, 0)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "AT\tTENANT\tACTOR\tACTION\tTARGET\tDETAIL")
		for _, e := range entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				e.At.Format(time.RFC3339), e.TenantID, e.Actor, e.Action, e.TargetID, emptyDash(e.Detail))
		}
		return tw.Flush()

	default:
		return usageError()
	}
}

// tenantBaseURL mirrors internal/scim.Handler.baseURL: the assembled URL is
// never stored, only ever derived from the deployment's own SCIM_BASE_URL
// plus the tenant id, so it can never go stale if the operator changes
// domains later.
func tenantBaseURL(tenantID string) string {
	root := strings.TrimSuffix(os.Getenv("SCIM_BASE_URL"), "/")
	if root == "" {
		root = "<SCIM_BASE_URL>"
	}
	return root + "/scim/v2/" + tenantID
}

// parseExpiry accepts a bare day count ("90d") alongside anything
// time.ParseDuration understands, since "days until this token expires" is
// the unit an operator actually thinks in.
func parseExpiry(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return nil, fmt.Errorf("invalid -expires %q: %w", s, err)
		}
		t := time.Now().Add(time.Duration(n) * 24 * time.Hour)
		return &t, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("invalid -expires %q: %w", s, err)
	}
	t := time.Now().Add(d)
	return &t, nil
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// emptyDash keeps table columns visually consistent with formatTimePtr's "-"
// for absent values, rather than a blank cell that reads as a scan error.
func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
