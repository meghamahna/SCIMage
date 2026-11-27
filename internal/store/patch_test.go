package store

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// waitForLockWaiters returns once n transactions are queued behind the row lock
// on table's row id. A locked row's xmax is the locker's xid: the first waiter
// holds an ungranted lock on that xid and each later one an ungranted lock on
// the row's tuple, so this counts only this row's waiters.
func waitForLockWaiters(t *testing.T, s *Store, table, tenantID, id string, n int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q := `SELECT count(*) FROM ` + table + ` r
	      JOIN pg_locks l ON NOT l.granted AND (
	          (l.locktype = 'transactionid' AND l.transactionid = r.xmax) OR
	          (l.locktype = 'tuple' AND l.relation = '` + table + `'::regclass
	           AND l.page = (r.ctid::text::point)[0]::int AND l.tuple = (r.ctid::text::point)[1]::int))
	      WHERE r.tenant_id = $1 AND r.id = $2`

	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		var waiting int
		if err := s.pool.QueryRow(ctx, q, tenantID, id).Scan(&waiting); err != nil {
			t.Fatalf("count transactions queued behind the %s row lock: %v", table, err)
		}
		if waiting >= n {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%d of %d transactions queued behind the %s row lock: %v", waiting, n, table, ctx.Err())
		case <-poll.C:
		}
	}
}

// whileLocked runs patch with a callback that holds the row until every one of
// writers is queued behind its lock, then applies first's change. It returns
// the writers' errors, in order.
func whileLocked[T any](t *testing.T, s *Store, table, tenantID, id string,
	patch func(func(*T) (*T, error)) error, first func(T) T, writers ...func() error) []error {
	t.Helper()

	locked, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })

	// On an early failure, let every writer finish before the tenant's rows
	// are cleaned up beneath them.
	var running sync.WaitGroup
	t.Cleanup(func() {
		releaseFirst()
		running.Wait()
	})

	firstDone := make(chan error, 1)
	running.Add(1)
	go func() {
		defer running.Done()
		firstDone <- patch(func(cur *T) (*T, error) {
			close(locked)
			<-release
			next := first(*cur)
			return &next, nil
		})
	}()

	select {
	case <-locked:
	case err := <-firstDone:
		t.Fatalf("first patch returned before reaching its callback: %v", err)
	}

	errs := make([]error, len(writers))
	for i, write := range writers {
		running.Add(1)
		go func() {
			defer running.Done()
			errs[i] = write()
		}()
	}

	waitForLockWaiters(t, s, table, tenantID, id, len(writers))
	releaseFirst()

	if err := <-firstDone; err != nil {
		t.Fatalf("first patch: %v", err)
	}
	running.Wait()
	return errs
}

// patchWhileLocked runs two PATCHes of one row, the second queued behind the
// first's lock. It returns the current resource second's callback was handed.
func patchWhileLocked[T any](t *testing.T, s *Store, table, tenantID, id string,
	patch func(func(*T) (*T, error)) error, first, second func(T) T) T {
	t.Helper()

	var seen T
	errs := whileLocked(t, s, table, tenantID, id, patch, first, func() error {
		return patch(func(cur *T) (*T, error) {
			seen = *cur
			next := second(*cur)
			return &next, nil
		})
	})
	if errs[0] != nil {
		t.Fatalf("second patch: %v", errs[0])
	}
	return seen
}

// The offboarding race: a deactivation and a name change for one user arrive
// together. The second must apply on top of the first, never write back the
// active=true it would have read without the lock.
func TestConcurrentPatchUserKeepsTheDeactivation(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	seen := patchWhileLocked(t, s, "users", tenantID, created.ID,
		func(fn func(*User) (*User, error)) error {
			_, err := s.PatchUser(ctx, tenantID, created.ID, fn, testAudit(tenantID))
			return err
		},
		func(u User) User { u.Active = false; return u },
		func(u User) User { u.GivenName = ptr("Barbara"); return u },
	)

	if seen.Active {
		t.Error("second patch was handed the user as active, not the committed deactivation")
	}

	got, err := s.GetUser(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Active || got.GivenName == nil || *got.GivenName != "Barbara" {
		t.Errorf("stored user active=%v givenName=%v, want inactive and Barbara", got.Active, got.GivenName)
	}

	// Newest first: the rename, the deactivation, then the create.
	entries, err := s.ListAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d audit entries, want 3", len(entries))
	}
	deactivateBefore := decodeAuditUser(t, entries[1].Before)
	deactivateAfter := decodeAuditUser(t, entries[1].After)
	renameBefore := decodeAuditUser(t, entries[0].Before)
	renameAfter := decodeAuditUser(t, entries[0].After)

	if !deactivateBefore.Active || deactivateAfter.Active {
		t.Errorf("deactivation audited active %v -> %v, want true -> false", deactivateBefore.Active, deactivateAfter.Active)
	}
	if renameBefore.Active || !renameBefore.UpdatedAt.Equal(deactivateAfter.UpdatedAt) {
		t.Errorf("rename's before-image is not the deactivation's after-image: %+v", renameBefore)
	}
	if renameAfter.Active || renameAfter.GivenName == nil || *renameAfter.GivenName != "Barbara" {
		t.Errorf("rename audited after-image %+v, want inactive and Barbara", renameAfter)
	}
}

// A DELETE that waited behind a PATCH must record the PATCH's result as its
// before-image, not the user as it stood when the DELETE arrived.
func TestDeactivateUserQueuedBehindPatch(t *testing.T) {
	s := newEventStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})

	errs := whileLocked(t, s, "users", tenantID, created.ID,
		func(fn func(*User) (*User, error)) error {
			_, err := s.PatchUser(ctx, tenantID, created.ID, fn, testAudit(tenantID))
			return err
		},
		func(u User) User { u.GivenName = ptr("Barbara"); return u },
		func() error {
			_, err := s.DeactivateUser(ctx, tenantID, created.ID, testAudit(tenantID))
			return err
		},
	)
	if errs[0] != nil {
		t.Fatalf("DeactivateUser: %v", errs[0])
	}

	got, err := s.GetUser(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Active {
		t.Error("user is still active after the DELETE")
	}

	// Newest first: the deactivation, the patch, then the create.
	entries, err := s.ListAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d audit entries, want 3", len(entries))
	}
	if entries[0].Action != ActionDeactivate {
		t.Fatalf("newest audit action = %q, want %q", entries[0].Action, ActionDeactivate)
	}
	patchAfter := decodeAuditUser(t, entries[1].After)
	deactivateBefore := decodeAuditUser(t, entries[0].Before)
	if !deactivateBefore.UpdatedAt.Equal(patchAfter.UpdatedAt) || deactivateBefore.GivenName == nil {
		t.Errorf("deactivation's before-image is not the patch's after-image: %+v", deactivateBefore)
	}

	ds := deliveriesFor(t, s, created.ID)
	if len(ds) != 3 {
		t.Fatalf("got %d change events, want 3", len(ds))
	}
	last := ds[2]
	if last.EventType != EventUserDeactivated {
		t.Errorf("DELETE's event type = %q, want %q", last.EventType, EventUserDeactivated)
	}
	if e := decodeEvent(t, last); e.Before == nil || !e.Before.UpdatedAt.Equal(patchAfter.UpdatedAt) {
		t.Errorf("DELETE's event before-image is not the patch's after-image: %+v", e.Before)
	}
}

// Parallel membership pushes: removing X and adding Y race on one group. The
// add must see X already gone, so the removal sticks.
func TestConcurrentPatchGroupKeepsTheRemoval(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	x := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	y := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
	g := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), Members: []string{x.ID}})

	seen := patchWhileLocked(t, s, "groups", tenantID, g.ID,
		func(fn func(*Group) (*Group, error)) error {
			_, err := s.PatchGroup(ctx, tenantID, g.ID, fn, testAudit(tenantID))
			return err
		},
		func(g Group) Group {
			g.Members = slices.DeleteFunc(slices.Clone(g.Members), func(m string) bool { return m == x.ID })
			return g
		},
		func(g Group) Group { g.Members = append(slices.Clone(g.Members), y.ID); return g },
	)

	if len(seen.Members) != 0 {
		t.Errorf("second patch was handed members %v, want the committed removal (none)", seen.Members)
	}

	got, err := s.GetGroup(ctx, tenantID, g.ID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if !slices.Equal(got.Members, []string{y.ID}) {
		t.Errorf("stored members = %v, want only %s", got.Members, y.ID)
	}

	// Newest first: the add, the removal, then three creates.
	entries, err := s.ListAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("got %d audit entries, want 5", len(entries))
	}
	removeBefore := decodeAuditGroup(t, entries[1].Before)
	removeAfter := decodeAuditGroup(t, entries[1].After)
	addBefore := decodeAuditGroup(t, entries[0].Before)
	addAfter := decodeAuditGroup(t, entries[0].After)

	if !slices.Equal(removeBefore.Members, []string{x.ID}) || len(removeAfter.Members) != 0 {
		t.Errorf("removal audited members %v -> %v, want [%s] -> none", removeBefore.Members, removeAfter.Members, x.ID)
	}
	if len(addBefore.Members) != 0 || !addBefore.UpdatedAt.Equal(removeAfter.UpdatedAt) {
		t.Errorf("add's before-image is not the removal's after-image: %+v", addBefore)
	}
	if !slices.Equal(addAfter.Members, []string{y.ID}) {
		t.Errorf("add audited members after = %v, want [%s]", addAfter.Members, y.ID)
	}
}

// Two DELETEs of one group queue behind a PATCH. Whichever runs second must
// find the group gone rather than delete it again and audit a second success.
func TestConcurrentDeleteGroupHasOneWinner(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	g := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})
	deleteGroup := func() error {
		_, err := s.DeleteGroup(ctx, tenantID, g.ID, testAudit(tenantID))
		return err
	}

	errs := whileLocked(t, s, "groups", tenantID, g.ID,
		func(fn func(*Group) (*Group, error)) error {
			_, err := s.PatchGroup(ctx, tenantID, g.ID, fn, testAudit(tenantID))
			return err
		},
		func(g Group) Group { return g },
		deleteGroup, deleteGroup,
	)

	var won, lost int
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrGroupNotFound):
			lost++
		default:
			t.Errorf("DeleteGroup: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Errorf("%d DELETEs succeeded and %d found no group, want one of each", won, lost)
	}

	entries, err := s.ListAuditEntries(ctx, tenantID, 0)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	var deletes int
	for _, e := range entries {
		if e.Action == ActionDelete && e.Result == ResultSuccess {
			deletes++
		}
	}
	if deletes != 1 {
		t.Errorf("got %d successful delete audit entries, want 1", deletes)
	}
}

func TestPatchUserRefusal(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	t.Run("a refused patch writes nothing", func(t *testing.T) {
		created := createUser(t, s, tenantID, &User{UserName: uniqueUserName(), Active: true})
		entriesBefore := countAudit(t, s, tenantID)
		refused := errors.New("invalid operation")

		_, err := s.PatchUser(ctx, tenantID, created.ID, func(u *User) (*User, error) {
			return nil, refused
		}, testAudit(tenantID))
		if !errors.Is(err, refused) {
			t.Fatalf("PatchUser = %v, want the callback's error", err)
		}

		got, err := s.GetUser(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}
		if !got.UpdatedAt.Equal(created.UpdatedAt) {
			t.Errorf("updated_at moved from %v to %v on a refused patch", created.UpdatedAt, got.UpdatedAt)
		}
		if n := countAudit(t, s, tenantID); n != entriesBefore {
			t.Errorf("audit entries = %d, want %d", n, entriesBefore)
		}
	})

	t.Run("unknown user is not found without calling patch", func(t *testing.T) {
		_, err := s.PatchUser(ctx, tenantID, nonexistentID, func(u *User) (*User, error) {
			t.Error("patch called for a user that does not exist")
			return u, nil
		}, testAudit(tenantID))
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("PatchUser = %v, want ErrNotFound", err)
		}
	})
}

func TestPatchGroupRefusal(t *testing.T) {
	s := newTestStore(t)
	tenantID := newTestTenant(t, s)
	ctx := context.Background()

	t.Run("a refused patch writes nothing", func(t *testing.T) {
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName()})
		entriesBefore := countAudit(t, s, tenantID)
		refused := errors.New("invalid operation")

		_, err := s.PatchGroup(ctx, tenantID, created.ID, func(g *Group) (*Group, error) {
			return nil, refused
		}, testAudit(tenantID))
		if !errors.Is(err, refused) {
			t.Fatalf("PatchGroup = %v, want the callback's error", err)
		}

		got, err := s.GetGroup(ctx, tenantID, created.ID)
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		if !got.UpdatedAt.Equal(created.UpdatedAt) {
			t.Errorf("updated_at moved from %v to %v on a refused patch", created.UpdatedAt, got.UpdatedAt)
		}
		if n := countAudit(t, s, tenantID); n != entriesBefore {
			t.Errorf("audit entries = %d, want %d", n, entriesBefore)
		}
	})

	t.Run("patch mutating its copy in place leaves the before-image intact", func(t *testing.T) {
		created := createGroup(t, s, tenantID, &Group{DisplayName: uniqueGroupName(), ExternalID: ptr("ext-before")})

		change, err := s.PatchGroup(ctx, tenantID, created.ID, func(g *Group) (*Group, error) {
			*g.ExternalID = "ext-after"
			return g, nil
		}, testAudit(tenantID))
		if err != nil {
			t.Fatalf("PatchGroup: %v", err)
		}
		if got := *change.Before.ExternalID; got != "ext-before" {
			t.Errorf("before-image externalId = %q, want ext-before", got)
		}
		if got := *change.After.ExternalID; got != "ext-after" {
			t.Errorf("after-image externalId = %q, want ext-after", got)
		}
	})

	t.Run("unknown group is not found without calling patch or auditing", func(t *testing.T) {
		entriesBefore := countAudit(t, s, tenantID)

		_, err := s.PatchGroup(ctx, tenantID, nonexistentID, func(g *Group) (*Group, error) {
			t.Error("patch called for a group that does not exist")
			return g, nil
		}, testAudit(tenantID))
		if !errors.Is(err, ErrGroupNotFound) {
			t.Errorf("PatchGroup = %v, want ErrGroupNotFound", err)
		}
		if n := countAudit(t, s, tenantID); n != entriesBefore {
			t.Errorf("audit entries = %d, want %d", n, entriesBefore)
		}
	})
}
