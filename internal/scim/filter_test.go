package scim

// Filtering exists so an identity provider can ask "do you already have this
// user?" before creating one. A wrong answer here makes a client update or
// deactivate the wrong person, so the cases below cover both the match and the
// refusals.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestParseFilter(t *testing.T) {
	t.Run("supported expressions", func(t *testing.T) {
		for _, tc := range []struct {
			expr            string
			userName, extID string
		}{
			{`userName eq "jdoe"`, "jdoe", ""},
			{`userName eq "joanne@hirthe.biz"`, "joanne@hirthe.biz", ""},
			{`UserName eq "jdoe"`, "jdoe", ""}, // attribute names are case-insensitive
			{`externalId eq "0oa1b2c3"`, "", "0oa1b2c3"},
			{`  userName   eq   "jdoe"  `, "jdoe", ""},
			{"", "", ""}, // absent filter lists everything
		} {
			got, err := parseFilter(tc.expr)
			if err != nil {
				t.Errorf("parseFilter(%q): %v", tc.expr, err)
				continue
			}
			if got.UserName != tc.userName || got.ExternalID != tc.extID {
				t.Errorf("parseFilter(%q) = %+v, want userName=%q externalId=%q",
					tc.expr, got, tc.userName, tc.extID)
			}
		}
	})

	// Anything beyond the supported shape is refused, never approximated.
	t.Run("refused expressions", func(t *testing.T) {
		for _, expr := range []string{
			`displayName eq "jdoe"`,              // attribute isn't stored
			`userName co "jdoe"`,                 // operator beyond eq
			`userName sw "j"`,                    //
			`userName pr`,                        //
			`userName eq "a" and active eq true`, // compound
			`userName eq jdoe`,                   // unquoted value
			`userName eq ""`,                     // empty value
			`nonsense`,                           //
		} {
			if got, err := parseFilter(expr); err == nil {
				t.Errorf("parseFilter(%q) = %+v, want an error", expr, got)
			}
		}
	})
}

func TestListUsersFiltered(t *testing.T) {
	requireDB(t)

	in := newUser()
	in.ExternalID = fmt.Sprintf("ext-%s", strings.TrimPrefix(in.UserName, "test-"))
	created := createUser(t, in)

	// A second user, so a match proves selection rather than "everything".
	other := createUser(t, newUser())

	matched := func(t *testing.T, query string) ListResponse {
		t.Helper()

		rr := do(t, http.MethodGet, "/Users?filter="+query, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
		}
		return decodeBody[ListResponse](t, rr)
	}

	t.Run("userName eq returns only that user", func(t *testing.T) {
		list := matched(t, `userName+eq+%22`+created.UserName+`%22`)

		if list.TotalResults != 1 || len(list.Resources) != 1 {
			t.Fatalf("totalResults=%d resources=%d, want 1 and 1", list.TotalResults, len(list.Resources))
		}
		if list.Resources[0].ID != created.ID {
			t.Errorf("matched %s, want %s", list.Resources[0].ID, created.ID)
		}
		if list.Resources[0].ID == other.ID {
			t.Error("the filter returned the wrong user")
		}
	})

	// Uniqueness is enforced on lower(user_name), so a lookup has to agree with
	// what a create would have rejected.
	t.Run("userName eq is case-insensitive", func(t *testing.T) {
		list := matched(t, `userName+eq+%22`+strings.ToUpper(created.UserName)+`%22`)

		if list.TotalResults != 1 {
			t.Fatalf("totalResults = %d, want 1 — a differently-cased name is the same identity", list.TotalResults)
		}
		if list.Resources[0].ID != created.ID {
			t.Errorf("matched %s, want %s", list.Resources[0].ID, created.ID)
		}
	})

	t.Run("externalId eq returns that user", func(t *testing.T) {
		list := matched(t, `externalId+eq+%22`+created.ExternalID+`%22`)

		if list.TotalResults != 1 || len(list.Resources) != 1 {
			t.Fatalf("totalResults=%d resources=%d, want 1 and 1", list.TotalResults, len(list.Resources))
		}
		if list.Resources[0].ID != created.ID {
			t.Errorf("matched %s, want %s", list.Resources[0].ID, created.ID)
		}
	})

	// "No such user" is an empty list, not a 404 — the client is asking whether
	// one exists, and 404 would read as the endpoint being wrong.
	t.Run("no match is an empty list", func(t *testing.T) {
		list := matched(t, `userName+eq+%22nobody-`+created.UserName+`%22`)

		if list.TotalResults != 0 || len(list.Resources) != 0 {
			t.Errorf("totalResults=%d resources=%d, want 0 and 0", list.TotalResults, len(list.Resources))
		}
	})

	t.Run("unsupported filters are 400 invalidFilter", func(t *testing.T) {
		for _, query := range []string{
			`displayName+eq+%22jdoe%22`,
			`userName+co+%22jdoe%22`,
			`userName+pr`,
		} {
			rr := do(t, http.MethodGet, "/Users?filter="+query, nil)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("filter=%s gave %d, want 400", query, rr.Code)
				continue
			}
			if got := decodeBody[Error](t, rr).ScimType; got != "invalidFilter" {
				t.Errorf("filter=%s scimType = %q, want invalidFilter", query, got)
			}
		}
	})
}

// Some identity providers send emails[].primary as the string "true" rather
// than a boolean. RFC 7643 types it as boolean, so reading the spec alone
// leaves this broken: the decode fails and every create returns 400.
func TestAcceptsQuotedBooleans(t *testing.T) {
	requireDB(t)

	body := fmt.Sprintf(`{
	  "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
	  "externalId": %q,
	  "userName": %q,
	  "name": {"givenName": "Santino", "familyName": "Jerrell"},
	  "emails": [{"primary": "true", "value": "abelardo@example.com"}],
	  "active": "true"
	}`, "ext-"+uniqueUserName(), uniqueUserName())

	rr := do(t, http.MethodPost, "/Users", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
	}

	out := decodeBody[User](t, rr)
	t.Cleanup(func() { hardDelete(t, out.ID) })

	if !active(t, out) {
		t.Error(`active = false, want true from the string "true"`)
	}
	if len(out.Emails) != 1 || out.Emails[0].Value != "abelardo@example.com" {
		t.Errorf("emails = %+v, want the address from a string-primary entry", out.Emails)
	}

	// Responses stay spec-correct: a real boolean goes back out.
	if !strings.Contains(rr.Body.String(), `"active":true`) {
		t.Errorf("response should encode active as a boolean, got %s", rr.Body)
	}
}

func TestExternalID(t *testing.T) {
	requireDB(t)

	t.Run("round-trips on create", func(t *testing.T) {
		in := newUser()
		in.ExternalID = "0oa" + strings.TrimPrefix(in.UserName, "test-")

		created := createUser(t, in)
		if created.ExternalID != in.ExternalID {
			t.Errorf("externalId = %q, want %q", created.ExternalID, in.ExternalID)
		}

		got := decodeBody[User](t, do(t, http.MethodGet, "/Users/"+created.ID, nil))
		if got.ExternalID != in.ExternalID {
			t.Errorf("externalId after fetch = %q, want %q", got.ExternalID, in.ExternalID)
		}
	})

	t.Run("is absent when the client omits it", func(t *testing.T) {
		created := createUser(t, newUser())

		if created.ExternalID != "" {
			t.Errorf("externalId = %q, want empty", created.ExternalID)
		}
	})
}
