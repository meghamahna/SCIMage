---
id: api-scim-routes
kind: api
title: SCIM HTTP routes
summary: Route table for /scim/v2/{tenantID} Users, Groups and discovery, plus unauthenticated health probes and middleware order
tags: [routes, endpoints, api, scim, users, groups, discovery, serviceproviderconfig, healthz, readyz, rest]
links: [implements:module-scim, related:api-scim-errors, related:api-scim-filter, related:api-scim-patch, related:component-scim-auth, related:model-scim-resources]
sources: [internal/scim/handler.go:63-98, internal/scim/discovery.go:105-161, internal/scim/health.go:28-40, cmd/server/main.go:122-126]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Every SCIM path is `/scim/v2/{tenantID}/...`; the tenant segment is the isolation boundary → internal/scim/handler.go:58-62
- Users: POST and GET list on `/Users`; GET, PUT, PATCH, DELETE on `/Users/{id}` → internal/scim/handler.go:65-70
- Groups: same five verbs on `/Groups` and `/Groups/{id}` → internal/scim/handler.go:72-77
- Discovery (GET only): `/ServiceProviderConfig`, `/ResourceTypes`, `/Schemas` → internal/scim/handler.go:79-83
- Wrong verb on a known path → SCIM 405; unknown path → SCIM 404 → internal/scim/handler.go:85-91
- DELETE /Users/{id} is a soft delete (active=false), answers 204 → internal/scim/handler.go:307-321
- DELETE /Groups/{id} answers 204 after store delete → internal/scim/handler.go:453-461
- User list: filter, startIndex (1-based, default 1), count (default store.MaxPageSize, negative becomes 0) → internal/scim/handler.go:144-175, internal/scim/handler.go:668-685
- Create returns 201 with Location header; duplicate userName → 409 → internal/scim/handler.go:101-127
- PUT rejects a body id that differs from the path id → internal/scim/handler.go:177-190
- ServiceProviderConfig advertises PATCH and filter (eq only); bulk, changePassword, sort, etag are false → internal/scim/discovery.go:105-127
- Health: GET /healthz (liveness, no deps) and GET /readyz (DB ping, 2s cap), on the root mux outside /scim/v2 → cmd/server/main.go:124-125, internal/scim/health.go:17-19, internal/scim/health.go:38-40
- Middleware order, outermost first: request log, 10s timeout, token auth, per-caller throttle, mux → internal/scim/handler.go:98, internal/scim/timeout.go:15
- Unauthenticated requests get 401 even for unknown paths, so route existence is not revealed → internal/scim/auth.go:41-44
