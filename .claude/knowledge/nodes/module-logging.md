---
id: module-logging
kind: module
title: Process logging (internal/logging)
summary: Sets the default slog JSON logger to stdout plus one dated file under LOG_DIR; the audit trail is separate
tags: [logging, logs, slog, json logs, LOG_DIR, LOG_LEVEL, log file, operational logs, log level]
links: [related:module-console-auth, related:gotcha-request-body-logging-opt-in, depends_on:entrypoint-cmd-server-main]
sources: [internal/logging/logging.go:1-74, cmd/server/main.go:65-70]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Setup() configures the default slog logger; called first in cmd/server run() → internal/logging/logging.go:26-28, cmd/server/main.go:65-70
- Output is JSON via slog.NewJSONHandler at the level from LOG_LEVEL (default info) → internal/logging/logging.go:29-32, internal/logging/logging.go:53
- Invalid LOG_LEVEL fails startup with an error naming the allowed values → internal/logging/logging.go:30-31
- Stdout is always written; a file is added through io.MultiWriter when LOG_DIR is not blank → internal/logging/logging.go:39-51
- LOG_DIR unset uses DefaultDir "logs"; LOG_DIR set to empty means stdout only → internal/logging/logging.go:20-23, internal/logging/logging.go:34-37
- File name is scimage-YYYY-MM-DD.log in UTC, opened append-only → internal/logging/logging.go:67-69
- Log directory mode 0700 and log file mode 0600, because request logging can carry user attributes → internal/logging/logging.go:62-69
- One file per day, with no rotation library; size control is left to stdout or the platform → internal/logging/logging.go:57-60
- Audit trail is not in logs: it lives in the audit_log table so the write commits with the change → internal/logging/logging.go:4-7
- Console logs token lookup failures with key id, never the secret → internal/console/auth.go:76-85
- ARIA narrate failures are logged with the error; the operator sees only a generic message → internal/console/handlers.go:734-744
## Gotchas
- Package comment says request bodies may be logged here, but request logging is in internal/scim behind SCIM_LOG_REQUESTS=1 → internal/logging/logging.go:4-5, internal/scim/requestlog.go:15-27
- Containers should set LOG_DIR empty, since a file inside a container is unbounded and unrotated → internal/logging/logging.go:20-23
