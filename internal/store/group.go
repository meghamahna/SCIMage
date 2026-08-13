package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Members is the group's own member ids, in the order they were added —
// there is no attribute on User to soft-delete, so unlike users a group has
// no analogous nullable-optional shape here beyond ExternalID.
type Group struct {
	ID          string    `json:"id"`
	ExternalID  *string   `json:"externalId,omitempty"`
	DisplayName string    `json:"displayName"`
	Members     []string  `json:"members,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Order must match scanGroup. Membership lives in group_members and is
// fetched separately — see fetchMembers and attachMembers — since it can't
// be expressed as a fixed column list.
const groupColumns = `id, external_id, display_name, created_at, updated_at`

// Named in migrations/000007_create_groups.up.sql.
const uniqueGroupNameIndex = "idx_groups_tenant_displayname"

// GroupFilter narrows a listing the same way UserFilter does: the zero value
// (besides tenantID) lists everything for that tenant.
type GroupFilter struct {
	DisplayName string
	ExternalID  string
}

func (f GroupFilter) clause(tenantID string) (string, []any) {
	args := []any{tenantID}
	conds := []string{"tenant_id = $1"}

	if f.DisplayName != "" {
		args = append(args, f.DisplayName)
		conds = append(conds, "lower(display_name) = lower($"+strconv.Itoa(len(args))+")")
	}
	if f.ExternalID != "" {
		args = append(args, f.ExternalID)
		conds = append(conds, "external_id = $"+strconv.Itoa(len(args)))
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

// GroupChange is the before/after pair UpdateGroup returns, the same shape
// Change is for Users. DeleteGroup returns a bare *Group instead: a delete
// has no after-image, so there is no pair to carry.
type GroupChange struct {
	Before *Group
	After  *Group
}

func scanGroup(row pgx.Row) (*Group, error) {
	var g Group
	if err := row.Scan(&g.ID, &g.ExternalID, &g.DisplayName, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// fetchMembers returns one group's member ids in the deterministic order
// they were added, so repeated reads and paging agree.
func fetchMembers(ctx context.Context, q querier, groupID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT user_id FROM group_members WHERE group_id = $1 ORDER BY added_at, user_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("fetch members: %w", err)
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("fetch members: scan row: %w", err)
		}
		members = append(members, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fetch members: read rows: %w", err)
	}
	return members, nil
}

// attachMembers batch-fetches membership for a whole page of groups in one
// query, the same reasoning ListUsers avoids N+1 by not paging one row at a
// time: group_id = ANY(...) rather than one query per group.
func attachMembers(ctx context.Context, q querier, groups []Group) error {
	if len(groups) == 0 {
		return nil
	}

	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}

	rows, err := q.Query(ctx,
		`SELECT group_id, user_id FROM group_members WHERE group_id = ANY($1::uuid[]) ORDER BY group_id, added_at, user_id`, ids)
	if err != nil {
		return fmt.Errorf("fetch members: %w", err)
	}
	defer rows.Close()

	byGroup := make(map[string][]string, len(groups))
	for rows.Next() {
		var groupID, userID string
		if err := rows.Scan(&groupID, &userID); err != nil {
			return fmt.Errorf("fetch members: scan row: %w", err)
		}
		byGroup[groupID] = append(byGroup[groupID], userID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("fetch members: read rows: %w", err)
	}

	for i := range groups {
		groups[i].Members = byGroup[groups[i].ID]
	}
	return nil
}

// replaceMembers overwrites a group's membership wholesale: delete every
// existing row, then insert the new set, validating every id against this
// tenant's users in the same statement that inserts them. A row-count
// mismatch against the deduplicated input means at least one id doesn't
// exist or belongs to another tenant; the caller's transaction rolls the
// whole mutation back rather than applying a partial membership set.
func replaceMembers(ctx context.Context, tx pgx.Tx, tenantID, groupID string, memberIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, groupID); err != nil {
		return fmt.Errorf("clear members: %w", err)
	}

	deduped := dedupeNonEmpty(memberIDs)
	if len(deduped) == 0 {
		return nil
	}

	const q = `INSERT INTO group_members (group_id, user_id, tenant_id)
	           SELECT $1, u.id, $2 FROM users u WHERE u.tenant_id = $2 AND u.id = ANY($3::uuid[])`

	tag, err := tx.Exec(ctx, q, groupID, tenantID, deduped)
	if err != nil {
		if isInvalidArrayElement(err) {
			return ErrInvalidMember
		}
		return fmt.Errorf("add members: %w", err)
	}
	if int(tag.RowsAffected()) != len(deduped) {
		return ErrInvalidMember
	}
	return nil
}

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// getGroupTx reads a group and its membership inside an already-open
// transaction, for the before-image UpdateGroup and DeleteGroup need.
func getGroupTx(ctx context.Context, tx pgx.Tx, tenantID, id string) (*Group, error) {
	const q = `SELECT ` + groupColumns + ` FROM groups WHERE tenant_id = $1 AND id = $2`

	g, err := scanGroup(tx.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		if isMissingRow(err) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}

	members, err := fetchMembers(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	g.Members = members
	return g, nil
}

// CreateGroup returns the stored row with its (possibly empty) membership.
// displayName uniqueness is case-insensitive per tenant, the same
// reconciliation reasoning userName already uses.
func (s *Store) CreateGroup(ctx context.Context, tenantID string, g *Group, rec AuditRecord) (*Group, error) {
	const q = `INSERT INTO groups (tenant_id, external_id, display_name)
	           VALUES ($1, $2, $3)
	           RETURNING ` + groupColumns

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create group %q: begin: %w", g.DisplayName, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	created, err := scanGroup(tx.QueryRow(ctx, q, tenantID, g.ExternalID, g.DisplayName))
	if err != nil {
		if isUniqueViolationOn(err, uniqueGroupNameIndex) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionCreate, "", "duplicate displayName")
			return nil, fmt.Errorf("create group %q: %w", g.DisplayName, ErrDuplicateGroupName)
		}
		return nil, fmt.Errorf("create group %q: %w", g.DisplayName, err)
	}

	if err := replaceMembers(ctx, tx, tenantID, created.ID, g.Members); err != nil {
		if errors.Is(err, ErrInvalidMember) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionCreate, "", "invalid member reference")
		}
		return nil, fmt.Errorf("create group %q: %w", g.DisplayName, err)
	}

	members, err := fetchMembers(ctx, tx, created.ID)
	if err != nil {
		return nil, fmt.Errorf("create group %q: %w", g.DisplayName, err)
	}
	created.Members = members

	if err := insertAudit(ctx, tx, rec, ResourceGroup, ActionCreate, created.ID, ResultSuccess, "", nil, created); err != nil {
		return nil, fmt.Errorf("create group %q: %w", g.DisplayName, err)
	}

	if err := s.enqueueGroupChange(ctx, tx, tenantID, EventGroupCreated, created.ID, nil, created); err != nil {
		return nil, fmt.Errorf("create group %q: %w", g.DisplayName, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create group %q: commit: %w", g.DisplayName, err)
	}
	return created, nil
}

// GetGroup is scoped by tenant as well as id, the same isolation GetUser
// holds: another tenant's real group id gets the same 404 as a made-up one.
func (s *Store) GetGroup(ctx context.Context, tenantID, id string) (*Group, error) {
	const q = `SELECT ` + groupColumns + ` FROM groups WHERE tenant_id = $1 AND id = $2`

	g, err := scanGroup(s.pool.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		if isMissingRow(err) {
			return nil, fmt.Errorf("get group %q: %w", id, ErrGroupNotFound)
		}
		return nil, fmt.Errorf("get group %q: %w", id, err)
	}

	members, err := fetchMembers(ctx, s.pool, id)
	if err != nil {
		return nil, fmt.Errorf("get group %q: %w", id, err)
	}
	g.Members = members
	return g, nil
}

// ListGroups returns a page plus the total row count, the same shape
// ListUsers does.
func (s *Store) ListGroups(ctx context.Context, tenantID string, limit, offset int, f GroupFilter) ([]Group, int, error) {
	if limit < 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list groups: limit and offset must not be negative (got %d, %d)", limit, offset)
	}
	limit = min(limit, MaxPageSize)

	where, args := f.clause(tenantID)

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM groups`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count groups: %w", err)
	}

	q := `SELECT ` + groupColumns + `
	      FROM groups` + where + `
	      ORDER BY created_at, id
	      LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)

	rows, err := s.pool.Query(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	groups := make([]Group, 0, limit)
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("list groups: scan row: %w", err)
		}
		groups = append(groups, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list groups: read rows: %w", err)
	}

	if err := attachMembers(ctx, s.pool, groups); err != nil {
		return nil, 0, fmt.Errorf("list groups: %w", err)
	}

	return groups, total, nil
}

// UpdateGroup is the full replace behind PUT /Groups/{id}: it replaces
// displayName/externalId and the entire membership set in one transaction,
// so a PATCH funnelled through here (the same pattern PATCH /Users/{id}
// uses) gets one audit entry covering both.
func (s *Store) UpdateGroup(ctx context.Context, tenantID, id string, g *Group, rec AuditRecord) (*GroupChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("update group %q: begin: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	before, err := getGroupTx(ctx, tx, tenantID, id)
	if err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionReplace, id, "no such group")
		}
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}

	const q = `UPDATE groups SET display_name = $3, external_id = $4, updated_at = now()
	           WHERE tenant_id = $1 AND id = $2
	           RETURNING ` + groupColumns

	after, err := scanGroup(tx.QueryRow(ctx, q, tenantID, id, g.DisplayName, g.ExternalID))
	if err != nil {
		if isUniqueViolationOn(err, uniqueGroupNameIndex) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionReplace, id, "duplicate displayName")
			return nil, fmt.Errorf("update group %q: %w", id, ErrDuplicateGroupName)
		}
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}

	if err := replaceMembers(ctx, tx, tenantID, id, g.Members); err != nil {
		if errors.Is(err, ErrInvalidMember) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionReplace, id, "invalid member reference")
		}
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}

	members, err := fetchMembers(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}
	after.Members = members

	if err := insertAudit(ctx, tx, rec, ResourceGroup, ActionReplace, id, ResultSuccess, "", before, after); err != nil {
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}

	if err := s.enqueueGroupChange(ctx, tx, tenantID, EventGroupReplaced, id, before, after); err != nil {
		return nil, fmt.Errorf("update group %q: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("update group %q: commit: %w", id, err)
	}
	return &GroupChange{Before: before, After: after}, nil
}

// DeleteGroup is a hard delete, unlike DeactivateUser: the Group schema has
// no active attribute to soft-delete into, so removing one really removes
// the row (group_members cascades). The before-image is still captured
// inside the transaction, so the audit entry keeps a full record of what
// existed. Unlike DeactivateUser, this is not idempotent — a second delete
// of the same id is ErrGroupNotFound, because there is no longer a row to
// re-delete.
func (s *Store) DeleteGroup(ctx context.Context, tenantID, id string, rec AuditRecord) (*Group, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("delete group %q: begin: %w", id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	before, err := getGroupTx(ctx, tx, tenantID, id)
	if err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			s.auditRefusal(ctx, rec, ResourceGroup, ActionDelete, id, "no such group")
		}
		return nil, fmt.Errorf("delete group %q: %w", id, err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM groups WHERE tenant_id = $1 AND id = $2`, tenantID, id); err != nil {
		return nil, fmt.Errorf("delete group %q: %w", id, err)
	}

	if err := insertAudit(ctx, tx, rec, ResourceGroup, ActionDelete, id, ResultSuccess, "", before, nil); err != nil {
		return nil, fmt.Errorf("delete group %q: %w", id, err)
	}

	if err := s.enqueueGroupChange(ctx, tx, tenantID, EventGroupDeleted, id, before, nil); err != nil {
		return nil, fmt.Errorf("delete group %q: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("delete group %q: commit: %w", id, err)
	}
	return before, nil
}

// isInvalidArrayElement reports a 22P02 raised while binding a uuid[]
// parameter — a member id that isn't a well-formed UUID, the array
// equivalent of isMissingRow's single-id case.
func isInvalidArrayElement(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
