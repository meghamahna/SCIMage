-- Enterprise-facing gaps found after Phase 10 shipped: two customers could
-- silently share a display name, issued tokens carried no real attribution,
-- and none of the privileged CLI actions (create a tenant, issue or revoke a
-- token) were themselves audited, even though that is exactly the class of
-- action a governance-minded operator most wants a record of.

-- Tenant names are how a human operator recognises a customer; a duplicate,
-- even one that only differs by case, is a support incident waiting to
-- happen. Same reasoning as lower(user_name) on users.
CREATE UNIQUE INDEX idx_tenants_name_lower ON tenants (lower(name));

-- Nullable: existing rows predate this column, and it is provenance, not a
-- security control, so a missing value degrades to "unknown" rather than
-- blocking anything.
ALTER TABLE tenants ADD COLUMN created_by TEXT;

-- One row per privileged administrative action, mirroring audit_log's own
-- discipline: written in the same transaction as the change it describes, so
-- a tenant or token cannot be created, issued or revoked without a record of
-- who did it and when. tenant_id is the tenant the action is about — for
-- tenant.create that is the row just created, so filtering by tenant_id
-- always answers "everything administrative that has happened to this
-- tenant," creation included.
CREATE TABLE admin_audit_log (
    id        BIGSERIAL PRIMARY KEY,
    at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id TEXT NOT NULL REFERENCES tenants (id),
    actor     TEXT NOT NULL,
    action    TEXT NOT NULL,
    target_id TEXT NOT NULL,
    detail    TEXT
);

CREATE INDEX idx_admin_audit_log_tenant_id ON admin_audit_log (tenant_id, at DESC);
