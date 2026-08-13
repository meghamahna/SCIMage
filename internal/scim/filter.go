package scim

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/meghamahna/SCIMage/internal/store"
)

// SCIM defines a full filter grammar (RFC 7644 §3.4.2.2). This server supports
// one shape of it — `attribute eq "value"` on userName or externalId — because
// that is what identity providers send to reconcile a user they may already
// have provisioned, and it maps onto an indexed lookup.
//
// Everything else is refused with invalidFilter rather than answered
// approximately: a filter that silently returns the wrong set would have a
// client update or deactivate the wrong person.
var eqFilter = regexp.MustCompile(`^\s*(\w+)\s+eq\s+"([^"]*)"\s*$`)

// filterableAttributes are matched case-insensitively, since SCIM attribute
// names are (RFC 7643 §2.1).
var filterableAttributes = map[string]func(*store.UserFilter, string){
	"username":   func(f *store.UserFilter, v string) { f.UserName = v },
	"externalid": func(f *store.UserFilter, v string) { f.ExternalID = v },
}

// parseFilter turns a SCIM filter expression into a store filter. An empty
// expression is not a filter, and lists everything.
func parseFilter(expr string) (store.UserFilter, error) {
	var f store.UserFilter
	if strings.TrimSpace(expr) == "" {
		return f, nil
	}

	m := eqFilter.FindStringSubmatch(expr)
	if m == nil {
		return f, fmt.Errorf(`only "attribute eq \"value\"" is supported`)
	}

	apply, ok := filterableAttributes[strings.ToLower(m[1])]
	if !ok {
		return f, fmt.Errorf("filtering on %q is not supported; use userName or externalId", m[1])
	}

	// An empty value would match nothing useful and reads as a client bug.
	if m[2] == "" {
		return f, fmt.Errorf("a filter value is required")
	}

	apply(&f, m[2])
	return f, nil
}

// groupFilterableAttributes mirrors filterableAttributes for /Groups:
// displayName is a group's reconciliation key, the same role userName plays
// for a User.
var groupFilterableAttributes = map[string]func(*store.GroupFilter, string){
	"displayname": func(f *store.GroupFilter, v string) { f.DisplayName = v },
	"externalid":  func(f *store.GroupFilter, v string) { f.ExternalID = v },
}

func parseGroupFilter(expr string) (store.GroupFilter, error) {
	var f store.GroupFilter
	if strings.TrimSpace(expr) == "" {
		return f, nil
	}

	m := eqFilter.FindStringSubmatch(expr)
	if m == nil {
		return f, fmt.Errorf(`only "attribute eq \"value\"" is supported`)
	}

	apply, ok := groupFilterableAttributes[strings.ToLower(m[1])]
	if !ok {
		return f, fmt.Errorf("filtering on %q is not supported; use displayName or externalId", m[1])
	}

	if m[2] == "" {
		return f, fmt.Errorf("a filter value is required")
	}

	apply(&f, m[2])
	return f, nil
}
