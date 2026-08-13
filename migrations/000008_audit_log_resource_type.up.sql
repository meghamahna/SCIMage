-- Groups are audited through the same audit_log table as users (one trail
-- for ARIA to read, rather than two that can drift apart), so a row
-- now has to say which kind of resource its before/after images describe.
-- Every existing row is a user; new resource kinds are Go-side additions,
-- not schema ones, so the default stays rather than being dropped.
ALTER TABLE audit_log ADD COLUMN resource_type TEXT NOT NULL DEFAULT 'user';
