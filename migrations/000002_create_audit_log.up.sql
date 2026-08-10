-- One row per mutating call, written in the same transaction as the change it
-- describes. A user cannot be created, replaced or deactivated without its
-- audit entry: if the entry fails to insert, the mutation rolls back with it.
-- A file could never give that guarantee — Postgres and a filesystem have no
-- shared commit.

CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_token TEXT NOT NULL,
    actor_ip    TEXT,
    action      TEXT NOT NULL,
    result      TEXT NOT NULL,
    detail      TEXT,

    -- No foreign key to users on purpose: a refused create has no target, and
    -- history has to outlive a row that gets hard-deleted.
    target_id   UUID,

    before      JSONB,
    after       JSONB
);

-- Reviewers and SAGE read recent activity first.
CREATE INDEX idx_audit_log_at ON audit_log (at DESC);

-- "What happened to this user" is the other question worth answering fast.
CREATE INDEX idx_audit_log_target ON audit_log (target_id) WHERE target_id IS NOT NULL;
