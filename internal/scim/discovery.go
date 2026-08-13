package scim

import (
	"net/http"

	"github.com/meghamahna/SCIMage/internal/store"
)

// The discovery endpoints (RFC 7644 §4). A SCIM client fetches these before it
// provisions anything, to learn what the server supports — so these documents
// describe this server's actual behaviour, and change when the behaviour does.
const (
	serviceProviderConfigSchema = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	resourceTypeSchema          = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	schemaSchema                = "urn:ietf:params:scim:schemas:core:2.0:Schema"
)

type supported struct {
	Supported bool `json:"supported"`
}

type bulkConfig struct {
	Supported      bool `json:"supported"`
	MaxOperations  int  `json:"maxOperations"`
	MaxPayloadSize int  `json:"maxPayloadSize"`
}

type filterConfig struct {
	Supported  bool `json:"supported"`
	MaxResults int  `json:"maxResults"`
}

type authScheme struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Primary     bool   `json:"primary,omitempty"`
}

type serviceProviderConfig struct {
	Schemas               []string     `json:"schemas"`
	DocumentationURI      string       `json:"documentationUri,omitempty"`
	Patch                 supported    `json:"patch"`
	Bulk                  bulkConfig   `json:"bulk"`
	Filter                filterConfig `json:"filter"`
	ChangePassword        supported    `json:"changePassword"`
	Sort                  supported    `json:"sort"`
	ETag                  supported    `json:"etag"`
	AuthenticationSchemes []authScheme `json:"authenticationSchemes"`
	Meta                  *Meta        `json:"meta,omitempty"`
}

type resourceType struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Endpoint    string   `json:"endpoint"`
	Description string   `json:"description"`
	Schema      string   `json:"schema"`
	Meta        *Meta    `json:"meta,omitempty"`
}

type schemaAttribute struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	MultiValued   bool              `json:"multiValued"`
	Description   string            `json:"description"`
	Required      bool              `json:"required"`
	CaseExact     bool              `json:"caseExact"`
	Mutability    string            `json:"mutability"`
	Returned      string            `json:"returned"`
	Uniqueness    string            `json:"uniqueness"`
	SubAttributes []schemaAttribute `json:"subAttributes,omitempty"`
}

type resourceSchema struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Attributes  []schemaAttribute `json:"attributes"`
	Meta        *Meta             `json:"meta,omitempty"`
}

// listOf wraps discovery documents in a ListResponse, which RFC 7644 §4
// requires for /ResourceTypes and /Schemas.
type listOf[T any] struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	ItemsPerPage int      `json:"itemsPerPage"`
	StartIndex   int      `json:"startIndex"`
	Resources    []T      `json:"Resources"`
}

func newList[T any](resources []T) listOf[T] {
	return listOf[T]{
		Schemas:      []string{listSchema},
		TotalResults: len(resources),
		ItemsPerPage: len(resources),
		StartIndex:   1,
		Resources:    resources,
	}
}

func (h *Handler) serviceProviderConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, serviceProviderConfig{
		Schemas: []string{serviceProviderConfigSchema},
		Patch:   supported{Supported: true},
		Bulk:    bulkConfig{Supported: false},
		// Equality on userName and externalId — the reconciliation lookups. The
		// wider grammar is refused rather than approximated.
		Filter: filterConfig{Supported: true, MaxResults: store.MaxPageSize},
		// Passwords are never accepted or stored, so there is nothing to change.
		ChangePassword: supported{Supported: false},
		Sort:           supported{Supported: false},
		ETag:           supported{Supported: false},
		AuthenticationSchemes: []authScheme{{
			Type:        "oauthbearertoken",
			Name:        "OAuth Bearer Token",
			Description: "Authentication using the OAuth Bearer Token standard",
			Primary:     true,
		}},
		Meta: &Meta{
			ResourceType: "ServiceProviderConfig",
			Location:     h.baseURL(r) + "/ServiceProviderConfig",
		},
	})
}

func (h *Handler) resourceTypes(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)

	writeJSON(w, http.StatusOK, newList([]resourceType{
		{
			Schemas:     []string{resourceTypeSchema},
			ID:          "User",
			Name:        "User",
			Endpoint:    "/Users",
			Description: "User Account",
			Schema:      userSchema,
			Meta: &Meta{
				ResourceType: "ResourceType",
				Location:     base + "/ResourceTypes/User",
			},
		},
		{
			Schemas:     []string{resourceTypeSchema},
			ID:          "Group",
			Name:        "Group",
			Endpoint:    "/Groups",
			Description: "Group",
			Schema:      groupSchema,
			Meta: &Meta{
				ResourceType: "ResourceType",
				Location:     base + "/ResourceTypes/Group",
			},
		},
	}))
}

func (h *Handler) schemas(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	user := userResourceSchema(base)

	// Registered extended attributes are advertised alongside the
	// core ones, so an IdP admin can discover and map to them — the same
	// "declaration matches behaviour" principle the rest of this document keeps.
	if h.extended && h.attrs != nil {
		attrs, err := h.attrs.ListAttributes(r.Context(), r.PathValue("tenantID"))
		if err != nil {
			serverError(w, "list registered attributes", err)
			return
		}
		for _, a := range attrs {
			user.Attributes = append(user.Attributes, schemaAttribute{
				Name:        a.Name,
				Type:        a.Type,
				Description: "Registered extended attribute.",
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			})
		}
	}

	writeJSON(w, http.StatusOK, newList([]resourceSchema{user, groupResourceSchema(base)}))
}

// userResourceSchema describes only the attributes this server stores, so the
// declaration and the behaviour stay in step.
func userResourceSchema(base string) resourceSchema {
	return resourceSchema{
		Schemas:     []string{schemaSchema},
		ID:          userSchema,
		Name:        "User",
		Description: "User Account",
		Attributes: []schemaAttribute{
			{
				Name:        "externalId",
				Type:        "string",
				Description: "An identifier for the resource as defined by the provisioning client.",
				CaseExact:   true,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			},
			{
				Name:        "userName",
				Type:        "string",
				Description: "Unique identifier for the User, used by the user to log in.",
				Required:    true,
				// caseExact=false is why uniqueness is enforced on
				// lower(user_name): bjensen and BJensen are one identity.
				CaseExact:  false,
				Mutability: "readWrite",
				Returned:   "default",
				Uniqueness: "server",
			},
			{
				Name:        "name",
				Type:        "complex",
				Description: "The components of the user's name.",
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []schemaAttribute{
					attr("givenName", "The given name of the User."),
					attr("familyName", "The family name of the User."),
				},
			},
			{
				Name:        "emails",
				Type:        "complex",
				MultiValued: true,
				Description: "Email addresses for the User.",
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []schemaAttribute{
					attr("value", "Email address for the User."),
					{
						Name:        "primary",
						Type:        "boolean",
						Description: "Whether this is the primary email address.",
						Mutability:  "readWrite",
						Returned:    "default",
						Uniqueness:  "none",
					},
				},
			},
			{
				Name:        "active",
				Type:        "boolean",
				Description: "A Boolean indicating the User's administrative status.",
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			},
		},
		Meta: &Meta{
			ResourceType: "Schema",
			Location:     base + "/Schemas/" + userSchema,
		},
	}
}

// groupResourceSchema describes only the attributes this server stores and
// returns. members declares just value and $ref — not display — because
// display is never populated (see the Member comment in models.go), and the
// declaration should match the behaviour rather than promise more.
func groupResourceSchema(base string) resourceSchema {
	return resourceSchema{
		Schemas:     []string{schemaSchema},
		ID:          groupSchema,
		Name:        "Group",
		Description: "Group",
		Attributes: []schemaAttribute{
			{
				Name:        "externalId",
				Type:        "string",
				Description: "An identifier for the resource as defined by the provisioning client.",
				CaseExact:   true,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			},
			{
				Name:        "displayName",
				Type:        "string",
				Description: "A human-readable name for the Group.",
				Required:    true,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "server",
			},
			{
				Name:        "members",
				Type:        "complex",
				MultiValued: true,
				Description: "A list of members of the Group.",
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []schemaAttribute{
					attr("value", "The id of a member of this Group."),
					{
						Name:        "$ref",
						Type:        "reference",
						Description: "The URI of the corresponding User resource.",
						Mutability:  "readWrite",
						Returned:    "default",
						Uniqueness:  "none",
					},
				},
			},
		},
		Meta: &Meta{
			ResourceType: "Schema",
			Location:     base + "/Schemas/" + groupSchema,
		},
	}
}

func attr(name, description string) schemaAttribute {
	return schemaAttribute{
		Name:        name,
		Type:        "string",
		Description: description,
		Mutability:  "readWrite",
		Returned:    "default",
		Uniqueness:  "none",
	}
}
