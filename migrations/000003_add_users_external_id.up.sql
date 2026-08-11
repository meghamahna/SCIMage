-- externalId is the identity provider's own key for a user (RFC 7643 §3.1).
-- Clients send it on create and then reconcile by it, so storing and returning
-- it is what lets an IdP recognise a user it has already provisioned.

ALTER TABLE users ADD COLUMN external_id TEXT;

-- Unique where present: two users cannot share one provider-side identity.
-- A partial index leaves rows without an externalId unconstrained, since NULLs
-- would otherwise all collide under a plain UNIQUE in some engines and, here,
-- would simply be unenforced.
CREATE UNIQUE INDEX idx_users_external_id ON users (external_id) WHERE external_id IS NOT NULL;
