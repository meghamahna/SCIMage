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
	if len(list.Resources) != 1 {
		t.Fatalf("got %d resource types, want 1", len(list.Resources))
	}

	got := list.Resources[0]
	if got.Endpoint != "/Users" {
		t.Errorf("endpoint = %q, want /Users", got.Endpoint)
	}
	if got.Schema != userSchema {
		t.Errorf("schema = %q, want %q", got.Schema, userSchema)
	}
}

func TestSchemas(t *testing.T) {
	requireDB(t)

	rr := do(t, http.MethodGet, "/Schemas", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	list := decodeBody[listOf[resourceSchema]](t, rr)
	if len(list.Resources) != 1 {
		t.Fatalf("got %d schemas, want 1", len(list.Resources))
	}
	if list.Resources[0].ID != userSchema {
		t.Errorf("id = %q, want %q", list.Resources[0].ID, userSchema)
	}

	byName := make(map[string]schemaAttribute, len(list.Resources[0].Attributes))
	for _, a := range list.Resources[0].Attributes {
		byName[a.Name] = a
	}

	// Every declared attribute has to be one the server actually stores.
	for _, want := range []string{"userName", "name", "emails", "active"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("schema is missing %q", want)
		}
	}

	// userName's characteristics are what tell a client that bjensen and
	// BJensen are one identity, which is how the store behaves.
	userName := byName["userName"]
	if !userName.Required {
		t.Error("userName.required = false, want true")
	}
	if userName.CaseExact {
		t.Error("userName.caseExact = true, want false — uniqueness is on lower(user_name)")
	}
	if userName.Uniqueness != "server" {
		t.Errorf("userName.uniqueness = %q, want server", userName.Uniqueness)
	}
}

// Discovery describes the server, so it sits behind the same bearer check as
// everything else.
func TestDiscoveryRequiresAuth(t *testing.T) {
	routes := NewHandler(nil, authTestToken).Routes()

	for _, path := range []string{"/ServiceProviderConfig", "/ResourceTypes", "/Schemas"} {
		t.Run(path, func(t *testing.T) {
			rr := sendUnauthenticated(t, routes, http.MethodGet, path)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a token", rr.Code)
			}
		})
	}
}
