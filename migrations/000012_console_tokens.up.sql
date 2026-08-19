-- The admin console (Phase 14) needs its own credential, separate from the
-- tenant-scoped scim_tokens the SCIM API authenticates with. A console token
-- authenticates the one operator who runs SCIMage, and that operator works
-- across every tenant — the same cross-tenant reach scimage-admin already has
-- from a shell on the database host. So it carries no tenant_id: it is a
-- system-wide credential, not a customer's.
--
-- Otherwise it mirrors scim_tokens exactly: key_id is the non-secret,
-- indexable pointer to the row (the role a username plays); secret_hash is
-- sha256 of the secret half, never the secret itself; revoked_at/expires_at
-- let the operator rotate without downtime.
CREATE TABLE console_tokens (
    key_id       TEXT PRIMARY KEY,
    secret_hash  BYTEA NOT NULL,
    label        TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- Issuing or revoking a console credential is a privileged action, exactly
-- the class admin_audit_log exists to record. But it belongs to no tenant, so
-- tenant_id must be allowed to be NULL for these system-scope entries. Every
-- existing action is tenant-scoped and keeps writing a real id; only the two
-- new console.token.* actions write NULL. Filtering by a specific tenant_id
-- still returns that tenant's history unchanged.
ALTER TABLE admin_audit_log ALTER COLUMN tenant_id DROP NOT NULL;
