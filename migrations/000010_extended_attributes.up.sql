-- Extensible attributes: an operator can register extra attribute names a
-- tenant's identity provider sends — known SCIM attributes this server
-- doesn't model as typed columns (displayName, title, phoneNumbers, the
-- enterprise extension, …) or fully custom fields — and have them captured
-- and returned rather than dropped. The captured values live in one JSONB
-- column, so adding an attribute is config, not a schema change.

-- Additive and nullable: existing rows get NULL, and nothing outside this
-- column is touched. A user with no extended attributes serialises exactly
-- as before.
ALTER TABLE users ADD COLUMN extended_attributes JSONB;

-- The per-tenant registry: which extra attribute names to capture, and the
-- type to declare for each in the /Schemas document. Scoped by tenant_id so
-- one customer's custom fields never show up in another's schema. `name` is
-- the top-level JSON key of the SCIM resource (e.g. "displayName" or the
-- whole "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User" object).
CREATE TABLE tenant_attributes (
    tenant_id  TEXT NOT NULL REFERENCES tenants (id),
    name       TEXT NOT NULL,
    type       TEXT NOT NULL DEFAULT 'string',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT,
    PRIMARY KEY (tenant_id, name)
);
