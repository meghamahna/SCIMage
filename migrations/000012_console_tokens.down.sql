-- Restoring NOT NULL would fail if any console.token.* entry (NULL tenant_id)
-- has been written, so drop those system-scope rows first. They only exist
-- because this migration allowed them; reverting the migration reverts them.
DELETE FROM admin_audit_log WHERE tenant_id IS NULL;
ALTER TABLE admin_audit_log ALTER COLUMN tenant_id SET NOT NULL;

DROP TABLE console_tokens;
