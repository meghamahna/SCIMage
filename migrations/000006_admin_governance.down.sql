DROP TABLE admin_audit_log;
ALTER TABLE tenants DROP COLUMN created_by;
DROP INDEX idx_tenants_name_lower;
