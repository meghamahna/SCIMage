package scim

import (
	"context"

	"github.com/meghamahna/SCIMage/internal/store"
)

// AttributeStore is the registry lookup the handler needs to know which extra
// attribute names a tenant has opted into capturing (Phase 14). Only the write,
// PATCH and discovery paths consult it; plain reads merge whatever is already
// stored, so a GET never depends on the registry. It may be nil when the
// feature is disabled — nothing calls it in that case.
type AttributeStore interface {
	ListAttributes(ctx context.Context, tenantID string) ([]store.TenantAttribute, error)
}

var _ AttributeStore = (*store.Store)(nil)
