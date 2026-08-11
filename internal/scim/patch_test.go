package scim

// PATCH is how identity providers deprovision, so these cases mirror the
// operations real clients send.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// patchUser sends a PatchOp body and returns the response.
func patchUser(t *testing.T, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, http.MethodPatch, "/Users/"+id, body)
}

const patchEnvelope = `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[%s]}`

func TestPatchDisableUser(t *testing.T) {
	requireDB(t)

	created := createUser(t, newUser())
	if !active(t, created) {
		t.Fatal("user should start active")
	}

	// The shape providers send to deprovision.
	body := fmt.Sprintf(patchEnvelope, `{"op":"replace","path":"active","value":false}`)

	rr := patchUser(t, created.ID, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	if active(t, decodeBody[User](t, rr)) {
		t.Error("active = true in the response, want false")
	}

	// Providers re-read by id and by filter after disabling, and expect both to
	// agree.
	got := decodeBody[User](t, do(t, http.MethodGet, "/Users/"+created.ID, nil))
	if active(t, got) {
		t.Error("fetch by id returned active = true after disabling")
	}

	list := decodeBody[ListResponse](t, do(t, http.MethodGet,
		"/Users?filter=userName+eq+%22"+created.UserName+"%22", nil))
	if len(list.Resources) != 1 {
		t.Fatalf("filter returned %d users, want 1", len(list.Resources))
	}
	if list.Resources[0].Active == nil || bool(*list.Resources[0].Active) {
		t.Error("fetch by filter returned active = true after disabling")
	}
}

func TestPatchReplaceAttributes(t *testing.T) {
	requireDB(t)

	created := createUser(t, newUser())

	newExternal := "ext-" + uniqueUserName()
	body := fmt.Sprintf(patchEnvelope, fmt.Sprintf(
		`{"op":"replace","path":"externalId","value":%q},`+
			`{"op":"replace","path":"name.givenName","value":"Kylie"},`+
			`{"op":"replace","path":"name.familyName","value":"Rollin"},`+
			`{"op":"replace","path":"emails[primary eq true].value","value":"kylie@example.com"}`,
		newExternal))

	rr := patchUser(t, created.ID, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	out := decodeBody[User](t, rr)
	if out.ExternalID != newExternal {
		t.Errorf("externalId = %q, want %q", out.ExternalID, newExternal)
	}
	if out.Name == nil || out.Name.GivenName != "Kylie" || out.Name.FamilyName != "Rollin" {
		t.Errorf("name = %+v, want Kylie Rollin", out.Name)
	}
	if len(out.Emails) != 1 || out.Emails[0].Value != "kylie@example.com" {
		t.Errorf("emails = %+v, want kylie@example.com", out.Emails)
	}

	// Attributes the patch didn't mention survive — that's what makes it a
	// partial update rather than a replace.
	if out.UserName != created.UserName {
		t.Errorf("userName = %q, want it unchanged at %q", out.UserName, created.UserName)
	}
}

func TestPatchUserName(t *testing.T) {
	requireDB(t)

	created := createUser(t, newUser())
	renamed := uniqueUserName()

	rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope,
		fmt.Sprintf(`{"op":"replace","path":"userName","value":%q}`, renamed)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	if got := decodeBody[User](t, rr).UserName; got != renamed {
		t.Errorf("userName = %q, want %q", got, renamed)
	}

	// The rename has to be findable by the new name, since that's how the
	// client reconciles afterwards.
	list := decodeBody[ListResponse](t, do(t, http.MethodGet,
		"/Users?filter=userName+eq+%22"+renamed+"%22", nil))
	if len(list.Resources) != 1 || list.Resources[0].ID != created.ID {
		t.Errorf("filter by the new userName returned %+v, want the renamed user", list.Resources)
	}
}

func TestPatchVariants(t *testing.T) {
	requireDB(t)

	t.Run("op is case-insensitive", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope,
			`{"op":"Replace","path":"name.givenName","value":"Casey"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[User](t, rr); got.Name == nil || got.Name.GivenName != "Casey" {
			t.Errorf("name = %+v, want givenName Casey", got.Name)
		}
	})

	t.Run("a quoted boolean disables the user", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope,
			`{"op":"replace","path":"active","value":"False"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}
		if active(t, decodeBody[User](t, rr)) {
			t.Error(`active = true, want false from the string "False"`)
		}
	})

	// Some clients omit the path and send an object of attributes instead.
	t.Run("a pathless replace applies each attribute", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope,
			`{"op":"replace","value":{"active":false,"name.givenName":"Jo"}}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}

		out := decodeBody[User](t, rr)
		if active(t, out) {
			t.Error("active = true, want false")
		}
		if out.Name == nil || out.Name.GivenName != "Jo" {
			t.Errorf("name = %+v, want givenName Jo", out.Name)
		}
	})

	t.Run("remove clears an attribute", func(t *testing.T) {
		created := createUser(t, newUser())

		rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope,
			`{"op":"remove","path":"name.givenName"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[User](t, rr); got.Name != nil && got.Name.GivenName != "" {
			t.Errorf("givenName = %q, want it cleared", got.Name.GivenName)
		}
	})
}

func TestPatchRefusals(t *testing.T) {
	requireDB(t)

	created := createUser(t, newUser())

	for _, tc := range []struct {
		name, ops, scimType string
	}{
		{"unknown path", `{"op":"replace","path":"department","value":"Eng"}`, "invalidPath"},
		{"unknown op", `{"op":"increment","path":"active","value":true}`, "invalidValue"},
		{"wrong value type", `{"op":"replace","path":"userName","value":42}`, "invalidValue"},
		{"no operations", ``, "invalidValue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := patchUser(t, created.ID, fmt.Sprintf(patchEnvelope, tc.ops))

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body)
			}
			if got := decodeBody[Error](t, rr).ScimType; got != tc.scimType {
				t.Errorf("scimType = %q, want %q", got, tc.scimType)
			}
		})
	}

	t.Run("unknown user is 404", func(t *testing.T) {
		rr := patchUser(t, nonexistentID, fmt.Sprintf(patchEnvelope,
			`{"op":"replace","path":"active","value":false}`))

		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("renaming onto a taken userName is 409", func(t *testing.T) {
		taken := createUser(t, newUser())
		mover := createUser(t, newUser())

		rr := patchUser(t, mover.ID, fmt.Sprintf(patchEnvelope,
			fmt.Sprintf(`{"op":"replace","path":"userName","value":%q}`, taken.UserName)))

		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body)
		}
		if got := decodeBody[Error](t, rr).ScimType; got != "uniqueness" {
			t.Errorf("scimType = %q, want uniqueness", got)
		}
	})
}
