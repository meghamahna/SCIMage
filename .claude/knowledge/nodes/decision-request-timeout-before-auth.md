---
id: decision-request-timeout-before-auth
kind: decision
title: Request timeout wraps auth; rate limiting runs after token verification
summary: The timeout is the outermost wrapper so the token lookup (a DB call) is bounded, and the per-caller limiter keys on a verified token, so pre-auth flooding is a known gap
tags: [decision, timeout, rate limit, ratelimit, auth, middleware, dos, token bucket, why, order]
links: [related:module-scim, related:component-scim-ratelimit, related:component-scim-auth, related:gotcha-scim-unauthenticated-not-limited]
sources: [docs/ARCHITECTURE.md:24-57, internal/scim/handler.go:63-98]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Decision
- Middleware order: request timeout (outermost), then auth, then rate limiter, then router → docs/ARCHITECTURE.md:27-35
- Routes() applies logRequests → withTimeout → requireToken → limiter.throttle → mux → internal/scim/handler.go:63-98
- Auth is applied by Routes(), so unregistered paths get the same 401 as real ones → docs/ARCHITECTURE.md:55-57
- Timeout default SCIM_REQUEST_TIMEOUT is 10s → docs/ARCHITECTURE.md:27-29
- Rate limiter is a token bucket keyed per issued token → docs/ARCHITECTURE.md:35
## Why
- The token lookup is a database call that runs before the limiter, so it is the one query an unauthenticated flood can reach unthrottled; the timeout bounds it → docs/ARCHITECTURE.md:47-50
- DATABASE_MAX_CONNS bounds how many such lookups can hold connections at once → docs/ARCHITECTURE.md:51-53
- Known gap: no limiter runs before auth; see the linked gotcha → docs/ARCHITECTURE.md:47-50
