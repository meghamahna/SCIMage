---
id: gotcha-container-log-dir
kind: gotcha
title: Container logs default to a file under logs/
summary: The image sets no LOG_DIR, so the default writes a file under relative logs/ unless the runner sets LOG_DIR empty for stdout-only logging
tags: [gotcha, docker, LOG_DIR, logs, container, stdout, logging, distroless, log file, rotation]
links: [related:workflow-docker, related:api-env-vars, related:module-logging]
sources: [Dockerfile:19-28, internal/logging/logging.go:20-24, internal/logging/logging.go:27-29, internal/logging/logging.go:34-50, .env.example:49-52]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Logging default directory is the relative `logs`, used when LOG_DIR is unset → internal/logging/logging.go:20-24,34-36
- Logs always go to stdout; the file is added on top → internal/logging/logging.go:27-29
- Empty LOG_DIR skips the file, so output is stdout only → internal/logging/logging.go:44-50
- The Dockerfile sets no LOG_DIR and no WORKDIR, so the container uses the default → Dockerfile:19-28
- .env.example says to set LOG_DIR empty in a container, where the runtime collects stdout → .env.example:49-52
