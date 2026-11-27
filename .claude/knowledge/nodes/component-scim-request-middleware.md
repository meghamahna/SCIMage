---
id: component-scim-request-middleware
kind: module
title: SCIM request timeout and request logging
summary: Per-request 10s context deadline covering auth and handler, and an opt-in request logger that records bodies only for authenticated calls
tags: [timeout, deadline, context, request log, logging, SCIM_REQUEST_TIMEOUT, SCIM_LOG_REQUESTS, debugging, middleware]
links: [implements:module-scim, related:api-scim-routes, related:gotcha-scim-idp-quirks, related:gotcha-request-body-logging-opt-in]
sources: [internal/scim/timeout.go:1-43, internal/scim/requestlog.go:1-265, internal/scim/handler.go:93-98]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Default request deadline is 10s; it bounds how long one request holds a DB connection → internal/scim/timeout.go:10-15
- SCIM_REQUEST_TIMEOUT takes a Go duration; unset or invalid falls back to 10s; zero or negative disables the deadline → internal/scim/timeout.go:17-30
- Deadline is applied outside auth, so the token lookup is covered too → internal/scim/handler.go:94-98, internal/scim/timeout.go:12-14
- Middleware order: logRequests, then withTimeout, then requireToken, then captureBody, then the rate limiter, then the mux → internal/scim/handler.go:98
- Request logging is off unless SCIM_LOG_REQUESTS=1; a warning is logged when it is on → internal/scim/requestlog.go:54-60
- Authenticated requests log method, path, status, user agent, query and a redacted body → internal/scim/requestlog.go:70-87
- Unauthenticated requests log method, path and status only, cut to 16 and 256 bytes → internal/scim/requestlog.go:70-75
- Full rules for body capture and redaction: gotcha-request-body-logging-opt-in → internal/scim/requestlog.go:98-253
- The Authorization header is never logged → internal/scim/requestlog.go:70-82
- Logging wraps everything, so it records requests that auth rejects, without their bodies → internal/scim/handler.go:94-98
