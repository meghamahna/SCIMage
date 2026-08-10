-- SCIM 2.0 core User (RFC 7643 §4.1), reduced to the attributes this server
-- implements. emails is flattened to its primary value; full multi-valued
-- support would be a child table, not a JSON column.

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

-- userName is caseExact=false, so 'bjensen' and 'BJensen' are the same user
-- and a plain TEXT UNIQUE would let both in. This also serves lookups by
-- userName, so no second index is needed.
CREATE UNIQUE INDEX idx_users_user_name_lower ON users (lower(user_name));
