package scim

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var groupNameSeq atomic.Int64

func uniqueGroupName() string {
	return fmt.Sprintf("test-group-%d-%d", time.Now().UnixNano(), groupNameSeq.Add(1))
}

func newGroup() Group {
	return Group{
		Schemas:     []string{groupSchema},
		DisplayName: uniqueGroupName(),
	}
}

func createGroup(t *testing.T, in Group) Group {
	t.Helper()

	rr := do(t, http.MethodPost, "/Groups", in)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /Groups = %d, want 201: %s", rr.Code, rr.Body)
	}

	out := decodeBody[Group](t, rr)
	t.Cleanup(func() { hardDeleteGroup(t, out.ID) })
	return out
}

// The members attribute is multi-valued, so a group with none must still
// return `members: []` rather than dropping the key — some IdP clients (and
// the Entra validator) choke on the absent key. And excludedAttributes=members,
// which Okta and Entra send to skip large member lists, has to actually omit
// it. decodeBody can't tell `[]` from absent, so these assert on the raw body.
func TestGroupMembersSerialization(t *testing.T) {
	requireDB(t)

	t.Run("an empty group returns members as []", func(t *testing.T) {
		created := createGroup(t, newGroup())

		rr := do(t, http.MethodGet, "/Groups/"+created.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET = %d, want 200: %s", rr.Code, rr.Body)
		}
		if !strings.Contains(rr.Body.String(), `"members":[]`) {
			t.Errorf("empty group body has no `members: []`: %s", rr.Body)
		}
	})

	t.Run("a populated group returns its members", func(t *testing.T) {
		u := createUser(t, newUser())
		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u.ID}}})

		rr := do(t, http.MethodGet, "/Groups/"+created.ID, nil)
		body := rr.Body.String()
		if !strings.Contains(body, `"members":[`) || !strings.Contains(body, u.ID) {
			t.Errorf("populated group body is missing its member: %s", body)
		}
	})

	t.Run("excludedAttributes=members omits members entirely", func(t *testing.T) {
		u := createUser(t, newUser())
		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u.ID}}})

		rr := do(t, http.MethodGet, "/Groups/"+created.ID+"?excludedAttributes=members", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET = %d, want 200: %s", rr.Code, rr.Body)
		}
		if strings.Contains(rr.Body.String(), `"members"`) {
			t.Errorf("members should be excluded but is present: %s", rr.Body)
		}
	})

	t.Run("list honors excludedAttributes=members for every resource", func(t *testing.T) {
		u := createUser(t, newUser())
		createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u.ID}}})

		rr := do(t, http.MethodGet, "/Groups?excludedAttributes=members", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET = %d, want 200: %s", rr.Code, rr.Body)
		}
		if strings.Contains(rr.Body.String(), `"members"`) {
			t.Errorf("a listed group still carries members despite exclusion: %s", rr.Body)
		}
	})
}

// hardDeleteGroup is cleanup, not the behaviour under test: DeleteGroup is
// already a hard delete, this just catches groups a test didn't delete
// itself.
func hardDeleteGroup(t *testing.T, id string) {
	t.Helper()

	if _, err := cleanupPool.Exec(context.Background(), `DELETE FROM groups WHERE id = $1`, id); err != nil {
		t.Errorf("cleanup: delete group %q: %v", id, err)
	}
}

func TestCreateGroup(t *testing.T) {
	requireDB(t)

	t.Run("201 with a Location header", func(t *testing.T) {
		u := createUser(t, newUser())
		in := newGroup()
		in.Members = []Member{{Value: u.ID}}

		rr := do(t, http.MethodPost, "/Groups", in)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		t.Cleanup(func() { hardDeleteGroup(t, got.ID) })

		if got.ID == "" {
			t.Error("id is empty")
		}
		if got.DisplayName != in.DisplayName {
			t.Errorf("displayName = %q, want %q", got.DisplayName, in.DisplayName)
		}
		if len(got.Members) != 1 || got.Members[0].Value != u.ID {
			t.Errorf("members = %+v, want [%s]", got.Members, u.ID)
		}
		if got.Members[0].Ref == "" {
			t.Error("members[0].$ref is empty")
		}

		if got.Meta == nil || got.Meta.ResourceType != "Group" {
			t.Fatalf("meta = %+v, want resourceType Group", got.Meta)
		}
		if loc := rr.Header().Get("Location"); loc == "" || loc != got.Meta.Location {
			t.Errorf("Location = %q, meta.location = %q — want both set and equal", loc, got.Meta.Location)
		}
		if !strings.HasSuffix(got.Meta.Location, "/Groups/"+got.ID) {
			t.Errorf("meta.location = %q, want it to end in /Groups/%s", got.Meta.Location, got.ID)
		}
	})

	t.Run("a member id that doesn't exist is 400 invalidValue", func(t *testing.T) {
		in := newGroup()
		in.Members = []Member{{Value: nonexistentID}}

		rr := do(t, http.MethodPost, "/Groups", in)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[Error](t, rr).ScimType; got != "invalidValue" {
			t.Errorf("scimType = %q, want invalidValue", got)
		}
	})
}

func TestCreateGroupConflict(t *testing.T) {
	requireDB(t)

	existing := createGroup(t, newGroup())

	dup := newGroup()
	dup.DisplayName = existing.DisplayName

	rr := do(t, http.MethodPost, "/Groups", dup)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body)
	}
	if got := decodeBody[Error](t, rr).ScimType; got != "uniqueness" {
		t.Errorf("scimType = %q, want uniqueness", got)
	}
}

func TestCreateGroupBadRequest(t *testing.T) {
	requireDB(t)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing schemas", `{"displayName":"engineering"}`},
		{"missing displayName", `{"schemas":["` + groupSchema + `"]}`},
		{"not JSON", `{not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, http.MethodPost, "/Groups", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestGetGroup(t *testing.T) {
	requireDB(t)

	created := createGroup(t, newGroup())

	t.Run("returns the group", func(t *testing.T) {
		rr := do(t, http.MethodGet, "/Groups/"+created.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}
		got := decodeBody[Group](t, rr)
		if got.ID != created.ID {
			t.Errorf("id = %q, want %q", got.ID, created.ID)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		rr := do(t, http.MethodGet, "/Groups/"+nonexistentID, nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
		}
	})
}

func TestListGroups(t *testing.T) {
	requireDB(t)

	want := createGroup(t, newGroup())

	t.Run("appears in an unfiltered listing", func(t *testing.T) {
		rr := do(t, http.MethodGet, "/Groups", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		list := decodeBody[listOf[Group]](t, rr)
		if len(list.Schemas) != 1 || list.Schemas[0] != listSchema {
			t.Errorf("schemas = %v, want the ListResponse schema", list.Schemas)
		}

		found := false
		for _, g := range list.Resources {
			if g.ID == want.ID {
				found = true
			}
		}
		if !found {
			t.Error("created group is missing from the listing")
		}
	})

	t.Run("filters by displayName", func(t *testing.T) {
		rr := do(t, http.MethodGet, "/Groups?filter=displayName+eq+%22"+want.DisplayName+"%22", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		list := decodeBody[listOf[Group]](t, rr)
		if list.TotalResults != 1 || len(list.Resources) != 1 || list.Resources[0].ID != want.ID {
			t.Errorf("filtered listing = %+v, want just %q", list.Resources, want.ID)
		}
	})

	t.Run("an unsupported filter is 400 invalidFilter", func(t *testing.T) {
		rr := do(t, http.MethodGet, `/Groups?filter=members+eq+%22x%22`, nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
		}
	})
}

func TestReplaceGroup(t *testing.T) {
	requireDB(t)

	t.Run("replaces displayName, externalId and the whole membership set", func(t *testing.T) {
		u1 := createUser(t, newUser())
		u2 := createUser(t, newUser())

		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u1.ID}}})

		in := Group{
			Schemas:     []string{groupSchema},
			DisplayName: uniqueGroupName(),
			ExternalID:  "ext-123",
			Members:     []Member{{Value: u2.ID}},
		}
		rr := do(t, http.MethodPut, "/Groups/"+created.ID, in)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if got.DisplayName != in.DisplayName {
			t.Errorf("displayName = %q, want %q", got.DisplayName, in.DisplayName)
		}
		if got.ExternalID != "ext-123" {
			t.Errorf("externalId = %q, want ext-123", got.ExternalID)
		}
		if len(got.Members) != 1 || got.Members[0].Value != u2.ID {
			t.Errorf("members = %+v, want just [%s] — a full replace drops the old member", got.Members, u2.ID)
		}
	})

	t.Run("id in the body must match the path", func(t *testing.T) {
		created := createGroup(t, newGroup())

		in := newGroup()
		in.ID = nonexistentID
		rr := do(t, http.MethodPut, "/Groups/"+created.ID, in)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		rr := do(t, http.MethodPut, "/Groups/"+nonexistentID, newGroup())
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
		}
	})
}

func TestPatchGroupMembers(t *testing.T) {
	requireDB(t)

	t.Run("add appends a member", func(t *testing.T) {
		u := createUser(t, newUser())
		created := createGroup(t, newGroup())

		body := map[string]any{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{
				{"op": "add", "path": "members", "value": []map[string]string{{"value": u.ID}}},
			},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if len(got.Members) != 1 || got.Members[0].Value != u.ID {
			t.Errorf("members = %+v, want [%s]", got.Members, u.ID)
		}
	})

	t.Run("remove with a value filter drops one member", func(t *testing.T) {
		u1 := createUser(t, newUser())
		u2 := createUser(t, newUser())
		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u1.ID}, {Value: u2.ID}}})

		body := map[string]any{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{
				{"op": "remove", "path": `members[value eq "` + u1.ID + `"]`},
			},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if len(got.Members) != 1 || got.Members[0].Value != u2.ID {
			t.Errorf("members = %+v, want just [%s]", got.Members, u2.ID)
		}
	})

	t.Run("remove with a bare path clears every member", func(t *testing.T) {
		u := createUser(t, newUser())
		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u.ID}}})

		body := map[string]any{
			"schemas":    []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{{"op": "remove", "path": "members"}},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if len(got.Members) != 0 {
			t.Errorf("members = %+v, want empty", got.Members)
		}
	})

	t.Run("replace overwrites the whole set", func(t *testing.T) {
		u1 := createUser(t, newUser())
		u2 := createUser(t, newUser())
		created := createGroup(t, Group{Schemas: []string{groupSchema}, DisplayName: uniqueGroupName(), Members: []Member{{Value: u1.ID}}})

		body := map[string]any{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{
				{"op": "replace", "path": "members", "value": []map[string]string{{"value": u2.ID}}},
			},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if len(got.Members) != 1 || got.Members[0].Value != u2.ID {
			t.Errorf("members = %+v, want just [%s]", got.Members, u2.ID)
		}
	})

	t.Run("a pathless replace can update displayName and members together", func(t *testing.T) {
		u := createUser(t, newUser())
		created := createGroup(t, newGroup())
		newName := uniqueGroupName()

		body := map[string]any{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{
				{"op": "replace", "value": map[string]any{
					"displayName": newName,
					"members":     []map[string]string{{"value": u.ID}},
				}},
			},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		got := decodeBody[Group](t, rr)
		if got.DisplayName != newName {
			t.Errorf("displayName = %q, want %q", got.DisplayName, newName)
		}
		if len(got.Members) != 1 || got.Members[0].Value != u.ID {
			t.Errorf("members = %+v, want [%s]", got.Members, u.ID)
		}
	})

	t.Run("an unsupported path is 400 invalidPath", func(t *testing.T) {
		created := createGroup(t, newGroup())

		body := map[string]any{
			"schemas":    []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{{"op": "replace", "path": "nickname", "value": "x"}},
		}
		rr := do(t, http.MethodPatch, "/Groups/"+created.ID, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[Error](t, rr).ScimType; got != "invalidPath" {
			t.Errorf("scimType = %q, want invalidPath", got)
		}
	})
}

// Unlike DELETE /Users, there is no "survives as inactive": the Group schema
// has no active attribute, so this is a real deletion.
func TestDeleteGroup(t *testing.T) {
	requireDB(t)

	t.Run("204 and the group is actually gone", func(t *testing.T) {
		created := createGroup(t, newGroup())

		rr := do(t, http.MethodDelete, "/Groups/"+created.ID, nil)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", rr.Code, rr.Body)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rr.Body)
		}

		rr = do(t, http.MethodGet, "/Groups/"+created.ID, nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET after delete = %d, want 404 — the row should be gone: %s", rr.Code, rr.Body)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		rr := do(t, http.MethodDelete, "/Groups/"+nonexistentID, nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
		}
	})
}
