package scim

import (
	"net/http"
	"testing"
)

func TestServiceProviderConfig(t *testing.T) {
	requireDB(t)

	rr := do(t, http.MethodGet, "/ServiceProviderConfig", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	if got := rr.Header().Get("Content-Type"); got != contentType {
		t.Errorf("Content-Type = %q, want %q", got, contentType)
	}

	cfg := decodeBody[serviceProviderConfig](t, rr)
	if len(cfg.Schemas) != 1 || cfg.Schemas[0] != serviceProviderConfigSchema {
		t.Errorf("schemas = %v, want the ServiceProviderConfig schema", cfg.Schemas)
	}
	if len(cfg.AuthenticationSchemes) == 0 {
		t.Error("no authenticationSchemes — a client can't tell how to authenticate")
	}
	if cfg.Meta == nil || cfg.Meta.ResourceType != "ServiceProviderConfig" {
		t.Errorf("meta = %+v, want resourceType ServiceProviderConfig", cfg.Meta)
	}
}

// The document is a promise. These check it against what the server actually
// does, so a capability can't be advertised before it works — or stay denied
// after it does.
func TestServiceProviderConfigMatchesBehaviour(t *testing.T) {
	requireDB(t)

	cfg := decodeBody[serviceProviderConfig](t, do(t, http.MethodGet, "/ServiceProviderConfig", nil))

	t.Run("filter", func(t *testing.T) {
		rr := do(t, http.MethodGet, `/Users?filter=userName+eq+%22jdoe%22`, nil)

		if cfg.Filter.Supported && rr.Code != http.StatusOK {
			t.Errorf("filter declared supported, but a filtered list returned %d", rr.Code)
		}
		if !cfg.Filter.Supported && rr.Code != http.StatusBadRequest {
			t.Errorf("filter declared unsupported, but a filtered list returned %d, want 400", rr.Code)
		}
	})

	t.Run("patch", func(t *testing.T) {
		rr := do(t, http.MethodPatch, "/Users/"+nonexistentID, nil)

		if cfg.Patch.Supported && rr.Code == http.StatusNotImplemented {
			t.Error("patch declared supported, but PATCH returned 501")
		}
		if !cfg.Patch.Supported && rr.Code != http.StatusNotImplemented {
			t.Errorf("patch declared unsupported, but PATCH returned %d, want 501", rr.Code)
		}
	})
}

func TestResourceTypes(t *testing.T) {
	requireDB(t)

	rr := do(t, http.MethodGet, "/ResourceTypes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	list := decodeBody[listOf[resourceType]](t, rr)
	if len(list.Schemas) != 1 || list.Schemas[0] != listSchema {
		t.Errorf("schemas = %v, want the ListResponse schema", list.Schemas)
	}
	if len(list.Resources) != 2 {
		t.Fatalf("got %d resource types, want 2 (User and Group)", len(list.Resources))
	}

	byID := make(map[string]resourceType, len(list.Resources))
	for _, r := range list.Resources {
		byID[r.ID] = r
	}

	user, ok := byID["User"]
	if !ok || user.Endpoint != "/Users" || user.Schema != userSchema {
		t.Errorf("User resource type = %+v, want endpoint /Users and schema %q", user, userSchema)
	}

	group, ok := byID["Group"]
	if !ok || group.Endpoint != "/Groups" || group.Schema != groupSchema {
		t.Errorf("Group resource type = %+v, want endpoint /Groups and schema %q", group, groupSchema)
	}
}

func TestSchemas(t *testing.T) {
	requireDB(t)

	rr := do(t, http.MethodGet, "/Schemas", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	list := decodeBody[listOf[resourceSchema]](t, rr)
	if len(list.Resources) != 2 {
		t.Fatalf("got %d schemas, want 2 (User and Group)", len(list.Resources))
	}

	byID := make(map[string]resourceSchema, len(list.Resources))
	for _, s := range list.Resources {
		byID[s.ID] = s
	}

	user, ok := byID[userSchema]
	if !ok {
		t.Fatalf("no schema declared for %q", userSchema)
	}
	userAttrs := attrsByName(user)

	// Every declared attribute has to be one the server actually stores.
	for _, want := range []string{"userName", "name", "emails", "active"} {
		if _, ok := userAttrs[want]; !ok {
			t.Errorf("User schema is missing %q", want)
		}
	}

	// userName's characteristics are what tell a client that bjensen and
	// BJensen are one identity, which is how the store behaves.
	userName := userAttrs["userName"]
	if !userName.Required {
		t.Error("userName.required = false, want true")
	}
	if userName.CaseExact {
		t.Error("userName.caseExact = true, want false — uniqueness is on lower(user_name)")
	}
	if userName.Uniqueness != "server" {
		t.Errorf("userName.uniqueness = %q, want server", userName.Uniqueness)
	}

	group, ok := byID[groupSchema]
	if !ok {
		t.Fatalf("no schema declared for %q", groupSchema)
	}
	groupAttrs := attrsByName(group)

	for _, want := range []string{"displayName", "members"} {
		if _, ok := groupAttrs[want]; !ok {
			t.Errorf("Group schema is missing %q", want)
		}
	}

	displayName := groupAttrs["displayName"]
	if !displayName.Required {
		t.Error("displayName.required = false, want true")
	}
	if displayName.Uniqueness != "server" {
		t.Errorf("displayName.uniqueness = %q, want server", displayName.Uniqueness)
	}

	// display is never populated (see the Member comment in models.go), so
	// the declaration shouldn't promise it.
	members := groupAttrs["members"]
	memberSubAttrs := attrsByName(resourceSchema{Attributes: members.SubAttributes})
	if _, ok := memberSubAttrs["display"]; ok {
		t.Error("members declares a display subAttribute that is never populated")
	}
	if _, ok := memberSubAttrs["value"]; !ok {
		t.Error("members is missing the value subAttribute")
	}
}

func attrsByName(s resourceSchema) map[string]schemaAttribute {
	byName := make(map[string]schemaAttribute, len(s.Attributes))
	for _, a := range s.Attributes {
		byName[a.Name] = a
	}
	return byName
}

// Discovery describes the server, so it sits behind the same bearer check as
// everything else.
func TestDiscoveryRequiresAuth(t *testing.T) {
	routes := NewHandler(nil, nil, fakeTokenStore{tok: validFakeToken()}).Routes()

	for _, path := range []string{
		"/scim/v2/" + fakeTenantID + "/ServiceProviderConfig",
		"/scim/v2/" + fakeTenantID + "/ResourceTypes",
		"/scim/v2/" + fakeTenantID + "/Schemas",
	} {
		t.Run(path, func(t *testing.T) {
			rr := sendUnauthenticated(t, routes, http.MethodGet, path)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a token", rr.Code)
			}
		})
	}
}
