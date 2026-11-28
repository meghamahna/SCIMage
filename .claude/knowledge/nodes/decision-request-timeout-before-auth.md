---
id: decision-request-timeout-before-auth
kind: decision
title: Request timeout wraps auth; rate limiting runs after token verification
summary: The timeout sits outside auth so the token lookup (a DB call) is bounded; the per-caller limiter keys on a verified token, so pre-auth flooding is a known gap
tags: [decision, timeout, rate limit, ratelimit, auth, middleware, dos, token bucket, why, order]
links: [related:module-scim, related:component-scim-ratelimit, related:component-scim-auth, related:gotcha-scim-unauthenticated-not-limited, documented_in:doc-architecture]
sources: [docs/ARCHITECTURE.md:33-52, docs/ARCHITECTURE.md:58-75, internal/scim/handler.go:93-98, internal/scim/timeout.go:15-22, internal/scim/ratelimit.go:96]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Decision
- Middleware order: request logging (outermost), request timeout, auth, body capture, rate limiter, router → docs/ARCHITECTURE.md:33-52
- Routes() chains logRequests → withTimeout → requireToken → captureBody → limiter.throttle → mux → internal/scim/handler.go:98
- Default SCIM_REQUEST_TIMEOUT is 10s → internal/scim/timeout.go:15, docs/ARCHITECTURE.md:35-36
- Auth is applied by Routes(), so unregistered paths get the same 401 as real ones → docs/ARCHITECTURE.md:69-75
- The limiter is a token bucket keyed by tenant and key id → internal/scim/ratelimit.go:96
## Why
- The token lookup is a database call that runs before the limiter, so it is the one query an unauthenticated flood can reach unthrottled; the timeout bounds it → docs/ARCHITECTURE.md:62-65
- DATABASE_MAX_CONNS bounds how many such lookups can hold connections at once → docs/ARCHITECTURE.md:66-68
- Known gap: no limiter runs before auth; see gotcha-scim-unauthenticated-not-limited → docs/ARCHITECTURE.md:62-68
