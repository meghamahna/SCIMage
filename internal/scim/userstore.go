package scim

import (
	"context"

	"github.com/meghamahna/SCIMage/internal/store"
)

// UserStore is the persistence the handler needs. *store.Store is the default
// implementation; an application with its own user table can supply another.
// Every method is scoped to one tenant, so a conforming implementation cannot
// accidentally serve one tenant's request out of another's data even if a
// caller passed the wrong id.
//
// Two obligations the compiler can't enforce. An implementation must write the
// audit entry in the same transaction as the change — which is why AuditRecord
// is a parameter here rather than something the handler records afterwards.
// And a nil error must come with a non-nil result: the handler dereferences
// what it gets back without checking, which is free against a concrete store
// and a panic against a conforming implementation that returns (nil, nil).
//
// Declared with the consumer, per Go convention. It sits under internal/, so
// supplying an implementation today means forking rather than importing.
type UserStore interface {
	CreateUser(ctx context.Context, tenantID string, u *store.User, rec store.AuditRecord) (*store.User, error)
	GetUser(ctx context.Context, tenantID, id string) (*store.User, error)
	ListUsers(ctx context.Context, tenantID string, limit, offset int, f store.UserFilter) ([]store.User, int, error)
	UpdateUser(ctx context.Context, tenantID, id string, u *store.User, rec store.AuditRecord) (*store.Change, error)
	DeactivateUser(ctx context.Context, tenantID, id string, rec store.AuditRecord) (*store.Change, error)
}

var _ UserStore = (*store.Store)(nil)

// TokenStore is the lookup the auth middleware needs. It hands back data —
// including the stored hash — rather than a verdict: the constant-time
// compare and the tenant check stay in auth.go, the same place that logic
// already lived for the single-token model this replaces.
type TokenStore interface {
	GetTokenByKeyID(ctx context.Context, keyID string) (*store.Token, error)
	TouchToken(ctx context.Context, keyID string) error
}

var _ TokenStore = (*store.Store)(nil)
