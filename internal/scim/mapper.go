package scim

import (
	"strings"

	"github.com/meghamahna/SCIMage/internal/store"
)

// toStoreUser flattens a SCIM resource onto a row. The schema keeps one email,
// so a multi-valued emails array collapses to its primary.
func toStoreUser(u User) store.User {
	su := store.User{
		UserName:   strings.TrimSpace(u.UserName),
		ExternalID: nonEmpty(strings.TrimSpace(u.ExternalID)),
		Active:     true,
		Email:      nonEmpty(primaryEmail(u.Emails)),
	}
	if u.Active != nil {
		su.Active = bool(*u.Active)
	}
	if u.Name != nil {
		su.GivenName = nonEmpty(strings.TrimSpace(u.Name.GivenName))
		su.FamilyName = nonEmpty(strings.TrimSpace(u.Name.FamilyName))
	}
	return su
}

func fromStoreUser(su *store.User, baseURL string) User {
	active := Bool(su.Active)
	u := User{
		Schemas:  []string{userSchema},
		ID:       su.ID,
		UserName: su.UserName,
		Active:   &active,
		Meta: &Meta{
			ResourceType: "User",
			Created:      su.CreatedAt,
			LastModified: su.UpdatedAt,
			Location:     baseURL + "/Users/" + su.ID,
		},
	}

	if su.GivenName != nil || su.FamilyName != nil {
		u.Name = &Name{}
		if su.GivenName != nil {
			u.Name.GivenName = *su.GivenName
		}
		if su.FamilyName != nil {
			u.Name.FamilyName = *su.FamilyName
		}
	}
	if su.Email != nil {
		u.Emails = []Email{{Value: *su.Email, Primary: true}}
	}
	if su.ExternalID != nil {
		u.ExternalID = *su.ExternalID
	}

	return u
}

func primaryEmail(emails []Email) string {
	for _, e := range emails {
		if e.Primary {
			return strings.TrimSpace(e.Value)
		}
	}
	if len(emails) > 0 {
		return strings.TrimSpace(emails[0].Value)
	}
	return ""
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
