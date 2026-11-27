---
id: module-scim
kind: module
title: SCIM HTTP module (internal/scim)
summary: SCIM 2.0 HTTP handlers, bearer auth, rate limit, filter/PATCH parsing and SCIM models for /Users and /Groups
tags: [scim, rfc7644, handler, users, groups, http, provisioning, internal/scim]
links: [documented_in:architecture-overview, related:api-scim-routes, related:component-scim-auth, related:component-scim-ratelimit, related:component-scim-request-middleware, related:model-scim-resources, related:api-scim-filter, related:api-scim-patch, related:api-scim-errors, related:component-scim-extended-attributes, decided_by:decision-audit-in-transaction, related:decision-scim-strict-filter, related:decision-scim-uniform-401, related:gotcha-scim-tenant-path-parsing, related:gotcha-scim-idp-quirks, related:gotcha-scim-unauthenticated-not-limited]
sources: [internal/scim/handler.go:20-98, internal/scim/auth.go:45-117, internal/scim/userstore.go:24-51, internal/scim/groupstore.go:13-26, internal/scim/attributestore.go:14-18]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Owns the SCIM HTTP layer: handlers, SCIM JSON models, auth, rate limit, request timeout, request logging → internal/scim/handler.go:101-790
- Persistence is behind UserStore, GroupStore, TokenStore and AttributeStore interfaces → internal/scim/userstore.go:24-38, internal/scim/userstore.go:46-49, internal/scim/groupstore.go:13-24, internal/scim/attributestore.go:14-16
- *store.Store satisfies each interface (compile-time assertions) → internal/scim/userstore.go:40, internal/scim/userstore.go:51, internal/scim/groupstore.go:26, internal/scim/attributestore.go:18
- Handler struct holds the stores, a per-caller limiter, and the extended-attribute flag and base URL → internal/scim/handler.go:27-42
- NewHandler reads SCIM_EXTENDED_ATTRIBUTES and SCIM_BASE_URL → internal/scim/handler.go:51-52
- The limiter reads SCIM_RATE_LIMIT and SCIM_RATE_BURST → internal/scim/ratelimit.go:42-46
- Routes() wraps the mux: logRequests → withTimeout → requireToken → limiter.throttle → mux → internal/scim/handler.go:63-98
- Other env var names read here: SCIM_REQUEST_TIMEOUT, SCIM_LOG_REQUESTS → internal/scim/timeout.go:17-22, internal/scim/requestlog.go:19-22
- Body cap is 1 MiB; attribute cap is 256 chars (userName is btree-indexed) → internal/scim/handler.go:20, internal/scim/handler.go:22-24
- Every user write takes the row lock (lockUserTx); PATCH hands the store a patch callback → internal/store/user.go:335-341, internal/scim/handler.go:307, internal/scim/handler.go:457
- Health probes live in this package, mounted on the root mux outside auth → internal/scim/health.go:28-40, cmd/server/main.go:124-125
## File map
- handler.go: /Users and /Groups CRUD, pagination, base URL, error writers → internal/scim/handler.go:101-790
- auth.go (requireToken, authenticate), ratelimit.go, timeout.go, requestlog.go: middleware → internal/scim/auth.go:45-117, internal/scim/ratelimit.go:1-109, internal/scim/timeout.go:15-43, internal/scim/requestlog.go:13-70
- discovery.go: ServiceProviderConfig, ResourceTypes, Schemas → internal/scim/discovery.go:105-334
- filter.go and patch.go: query filter and PATCH parsing → internal/scim/filter.go:19-63, internal/scim/patch.go:14-413
- extended.go: extensible attributes → internal/scim/extended.go:22-46
- mapper.go: store <-> SCIM mapping; models.go: SCIM types → internal/scim/mapper.go:11-116, internal/scim/models.go:10-125
