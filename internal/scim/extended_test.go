package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const enterpriseURN = "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"

// extendedRoutes builds a handler with the extensible-attribute pass-through
// turned on, over the same store/tenant/token the rest of the suite uses. The
// shared handler keeps the feature off, which the flag-off subtest relies on.
func extendedRoutes(t *testing.T) http.Handler {
	t.Helper()
	h := NewHandler(testStore, testStore, testStore, testStore)
	h.extended = true
	return h.Routes()
}

func registerAttr(t *testing.T, name string) {
	t.Helper()
	if _, err := testStore.RegisterAttribute(t.Context(), testTenantID, name, "string", "test"); err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
	t.Cleanup(func() {
		_ = testStore.UnregisterAttribute(context.Background(), testTenantID, name, "test")
	})
}

func doAt(t *testing.T, routes http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r *bytes.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/scim/v2/"+testTenantID+target, r)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+testToken)

	rr := httptest.NewRecorder()
	routes.ServeHTTP(rr, req)
	return rr
}

func decodeMap(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	return m
}

func hasSchema(m map[string]any, urn string) bool {
	schemas, _ := m["schemas"].([]any)
	for _, s := range schemas {
		if s == urn {
			return true
		}
	}
	return false
}

func TestExtendedAttributes(t *testing.T) {
	requireDB(t)

	routes := extendedRoutes(t)
	registerAttr(t, "displayName")
	registerAttr(t, enterpriseURN)

	createBody := func() (userName, body string) {
		userName = uniqueUserName()
		body = `{
		  "schemas":["` + userSchema + `","` + enterpriseURN + `"],
		  "userName":"` + userName + `",
		  "displayName":"Dana Scully",
		  "title":"unregistered, should be dropped",
		  "` + enterpriseURN + `":{"department":"X-Files"}
		}`
		return
	}

	t.Run("captures registered attributes and drops the rest on create", func(t *testing.T) {
		_, body := createBody()
		rr := doAt(t, routes, http.MethodPost, "/Users", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST = %d, want 201: %s", rr.Code, rr.Body)
		}
		m := decodeMap(t, rr)
		t.Cleanup(func() { hardDelete(t, m["id"].(string)) })

		if m["displayName"] != "Dana Scully" {
			t.Errorf("displayName = %v, want Dana Scully", m["displayName"])
		}
		if _, ok := m["title"]; ok {
			t.Error("title was returned, but it is not registered — it should be dropped")
		}
		ent, ok := m[enterpriseURN].(map[string]any)
		if !ok || ent["department"] != "X-Files" {
			t.Errorf("enterprise extension = %v, want department X-Files", m[enterpriseURN])
		}
		// A present extension has to be declared in schemas.
		if !hasSchema(m, enterpriseURN) {
			t.Errorf("schemas %v is missing the enterprise URN", m["schemas"])
		}
	})

	t.Run("round-trips on a later GET", func(t *testing.T) {
		_, body := createBody()
		created := decodeMap(t, doAt(t, routes, http.MethodPost, "/Users", body))
		id := created["id"].(string)
		t.Cleanup(func() { hardDelete(t, id) })

		m := decodeMap(t, doAt(t, routes, http.MethodGet, "/Users/"+id, ""))
		if m["displayName"] != "Dana Scully" {
			t.Errorf("displayName after GET = %v, want Dana Scully", m["displayName"])
		}
	})

	t.Run("PATCH replaces then removes a registered attribute", func(t *testing.T) {
		_, body := createBody()
		id := decodeMap(t, doAt(t, routes, http.MethodPost, "/Users", body))["id"].(string)
		t.Cleanup(func() { hardDelete(t, id) })

		replace := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"displayName","value":"Fox Mulder"}]}`
		m := decodeMap(t, doAt(t, routes, http.MethodPatch, "/Users/"+id, replace))
		if m["displayName"] != "Fox Mulder" {
			t.Errorf("displayName after PATCH = %v, want Fox Mulder", m["displayName"])
		}

		remove := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"remove","path":"displayName"}]}`
		m = decodeMap(t, doAt(t, routes, http.MethodPatch, "/Users/"+id, remove))
		if _, ok := m["displayName"]; ok {
			t.Errorf("displayName still present after remove: %v", m["displayName"])
		}
	})

	t.Run("a core-only PATCH preserves extended attributes", func(t *testing.T) {
		_, body := createBody()
		id := decodeMap(t, doAt(t, routes, http.MethodPost, "/Users", body))["id"].(string)
		t.Cleanup(func() { hardDelete(t, id) })

		patch := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`
		m := decodeMap(t, doAt(t, routes, http.MethodPatch, "/Users/"+id, patch))
		if m["active"] != false {
			t.Errorf("active = %v, want false", m["active"])
		}
		if m["displayName"] != "Dana Scully" {
			t.Errorf("displayName = %v, want it preserved through a core-only PATCH", m["displayName"])
		}
	})

	t.Run("/Schemas advertises registered attributes", func(t *testing.T) {
		rr := doAt(t, routes, http.MethodGet, "/Schemas", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /Schemas = %d, want 200", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), `"displayName"`) {
			t.Errorf("/Schemas does not advertise displayName: %s", rr.Body)
		}
	})

	// The flag-off guarantee: the shared suite handler has the feature disabled,
	// so a registered attribute is ignored — exactly today's behaviour.
	t.Run("with the feature off, a registered attribute is dropped", func(t *testing.T) {
		body := `{"schemas":["` + userSchema + `"],"userName":"` + uniqueUserName() + `","displayName":"ignored"}`
		rr := do(t, http.MethodPost, "/Users", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST = %d, want 201: %s", rr.Code, rr.Body)
		}
		m := decodeMap(t, rr)
		t.Cleanup(func() { hardDelete(t, m["id"].(string)) })

		if _, ok := m["displayName"]; ok {
			t.Error("displayName was captured with the feature off")
		}
	})
}
