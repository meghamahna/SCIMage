---
id: component-scim-request-middleware
kind: module
title: SCIM request timeout and request logging
summary: Per-request 10s context deadline covering auth and handler, and an opt-in request body logger that never logs Authorization
tags: [timeout, deadline, context, request log, logging, SCIM_REQUEST_TIMEOUT, SCIM_LOG_REQUESTS, audit, debugging]
links: [implements:module-scim, related:api-scim-routes, related:gotcha-scim-idp-quirks]
sources: [internal/scim/timeout.go:1-43, internal/scim/requestlog.go:1-70]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Default request deadline is 10s; it bounds how long one request holds a DB connection → internal/scim/timeout.go:10-15
- SCIM_REQUEST_TIMEOUT takes a Go duration; unset or invalid falls back to 10s; zero or negative disables the deadline → internal/scim/timeout.go:17-30
- Deadline is applied outside auth, so the token lookup is covered too → internal/scim/handler.go:94-98, internal/scim/timeout.go:12-14
- Request logging is off unless SCIM_LOG_REQUESTS=1; a warning is logged when it is on → internal/scim/requestlog.go:19-27
- When on, logs method, path, status, user agent, query and up to 2048 bytes of body (bodies carry user attributes) → internal/scim/requestlog.go:13, internal/scim/requestlog.go:29-62
- Authorization header is never logged → internal/scim/requestlog.go:19-20
- Logging wraps everything, so it records requests that auth rejects → internal/scim/handler.go:94-98
