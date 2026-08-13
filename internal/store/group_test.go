package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var groupNameSeq atomic.Int64

func uniqueGroupName() string {
	return fmt.Sprintf("test-group-%d-%d", time.Now().UnixNano(), groupNameSeq.Add(1))
}

func createGroup(t *testing.T, s *Store, tenantID string, g *Group) *Group {
	t.Helper()

	created, err := s.CreateGroup(context.Background(), tenantID, g, testAudit(tenantID))
	if err != nil {
		t.Fatalf("CreateGroup(%q): %v", g.DisplayName, err)
	}
	return created
}

// Pages the whole table, mirroring allUsers.
func allGroups(t *testing.T, s *Store, tenantID string) ([]Group, int) {
	t.Helper()

	var all []Group
	total := 0

	for offset := 0; ; offset += 50 {
		page, pageTotal, err := s.ListGroups(context.Background(), tenantID, 50, offset, GroupFilter{})
		if err != nil {
			t.Fatalf("ListGroups(50, %d): %v", offset, err)
		}
		total = pageTotal
		if len(page) == 0 {
			return all, total
		}
		all = append(all, page...)
	}
}

func TestCreateGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("returns the stored row", func(t *testing.T) {
		name := uniqueGroupName()
		created := createGroup(t, s, tenantID, &Group{DisplayName: name, ExternalID: ptr("ext-1")})

		if created.ID == "" {
			t.Error("ID is empty — the database should have assigned one")
		}
		if created.DisplayName != name {
			t.Errorf("DisplayName = %q, want %q", created.DisplayName, name)
		}
		if created.ExternalID == nil || *created.ExternalID != "ext-1" {
			t.Errorf("ExternalID = %v, want ext-1", created.ExternalID)
		}
		if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
			t.Errorf("timestamps not set: created=%v updated=%v", created.CreatedAt, created.UpdatedAt)
		}
		if len(created.Members) != 0 {
			t.Errorf("Members = %v, want empty", created.Members)
		}
	})

	t.Run("creates with initial members", func(t *testing.T) {
		u1 := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		u2 := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u1.ID, u2.ID}})

		if len(created.Members) != 2 {
			t.Fatalf("Members = %v, want 2 entries", created.Members)
		}
	})

	t.Run("dedupes repeated member ids", func(t *testing.T) {
		u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u.ID, u.ID}})

		if len(created.Members) != 1 {
			t.Errorf("Members = %v, want exactly one entry", created.Members)
		}
	})

	t.Run("a member id that doesn't exist is ErrInvalidMember", func(t *testing.T) {
		_, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{nonexistentID}}, testAudit(tenantID))
		if !errors.Is(err, ErrInvalidMember) {
			t.Fatalf("error = %v, want ErrInvalidMember", err)
		}
	})

	t.Run("a member id from another tenant is ErrInvalidMember", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		foreignUser := createUser(t, s, otherTenant, &User{UserName: uniqueUserName(), Active: true})

		_, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{foreignUser.ID}}, testAudit(tenantID))
		if !errors.Is(err, ErrInvalidMember) {
			t.Fatalf("error = %v, want ErrInvalidMember", err)
		}
	})

	t.Run("a malformed member id is ErrInvalidMember, not a 500", func(t *testing.T) {
		_, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{"not-a-uuid"}}, testAudit(tenantID))
		if !errors.Is(err, ErrInvalidMember) {
			t.Fatalf("error = %v, want ErrInvalidMember", err)
		}
	})

	t.Run("an invalid member rolls back the group row too", func(t *testing.T) {
		name := uniqueGroupName()
		if _, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: name, Members: []string{nonexistentID}}, testAudit(tenantID)); !errors.Is(err, ErrInvalidMember) {
			t.Fatalf("error = %v, want ErrInvalidMember", err)
		}

		// The name must be free to use again — nothing should have committed.
		if _, err := s.CreateGroup(ctx, tenantID, &Group{DisplayName: name}, testAudit(tenantID)); err != nil {
			t.Fatalf("CreateGroup after a rolled-back attempt: %v, want success", err)
		}
	})
}

// displayName is caseExact-agnostic uniqueness by decision, the same
// reconciliation reasoning userName already uses.
func TestCreateGroupDuplicateDisplayName(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)

	base := uniqueGroupName()
	createGroup(t, s, tenantID, &Group{DisplayName: base})

	tests := []struct {
		name        string
		displayName string
	}{
		{"exact duplicate", base},
		{"uppercased", strings.ToUpper(base)},
		{"mixed case", strings.Replace(base, "test", "TeSt", 1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateGroup(context.Background(), tenantID, &Group{DisplayName: tc.displayName}, testAudit(tenantID))
			if !errors.Is(err, ErrDuplicateGroupName) {
				t.Fatalf("CreateGroup(%q) error = %v, want ErrDuplicateGroupName", tc.displayName, err)
			}
		})
	}
}

func TestCreateGroupSameNameDifferentTenants(t *testing.T) {
	s := newTestStore(t)
	tenantA := newTestTenant(t, s)
	tenantB := newTestTenant(t, s)

	name := uniqueGroupName()
	createGroup(t, s, tenantA, &Group{DisplayName: name})

	if _, err := s.CreateGroup(context.Background(), tenantB, &Group{DisplayName: name}, testAudit(tenantB)); err != nil {
		t.Fatalf("CreateGroup(%q) in a different tenant: %v, want success", name, err)
	}
}

func TestGetGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u.ID}})

	t.Run("returns an existing group with its members", func(t *testing.T) {
		got, err := s.GetGroup(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetGroup(%q): %v", created.ID, err)
		}
		if got.DisplayName != created.DisplayName {
			t.Errorf("DisplayName = %q, want %q", got.DisplayName, created.DisplayName)
		}
		if len(got.Members) != 1 || got.Members[0] != u.ID {
			t.Errorf("Members = %v, want [%s]", got.Members, u.ID)
		}
	})

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"unknown id", nonexistentID},
		{"malformed id", "not-a-uuid"},
		{"empty id", ""},
	} {
		t.Run(tc.name+" is ErrGroupNotFound", func(t *testing.T) {
			if _, err := s.GetGroup(ctx, tenantID, tc.id); !errors.Is(err, ErrGroupNotFound) {
				t.Fatalf("GetGroup(%q) error = %v, want ErrGroupNotFound", tc.id, err)
			}
		})
	}

	t.Run("another tenant's real id is ErrGroupNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		if _, err := s.GetGroup(ctx, otherTenant, created.ID); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("GetGroup(%q) from another tenant error = %v, want ErrGroupNotFound", created.ID, err)
		}
	})
}

func TestListGroups(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	_, before, err := s.ListGroups(ctx, tenantID, 1, 0, GroupFilter{})
	if err != nil {
		t.Fatalf("ListGroups baseline: %v", err)
	}
	if before != 0 {
		t.Fatalf("baseline total = %d, want 0 for a fresh tenant", before)
	}

	const created = 3
	for range created {
		createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})
	}

	t.Run("total reflects every row", func(t *testing.T) {
		_, total, err := s.ListGroups(ctx, tenantID, 1, 0, GroupFilter{})
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		if total != created {
			t.Errorf("total = %d, want %d", total, created)
		}
	})

	t.Run("limit caps the page", func(t *testing.T) {
		groups, _, err := s.ListGroups(ctx, tenantID, 2, 0, GroupFilter{})
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		if len(groups) != 2 {
			t.Errorf("got %d groups, want 2", len(groups))
		}
	})

	t.Run("paging covers every row exactly once", func(t *testing.T) {
		all, total := allGroups(t, s, tenantID)

		if len(all) != total {
			t.Errorf("paged over %d rows, but total is %d", len(all), total)
		}

		seen := make(map[string]bool, len(all))
		for _, g := range all {
			if seen[g.ID] {
				t.Errorf("group %s appeared on more than one page", g.ID)
			}
			seen[g.ID] = true
		}
	})

	t.Run("filters by displayName", func(t *testing.T) {
		want := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})

		groups, total, err := s.ListGroups(ctx, tenantID, 10, 0, GroupFilter{DisplayName: want.DisplayName})
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		if total != 1 || len(groups) != 1 || groups[0].ID != want.ID {
			t.Errorf("filtered listing = %+v (total %d), want just %q", groups, total, want.ID)
		}
	})

	t.Run("negative limit is rejected", func(t *testing.T) {
		if _, _, err := s.ListGroups(ctx, tenantID, -1, 0, GroupFilter{}); err == nil {
			t.Error("expected an error for a negative limit, got nil")
		}
	})
}

func TestUpdateGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("replaces displayName, externalId and the whole membership set", func(t *testing.T) {
		u1 := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		u2 := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u1.ID}})

		newName := uniqueGroupName()
		changed, err := s.UpdateGroup(ctx, tenantID, created.ID, &Group{
			DisplayName: newName,
			ExternalID:  ptr("ext-2"),
			Members:     []string{u2.ID},
		}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("UpdateGroup: %v", err)
		}

		if changed.Before == nil || len(changed.Before.Members) != 1 || changed.Before.Members[0] != u1.ID {
			t.Errorf("Before.Members = %v, want [%s]", changed.Before, u1.ID)
		}

		after := changed.After
		if after.DisplayName != newName {
			t.Errorf("DisplayName = %q, want %q", after.DisplayName, newName)
		}
		if after.ExternalID == nil || *after.ExternalID != "ext-2" {
			t.Errorf("ExternalID = %v, want ext-2", after.ExternalID)
		}
		if len(after.Members) != 1 || after.Members[0] != u2.ID {
			t.Errorf("Members = %v, want [%s] — the old member should be gone", after.Members, u2.ID)
		}
		if !after.UpdatedAt.After(created.UpdatedAt) {
			t.Errorf("UpdatedAt not advanced: %v -> %v", created.UpdatedAt, after.UpdatedAt)
		}
	})

	t.Run("omitting members clears them", func(t *testing.T) {
		u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u.ID}})

		changed, err := s.UpdateGroup(ctx, tenantID, created.ID, &Group{DisplayName: created.DisplayName}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("UpdateGroup: %v", err)
		}
		if len(changed.After.Members) != 0 {
			t.Errorf("Members = %v, want empty after a replace with none", changed.After.Members)
		}
	})

	t.Run("unknown id is ErrGroupNotFound", func(t *testing.T) {
		_, err := s.UpdateGroup(ctx, tenantID, nonexistentID, &Group{DisplayName: uniqueGroupName()}, testAudit(tenantID))
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("error = %v, want ErrGroupNotFound", err)
		}
	})

	t.Run("taking another group's name is a conflict", func(t *testing.T) {
		taken := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})
		mover := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})

		_, err := s.UpdateGroup(ctx, tenantID, mover.ID, &Group{DisplayName: taken.DisplayName}, testAudit(tenantID))
		if !errors.Is(err, ErrDuplicateGroupName) {
			t.Fatalf("error = %v, want ErrDuplicateGroupName", err)
		}
	})

	t.Run("an invalid member leaves the group's prior state untouched", func(t *testing.T) {
		u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u.ID}})

		if _, err := s.UpdateGroup(ctx, tenantID, created.ID, &Group{
			DisplayName: created.DisplayName,
			Members:     []string{nonexistentID},
		}, testAudit(tenantID)); !errors.Is(err, ErrInvalidMember) {
			t.Fatalf("error = %v, want ErrInvalidMember", err)
		}

		got, err := s.GetGroup(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		if len(got.Members) != 1 || got.Members[0] != u.ID {
			t.Errorf("Members = %v, want the original [%s] — the failed update should have rolled back", got.Members, u.ID)
		}
	})

	t.Run("another tenant's real id is ErrGroupNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})

		_, err := s.UpdateGroup(ctx, otherTenant, created.ID, &Group{DisplayName: uniqueGroupName()}, testAudit(otherTenant))
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("error = %v, want ErrGroupNotFound", err)
		}
	})
}

func TestDeleteGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenantID := newTestTenant(t, s)

	t.Run("removes the row and cascades membership", func(t *testing.T) {
		u := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{u.ID}})

		before, err := s.DeleteGroup(ctx, tenantID, created.ID, testAudit(tenantID))
		if err != nil {
			t.Fatalf("DeleteGroup: %v", err)
		}
		if len(before.Members) != 1 || before.Members[0] != u.ID {
			t.Errorf("returned before-image Members = %v, want [%s]", before.Members, u.ID)
		}

		if _, err := s.GetGroup(ctx, tenantID, created.ID); !errors.Is(err, ErrGroupNotFound) {
			t.Errorf("GetGroup after delete: error = %v, want ErrGroupNotFound — the row should be gone", err)
		}

		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1`, created.ID).Scan(&n); err != nil {
			t.Fatalf("count group_members: %v", err)
		}
		if n != 0 {
			t.Errorf("group_members still has %d rows for a deleted group, want 0", n)
		}
	})

	// Unlike DeactivateUser, this is not idempotent: there is no `active`
	// flag to leave false a second time — the row is genuinely gone.
	t.Run("is not idempotent", func(t *testing.T) {
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})

		if _, err := s.DeleteGroup(ctx, tenantID, created.ID, testAudit(tenantID)); err != nil {
			t.Fatalf("first DeleteGroup: %v", err)
		}
		if _, err := s.DeleteGroup(ctx, tenantID, created.ID, testAudit(tenantID)); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("second DeleteGroup error = %v, want ErrGroupNotFound", err)
		}
	})

	t.Run("unknown id is ErrGroupNotFound", func(t *testing.T) {
		if _, err := s.DeleteGroup(ctx, tenantID, nonexistentID, testAudit(tenantID)); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("error = %v, want ErrGroupNotFound", err)
		}
	})

	t.Run("another tenant's real id is ErrGroupNotFound", func(t *testing.T) {
		otherTenant := newTestTenant(t, s)
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})

		if _, err := s.DeleteGroup(ctx, otherTenant, created.ID, testAudit(otherTenant)); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("error = %v, want ErrGroupNotFound", err)
		}
	})
}
