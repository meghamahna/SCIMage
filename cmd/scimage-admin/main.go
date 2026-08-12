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
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New(`usage:
  scimage-admin tenant create -name "Acme Corp"
  scimage-admin tenant list
  scimage-admin token issue -tenant <tenantID> -label "Okta prod" [-expires 90d]
  scimage-admin token list -tenant <tenantID>
  scimage-admin token revoke <keyID>`)
}

func tenantCmd(ctx context.Context, s *store.Store, action string, args []string) error {
	switch action {
	case "create":
		fs := flag.NewFlagSet("tenant create", flag.ContinueOnError)
		name := fs.String("name", "", "display name for the new tenant")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" {
			return errors.New("tenant create: -name is required")
		}

		t, err := s.CreateTenant(ctx, *name)
		if err != nil {
			return err
		}

		fmt.Printf("Created tenant %s (%s)\n", t.ID, t.Name)
		fmt.Printf("SCIM base URL: %s\n", tenantBaseURL(t.ID))
		return nil

	case "list":
		tenants, err := s.ListTenants(ctx)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCREATED")
		for _, t := range tenants {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Format(time.RFC3339))
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
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*label) == "" {
			return errors.New("token issue: -tenant and -label are required")
		}

		if _, err := s.GetTenant(ctx, *tenantID); err != nil {
			return fmt.Errorf("token issue: %w", err)
		}

		expiresAt, err := parseExpiry(*expires)
		if err != nil {
			return fmt.Errorf("token issue: %w", err)
		}

		plaintext, tok, err := s.IssueToken(ctx, *tenantID, *label, "scimage-admin", expiresAt)
		if err != nil {
			return err
		}

		fmt.Printf("Issued token %s for tenant %s\n", tok.KeyID, tok.TenantID)
		fmt.Println("This token is shown once and is not stored anywhere — save it now:")
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
		fmt.Fprintln(tw, "KEY ID\tLABEL\tCREATED\tLAST USED\tEXPIRES\tREVOKED")
		for _, t := range tokens {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				t.KeyID, t.Label, t.CreatedAt.Format(time.RFC3339),
				formatTimePtr(t.LastUsedAt), formatTimePtr(t.ExpiresAt), formatTimePtr(t.RevokedAt))
		}
		return tw.Flush()

	case "revoke":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return errors.New("usage: scimage-admin token revoke <keyID>")
		}
		if err := s.RevokeToken(ctx, args[0]); err != nil {
			return err
		}
		fmt.Printf("Revoked %s\n", args[0])
		return nil

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
