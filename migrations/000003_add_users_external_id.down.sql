-- Dropping the column drops idx_users_external_id with it.
ALTER TABLE users DROP COLUMN IF EXISTS external_id;
