package scim

import (
	"context"

	"github.com/meghamahna/SCIMage/internal/store"
)

// UserStore is the persistence the handler needs. *store.Store is the default
// implementation; an application with its own user table can supply another.
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
	CreateUser(ctx context.Context, u *store.User, rec store.AuditRecord) (*store.User, error)
	GetUser(ctx context.Context, id string) (*store.User, error)
	ListUsers(ctx context.Context, limit, offset int, f store.UserFilter) ([]store.User, int, error)
	UpdateUser(ctx context.Context, id string, u *store.User, rec store.AuditRecord) (*store.Change, error)
	DeactivateUser(ctx context.Context, id string, rec store.AuditRecord) (*store.Change, error)
}

var _ UserStore = (*store.Store)(nil)
