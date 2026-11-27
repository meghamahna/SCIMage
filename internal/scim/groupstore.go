package scim

import (
	"context"

	"github.com/meghamahna/SCIMage/internal/store"
)

// GroupStore is the persistence the handler needs for /Groups, mirroring
// UserStore's shape and its two obligations: the audit entry is written in
// the same transaction as the change, and a nil error must come with a
// non-nil result.
type GroupStore interface {
	CreateGroup(ctx context.Context, tenantID string, g *store.Group, rec store.AuditRecord) (*store.Group, error)
	GetGroup(ctx context.Context, tenantID, id string) (*store.Group, error)
	ListGroups(ctx context.Context, tenantID string, limit, offset int, f store.GroupFilter) ([]store.Group, int, error)
	UpdateGroup(ctx context.Context, tenantID, id string, g *store.Group, rec store.AuditRecord) (*store.GroupChange, error)
	// PatchGroup has PatchUser's obligation: patch runs with the group,
	// members included, locked against concurrent writers. An error from
	// patch must come back wrapped, so errors.As still finds it.
	PatchGroup(ctx context.Context, tenantID, id string, patch func(*store.Group) (*store.Group, error), rec store.AuditRecord) (*store.GroupChange, error)
	DeleteGroup(ctx context.Context, tenantID, id string, rec store.AuditRecord) (*store.Group, error)
}

var _ GroupStore = (*store.Store)(nil)
