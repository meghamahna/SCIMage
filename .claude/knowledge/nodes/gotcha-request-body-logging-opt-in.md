---
id: gotcha-request-body-logging-opt-in
kind: gotcha
title: Request body logging is opt-in and lives in internal/scim
summary: SCIM_LOG_REQUESTS=1 records request bodies (user attributes) to the normal log stream; Authorization is never logged
tags: [gotcha, logging, request logs, SCIM_LOG_REQUESTS, body, PII, user attributes, debug logging]
links: [related:module-logging, related:module-scim]
sources: [internal/scim/requestlog.go:15-27, internal/scim/requestlog.go:29-56]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Off unless SCIM_LOG_REQUESTS=1 → internal/scim/requestlog.go:19-22
- When on, it logs a warning at startup and writes method, path, status, user agent, query and body (2 KB cap) → internal/scim/requestlog.go:26-27, internal/scim/requestlog.go:43-54
- Authorization header is not recorded → internal/scim/requestlog.go:19-20
- Entries go through the same slog JSON output and LOG_DIR file as other logs → internal/logging/logging.go:44-51
