---
id: module-scim
kind: module
title: SCIM HTTP module (internal/scim)
summary: SCIM 2.0 HTTP handlers, bearer auth, rate limit, filter/PATCH parsing and SCIM models for /Users and /Groups
tags: [scim, rfc7644, handler, users, groups, http, provisioning, internal/scim]
links: [documented_in:architecture-overview, related:api-scim-routes, related:component-scim-auth, related:component-scim-ratelimit, related:component-scim-request-middleware, related:model-scim-resources, related:api-scim-filter, related:api-scim-patch, related:api-scim-errors, related:component-scim-extended-attributes, decided_by:decision-audit-in-transaction, related:decision-scim-strict-filter, related:decision-scim-uniform-401, related:gotcha-scim-tenant-path-parsing, related:gotcha-scim-idp-quirks, related:gotcha-scim-unauthenticated-not-limited]
sources: [internal/scim/handler.go:1-104, internal/scim/auth.go:1-151, internal/scim/userstore.go:1-47, internal/scim/groupstore.go:1-25]
updated: 2026-11-26
verified_commit: 4185d2b
---
## Facts
- Owns the SCIM HTTP layer: handlers, SCIM JSON models, auth, rate limit, request timeout, request logging → internal/scim/handler.go:105-744
- Persistence is behind UserStore, GroupStore, TokenStore and AttributeStore interfaces → internal/scim/userstore.go:24-47, internal/scim/groupstore.go:13-25, internal/scim/attributestore.go:14-16
- `*store.Store` satisfies each interface (compile-time assertions) → internal/scim/userstore.go:36, internal/scim/userstore.go:47, internal/scim/groupstore.go:25
- Handler struct holds the stores, a per-caller limiter and two env-derived settings → internal/scim/handler.go:31-45
- NewHandler reads SCIM_EXTENDED_ATTRIBUTES and SCIM_BASE_URL; the limiter reads SCIM_RATE_LIMIT/SCIM_RATE_BURST → internal/scim/handler.go:48-64, internal/scim/ratelimit.go:37-46
- Routes() wraps the mux: logRequests → withTimeout → requireToken → limiter.throttle → mux → internal/scim/handler.go:67-102
- Other env var names read here: SCIM_REQUEST_TIMEOUT, SCIM_LOG_REQUESTS → internal/scim/timeout.go:22, internal/scim/requestlog.go:22
- Body cap 1 MiB; attribute cap 256 chars (userName is btree-indexed) → internal/scim/handler.go:22-29
- PATCH and PUT writes are serialized per resource by the store's row lock; the handler only supplies the patch callback → internal/scim/handler.go:301, internal/scim/handler.go:448
- Health probes live in this package, mounted on the root mux outside auth → internal/scim/health.go:28-40, cmd/server/main.go:124-125
## File map
- handler.go: /Users and /Groups CRUD, pagination, base URL, error writers → internal/scim/handler.go:105-744
- auth.go, ratelimit.go, timeout.go, requestlog.go: middleware → internal/scim/auth.go:1-151
- discovery.go: ServiceProviderConfig, ResourceTypes, Schemas → internal/scim/discovery.go:105-161
- filter.go and patch.go: query filter and PATCH parsing → internal/scim/filter.go:19-56, internal/scim/patch.go:41-66
- extended.go: extensible attributes → internal/scim/extended.go:22-46
- mapper.go: store <-> SCIM mapping; models.go: SCIM types → internal/scim/mapper.go:11-28, internal/scim/models.go:50-124
