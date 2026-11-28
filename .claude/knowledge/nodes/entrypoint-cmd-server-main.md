---
id: entrypoint-cmd-server-main
kind: entrypoint
title: Entry: cmd/server/main.go (SCIM server process)
summary: Long-running server: wires logging, store, webhook dispatcher, SCIM and docs on SCIM_ADDR, and the optional console on CONSOLE_ADDR; signal-driven graceful shutdown
tags: [entrypoint, main, server, startup, wiring, listeners, SCIM_ADDR, CONSOLE_ADDR, healthz, readyz, shutdown, env, SIGTERM]
links: [calls:module-scim, calls:module-console, calls:module-webhook, depends_on:module-store, depends_on:module-logging, depends_on:module-apidocs, decided_by:decision-console-separate-listener, related:gotcha-console-docker-bind-address, related:architecture-overview]
sources: [cmd/server/main.go:22-62, cmd/server/main.go:64-209]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- main calls run and exits 1 on error, logging "server stopped" → cmd/server/main.go:64-69
- Startup order: logging.Setup → store.DSNFromEnv → webhook.ConfigFromEnv → store.New → dispatcher goroutine → listeners → wait → shutdown → stop dispatcher → cmd/server/main.go:71-119, cmd/server/main.go:162-204
- Logging is set up first and closed on return → cmd/server/main.go:72-76
- DB DSN from DATABASE_URL or POSTGRES_USER/PASSWORD/DB/PORT (read in store) → cmd/server/main.go:78-81
- Webhook config read before the pool opens, so a bad setting fails at boot → cmd/server/main.go:83-88
- WithChangeEvents is passed only when the webhook config is enabled (SCIM_WEBHOOK_URL set) → cmd/server/main.go:92-95
- Dispatcher runs on its own context, cancelled only after listeners drain → cmd/server/main.go:109-119, cmd/server/main.go:203-204
- Main SCIM listener: SCIM_ADDR, default :8080 → cmd/server/main.go:121-124, cmd/server/main.go:142-150
- Root mux: GET /healthz (liveness), GET /readyz (DB reachability), /docs/ apidocs, and / for SCIM routes, outside the SCIM auth path for health/docs → cmd/server/main.go:126-140
- Console listener is started only when CONSOLE_ADDR is set; it has no WriteTimeout and uses SCIMAGE_ENV as its env label → cmd/server/main.go:41-62, cmd/server/main.go:152-165
- Both listeners cap request headers at 64 KiB (maxHeaderBytes, below net/http's 1 MiB default) → cmd/server/main.go:36-39, cmd/server/main.go:60, cmd/server/main.go:149
- Each listener runs ListenAndServe in its own goroutine; one error channel covers both → cmd/server/main.go:167-174
- SIGINT or SIGTERM starts shutdown with a 15s grace per server; a second signal kills the process → cmd/server/main.go:97, cmd/server/main.go:182-195, cmd/server/main.go:22-23
- Timeouts: read-header 10s, read 10s, write 30s on SCIM only, idle 120s → cmd/server/main.go:29-34, cmd/server/main.go:142-150
- Env names read here: CONSOLE_ADDR, SCIMAGE_ENV, SCIM_ADDR (SCIM_WEBHOOK_* are read by the webhook package) → cmd/server/main.go:46, cmd/server/main.go:50, cmd/server/main.go:121
