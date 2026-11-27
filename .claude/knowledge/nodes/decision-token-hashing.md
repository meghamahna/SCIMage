---
id: decision-token-hashing
kind: decision
title: Bearer tokens stored as sha256 of a random secret, looked up by key id
summary: The database holds a non-secret key id and sha256 of a 32-byte random secret; no KDF, because the secret is already high entropy
tags: [decision, token, hashing, sha256, bearer, secret, key_id, credential storage, kdf, bcrypt]
links: [related:data-model-tenants-tokens, related:module-store, related:decision-credentials-issued-by-cli]
sources: [internal/store/token.go:17-30, internal/store/token.go:62-76, internal/store/token.go:28-31, internal/scim/auth.go:128, migrations/000005_multi_tenancy.up.sql:20-24, migrations/000012_console_tokens.up.sql:8-11, internal/store/consoletoken.go:50-63]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Decision
- A token is `scimage_<keyID>_<secret>`; keyID is the indexable pointer to the row, the secret is never stored → internal/store/token.go:17, internal/store/token.go:44-50
- Only sha256(secret) is written to secret_hash BYTEA; the plaintext is returned once at issue → internal/store/token.go:62-76, migrations/000005_multi_tenancy.up.sql:21-23
- Sizes: 8 bytes key id, 32 bytes secret, hex-encoded → internal/store/token.go:26-30
- No bcrypt/scrypt: secrets are machine-generated and high entropy, so a fast hash is enough; a KDF targets low-entropy human passwords → internal/store/token.go:19-25
- Constant-time comparison happens in the auth middleware, not the store, which only returns the hash → internal/store/token.go:28-31, internal/scim/auth.go:128
- Console credentials use the same scheme → internal/store/consoletoken.go:50-63, migrations/000012_console_tokens.up.sql:8-11
- Rotation: several live tokens per tenant, with revoked_at and expires_at → migrations/000005_multi_tenancy.up.sql:20-24
