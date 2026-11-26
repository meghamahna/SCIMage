---
id: entrypoint-cmd-server-main
kind: entrypoint
title: Entry: cmd/server/main.go (SCIM server process)
summary: Long-running server: wires logging, store, webhook dispatcher, SCIM and docs on SCIM_ADDR, and the optional console on CONSOLE_ADDR; signal-driven graceful shutdown
tags: [entrypoint, main, server, startup, wiring, listeners, SCIM_ADDR, CONSOLE_ADDR, healthz, readyz, shutdown, env, SIGTERM]
links: [calls:module-scim, calls:module-console, calls:module-webhook, depends_on:module-store, depends_on:module-logging, depends_on:module-apidocs, decided_by:decision-console-separate-listener, related:gotcha-console-docker-bind-address, related:architecture-overview]
sources: [cmd/server/main.go:23-34, cmd/server/main.go:36-63, cmd/server/main.go:65-113, cmd/server/main.go:115-158, cmd/server/main.go:161-203]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- main calls run and exits 1 on error, logging "server stopped" → cmd/server/main.go:58-63
- Startup order: logging.Setup → store.DSNFromEnv → webhook.ConfigFromEnv → store.New → dispatcher goroutine → listeners → wait → shutdown → stop dispatcher → cmd/server/main.go:65-113, cmd/server/main.go:115-203
- Logging is set up first and closed on return → cmd/server/main.go:66-70
- DB DSN from DATABASE_URL or POSTGRES_USER/PASSWORD/DB/PORT (read in store) → cmd/server/main.go:72-75
- Webhook config read before the pool opens, so a bad setting fails at boot → cmd/server/main.go:77-82
- WithChangeEvents is passed only when SCIM_WEBHOOK_URL is set → cmd/server/main.go:84-89
- Dispatcher runs on its own context, cancelled only after listeners drain → cmd/server/main.go:100-104, cmd/server/main.go:106-113
- Main SCIM listener: SCIM_ADDR, default :8080 → cmd/server/main.go:115-118, cmd/server/main.go:136-143
- Root mux: GET /healthz (liveness), GET /readyz (DB reachability), /docs/ apidocs, and / for SCIM routes, all outside the SCIM auth path for health/docs → cmd/server/main.go:120-134
- Console listener is started only when CONSOLE_ADDR is set; it has no WriteTimeout and uses SCIMAGE_ENV as its env label → cmd/server/main.go:40-56, cmd/server/main.go:145-158
- Each listener runs ListenAndServe in its own goroutine; one error channel covers both → cmd/server/main.go:160-167
- SIGINT or SIGTERM starts shutdown with a 15s grace per server; a second signal kills the process → cmd/server/main.go:91, cmd/server/main.go:169-188, cmd/server/main.go:23
- Timeouts: read-header 10s, read 10s, write 30s on SCIM only, idle 120s → cmd/server/main.go:29-34, cmd/server/main.go:136-143
- Env names read here: SCIM_ADDR, CONSOLE_ADDR, SCIMAGE_ENV, SCIM_WEBHOOK_URL (and related SCIM_WEBHOOK_* in the webhook node) → cmd/server/main.go:41-45, cmd/server/main.go:115
