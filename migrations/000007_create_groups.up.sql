-- The Group resource (RFC 7643 §4.2). Unlike users, a group has no `active`
-- attribute in the schema, so there is nothing to soft-delete into: deleting
-- a group is a real DELETE, and group_members cascades with it.
CREATE TABLE groups (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    TEXT NOT NULL REFERENCES tenants (id),
    external_id  TEXT,
    display_name TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- displayName uniqueness is per tenant and case-insensitive, the same
-- reconciliation reasoning idx_users_tenant_username already uses: an IdP
-- looks a group up by name before creating one.
CREATE UNIQUE INDEX idx_groups_tenant_displayname ON groups (tenant_id, lower(display_name));

-- Same reasoning as idx_users_tenant_external_id: every tenant has its own
-- identity provider, so two tenants may legitimately reuse an externalId.
CREATE UNIQUE INDEX idx_groups_tenant_external_id ON groups (tenant_id, external_id) WHERE external_id IS NOT NULL;

-- Membership is a join table rather than an array column: it gives an
-- indexed reverse lookup (a user's groups) for free and keeps a member
-- reference a real foreign key, so a deleted user or group can't leave a
-- dangling id behind.
--
-- tenant_id is denormalized onto this table (rather than joined through
-- groups) so every membership query and every test's tenant-scoped cleanup
-- stays a single WHERE tenant_id = $1, matching how audit_log and
-- webhook_deliveries already carry their own tenant_id instead of requiring
-- a join back to users.
CREATE TABLE group_members (
    group_id  UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL REFERENCES tenants (id),
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);

-- "which groups is this user in" is the reverse lookup a deprovisioning flow
-- needs; forward lookup (a group's members) is already covered by the
-- primary key's leading column.
CREATE INDEX idx_group_members_user_id ON group_members (user_id);
