-- One SCIMage deployment now serves many customer organizations. A tenant is
-- the isolation boundary: its own users, its own audit trail, its own issued
-- tokens, all living in the same tables as every other tenant's, scoped by
-- tenant_id rather than by separate infrastructure.
--
-- The new tenant_id columns below are NOT NULL with no default, so this only
-- applies cleanly against empty users/audit_log/webhook_deliveries tables —
-- fine for a fresh database, but existing rows need a real tenant assigned
-- before this migration, not just a default value.
--
-- id is app-supplied (a prefixed opaque string, e.g. tenant_9f2a...), not
-- gen_random_uuid(): it's pasted once into a customer's IdP as part of the
-- SCIM base URL, so it has to be stable and recognisable in logs, the same
-- reasoning behind the scim_tokens key id below.
CREATE TABLE tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- key_id is the indexable half of a token (not secret — the pointer to a
-- row, same role a username plays); secret_hash is sha256 of the secret half,
-- never the secret itself. revoked_at/expires_at let a tenant run several
-- live tokens at once, so rotation doesn't require downtime.
CREATE TABLE scim_tokens (
    key_id       TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants (id),
    secret_hash  BYTEA NOT NULL,
    label        TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- token list is always "for this tenant."
CREATE INDEX idx_scim_tokens_tenant_id ON scim_tokens (tenant_id);

ALTER TABLE users ADD COLUMN tenant_id TEXT NOT NULL REFERENCES tenants (id);

-- userName uniqueness moves from server-wide to per-tenant: two different
-- customers each provisioning a "bjensen" are two different people.
DROP INDEX idx_users_user_name_lower;
CREATE UNIQUE INDEX idx_users_tenant_username ON users (tenant_id, lower(user_name));

-- Likewise externalId is the identity provider's own key, and every tenant
-- has its own identity provider — two tenants' IdPs can legitimately reuse
-- the same externalId for unrelated people.
DROP INDEX idx_users_external_id;
CREATE UNIQUE INDEX idx_users_tenant_external_id ON users (tenant_id, external_id) WHERE external_id IS NOT NULL;

ALTER TABLE audit_log ADD COLUMN tenant_id TEXT NOT NULL REFERENCES tenants (id);
CREATE INDEX idx_audit_log_tenant_id ON audit_log (tenant_id);

ALTER TABLE webhook_deliveries ADD COLUMN tenant_id TEXT NOT NULL REFERENCES tenants (id);
