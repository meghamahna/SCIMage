ALTER TABLE webhook_deliveries DROP COLUMN tenant_id;

ALTER TABLE audit_log DROP COLUMN tenant_id;

DROP INDEX idx_users_tenant_external_id;
CREATE UNIQUE INDEX idx_users_external_id ON users (external_id) WHERE external_id IS NOT NULL;

DROP INDEX idx_users_tenant_username;
CREATE UNIQUE INDEX idx_users_user_name_lower ON users (lower(user_name));
ALTER TABLE users DROP COLUMN tenant_id;

DROP TABLE scim_tokens;
DROP TABLE tenants;
