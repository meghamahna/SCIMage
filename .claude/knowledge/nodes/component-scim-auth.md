---
id: component-scim-auth
kind: module
title: SCIM bearer token auth
summary: Per-tenant bearer token middleware; constant-time secret compare; uniform 401; token lookup via TokenStore
tags: [auth, authentication, bearer, token, constant-time, subtle, tenant, 401, middleware, api key]
links: [implements:module-scim, related:api-scim-routes, related:decision-scim-uniform-401, related:gotcha-scim-tenant-path-parsing, related:component-scim-ratelimit]
sources: [internal/scim/auth.go:1-151, internal/scim/userstore.go:38-43]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Auth is applied inside Routes(), not by the caller, so no route can be mounted without it → internal/scim/handler.go:94-98, internal/scim/auth.go:45-53
- Tokens are issued per tenant and looked up by key id; there is no process-wide secret → internal/scim/auth.go:20-23, internal/scim/auth.go:84
- Bearer scheme is matched case-insensitively; store.ParseToken splits the token into key id and secret → internal/scim/auth.go:143-151, internal/scim/auth.go:79
- Check order: key lookup → not revoked → not expired → SHA-256 of secret compared in constant time to stored hash → tenant in token equals tenant in path → internal/scim/auth.go:84-112
- Secret comparison uses crypto/subtle.ConstantTimeCompare on SHA-256 digests → internal/scim/auth.go:103-104
- Every failure returns the same 401 body and WWW-Authenticate: Bearer → internal/scim/auth.go:49-52
- Missing key is not logged (attacker-triggerable); other lookup errors are logged with key id → internal/scim/auth.go:85-93
- On success the key's last-used time is touched; a touch failure is logged, not fatal → internal/scim/auth.go:55-60
- Identity (TenantID, KeyID) goes into request context for throttle and audit → internal/scim/auth.go:25-31, internal/scim/auth.go:62-63
- Tenant id is read from the raw escaped path, not r.PathValue, because auth runs before the mux → internal/scim/auth.go:73-78, internal/scim/auth.go:124-139
- Token store contract (GetTokenByKeyID returns the hash; TouchToken) is declared here → internal/scim/userstore.go:38-41
