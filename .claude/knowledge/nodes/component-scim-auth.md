---
id: component-scim-auth
kind: module
title: SCIM bearer token auth
summary: Per-tenant bearer token middleware; constant-time secret compare; uniform 401; every failure logged once with its reason
tags: [auth, bearer, token, middleware, 401, constant time, tenant, key id, scimage, login, logging]
links: [implements:module-scim, related:api-scim-routes, related:decision-scim-uniform-401, related:gotcha-scim-tenant-path-parsing, related:component-scim-ratelimit, related:data-model-tenants-tokens]
sources: [internal/scim/auth.go:20-31, internal/scim/auth.go:45-63, internal/scim/auth.go:98-136, internal/scim/auth.go:150-175, internal/scim/userstore.go:38-41]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Auth is applied inside Routes(), not by the caller, so no route can be mounted without it → internal/scim/handler.go:94-98, internal/scim/auth.go:45-53
- Tokens are issued per tenant and looked up by key id; there is no process-wide secret → internal/scim/auth.go:109
- Bearer scheme is matched case-insensitively; store.ParseToken splits the token into key id and secret → internal/scim/auth.go:104, internal/scim/auth.go:167-175
- Check order: key lookup → not revoked → not expired → SHA-256 of secret compared in constant time to stored hash → tenant in token equals tenant in path → internal/scim/auth.go:109-133
- Secret comparison uses crypto/subtle.ConstantTimeCompare on SHA-256 digests → internal/scim/auth.go:127-128
- Every failure returns the same 401 body and WWW-Authenticate: Bearer → internal/scim/auth.go:61-62
- Every failure logs one "authentication failed" Warn with reason, key_id (only when well-formed), tenant_id (only when IsTenantID) and remote_addr → internal/scim/auth.go:55-60
- On success the key's last-used time is touched; a touch failure is logged, not fatal → internal/scim/auth.go:69-70
- Identity (TenantID, KeyID) goes into request context for throttle and audit → internal/scim/auth.go:73
- Tenant id is read from the raw escaped path, not r.PathValue, because auth runs before the mux → internal/scim/auth.go:99-102, internal/scim/auth.go:150-165
- Token store contract (GetTokenByKeyID returns the hash; TouchToken) is declared here → internal/scim/userstore.go:38-41
