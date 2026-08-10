-- The SCIM 2.0 core User resource (RFC 7643 §4.1), reduced to the
-- attributes this server implements. Multi-valued attributes (emails) are
-- flattened to the primary value; if this grows to full multi-valued
-- support it becomes a child table, not a JSON column.
--
-- gen_random_uuid() is built into Postgres 13+, so no pgcrypto extension.

CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_name   TEXT NOT NULL,
    given_name  TEXT,
    family_name TEXT,
    email       TEXT,
    active      BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RFC 7643 §4.1 gives userName caseExact=false, so 'bjensen' and 'BJensen'
-- are the same user. A plain TEXT UNIQUE is case-sensitive and would let
-- both rows exist, so uniqueness is enforced on lower(user_name) instead —
-- that's what makes POST /Users return 409 on a real duplicate.
--
-- This index also serves lookups by userName, so no separate one is needed:
-- a column-level UNIQUE plus an explicit CREATE INDEX would be two btrees on
-- the same column for no benefit.
CREATE UNIQUE INDEX idx_users_user_name_lower ON users (lower(user_name));
