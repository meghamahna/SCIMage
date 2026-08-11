// Package scim implements the SCIM 2.0 /Users endpoints (RFC 7644).
package scim

import (
	"fmt"
	"strings"
	"time"
)

const (
	userSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	listSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	errorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"

	// RFC 7644 §3.1. Only set on responses; requests aren't required to use it.
	contentType = "application/scim+json"
)

// Bool accepts a JSON boolean or a quoted one, and always marshals back as a
// real boolean.
//
// RFC 7643 types these attributes as boolean, but Entra sends
// emails[].primary as the string "true". A plain bool fails the whole decode,
// so every create from Entra would be a 400. Being liberal in what we accept
// here costs nothing and is the difference between working and not.
type Bool bool

func (b *Bool) UnmarshalJSON(data []byte) error {
	switch s := strings.ToLower(strings.Trim(strings.TrimSpace(string(data)), `"`)); s {
	case "true":
		*b = true
	case "false", "null", "":
		*b = false
	default:
		return fmt.Errorf("scim: %s is not a boolean", data)
	}
	return nil
}

func (b Bool) MarshalJSON() ([]byte, error) {
	if b {
		return []byte("true"), nil
	}
	return []byte("false"), nil
}

// Active is a pointer because SCIM defaults an omitted active to true, which a
// plain bool can't tell apart from an explicit false.
type User struct {
	Schemas []string `json:"schemas"`
	ID      string   `json:"id,omitempty"`

	// ExternalID is the client's own identifier for this user. It is stored and
	// returned unchanged, so an identity provider can reconcile against it.
	ExternalID string `json:"externalId,omitempty"`

	UserName string  `json:"userName"`
	Name     *Name   `json:"name,omitempty"`
	Emails   []Email `json:"emails,omitempty"`
	Active   *Bool   `json:"active,omitempty"`
	Meta     *Meta   `json:"meta,omitempty"`
}

type Name struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type Email struct {
	Value   string `json:"value"`
	Primary Bool   `json:"primary,omitempty"`
}

type Meta struct {
	ResourceType string    `json:"resourceType"`
	Created      time.Time `json:"created"`
	LastModified time.Time `json:"lastModified"`
	Location     string    `json:"location"`
}

// Resources is capitalised per RFC 7644 §3.4.2.
type ListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	ItemsPerPage int      `json:"itemsPerPage"`
	StartIndex   int      `json:"startIndex"`
	Resources    []User   `json:"Resources"`
}

// Status is a string in SCIM, not a number (RFC 7644 §3.12).
type Error struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	ScimType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}
