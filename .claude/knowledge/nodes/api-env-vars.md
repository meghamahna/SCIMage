---
id: api-env-vars
kind: api
title: Environment variables
summary: Catalogue of env var names grouped by purpose (database, server, console, rate limit, logging, webhooks, ARIA, tooling); names and defaults only; docs/CONFIGURATION.md is the authoritative reference
tags: [env, environment variables, config, configuration, .env, .env.example, settings, POSTGRES_USER, POSTGRES_PORT, DATABASE_URL, SCIM_ADDR, SCIM_WEBHOOK, ARIA_LLM, LOG_DIR, flags, defaults]
links: [related:command-dev-commands, related:workflow-docker, related:module-logging, related:module-webhook, related:module-aria, related:module-console, related:gotcha-container-log-dir]
sources: [docs/CONFIGURATION.md:1-6, docs/CONFIGURATION.md:130, .env.example:1-91, docker-compose.yml:10, scripts/with-env.sh:6-19, internal/store/store.go:51-55, internal/store/store.go:63-72, internal/store/store.go:104-113, internal/store/store.go:133-150, .gitignore:2-4]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Authoritative env reference is docs/CONFIGURATION.md; this node is a name-only index → docs/CONFIGURATION.md:1-6
- Source file: copy .env.example to .env; the example is kept in step with docs/CONFIGURATION.md → .env.example:1-3
- Secrets come from env only; .env is gitignored, .env.example is the exception → .gitignore:2-4, .env.example:1-3
- Tenant SCIM tokens are not env vars; they are issued with scimage-admin → .env.example:31-32
- **Database:** POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB, POSTGRES_PORT (compose, migrations, tests) → .env.example:5-9
- **Database:** POSTGRES_PORT is the host port the Compose Postgres publishes on 127.0.0.1 (default 5432); the container port stays 5432 → docs/CONFIGURATION.md:130, docker-compose.yml:10
- **Database:** DATABASE_URL is preferred over POSTGRES_* when set → internal/store/store.go:106-111, .env.example:11-13
- **Database:** DATABASE_MAX_CONNS (pool ceiling) and DATABASE_MAX_CONN_LIFETIME; unset means pgx defaults → internal/store/store.go:68-72, internal/store/store.go:133-150, .env.example:15-17
- **Server:** SCIM_ADDR (default :8080) → .env.example:19-20
- **Server:** SCIM_REQUEST_TIMEOUT, default 10s; non-positive disables it → .env.example:22-25
- **Server:** SCIM_BASE_URL sets external host and scheme in Location and meta.location → .env.example:27-29
- **Console:** CONSOLE_ADDR is opt-in; the console starts only when set; loopback default → .env.example:34-39
- **Console:** SCIMAGE_ENV is a cosmetic sidebar label → .env.example:41-42
- **Rate limit:** SCIM_RATE_LIMIT (req/s per caller, 0 disables) and SCIM_RATE_BURST → .env.example:44-47
- **Logging:** LOG_DIR (default logs; empty means stdout only), LOG_LEVEL (default info) → .env.example:49-53, internal/logging/logging.go:20-34
- **Logging:** SCIM_LOG_REQUESTS logs request bodies, which contain user attributes → .env.example:55-57
- **Extensible attributes:** SCIM_EXTENDED_ATTRIBUTES=1 enables the tenant attribute registry → .env.example:59-62
- **Webhooks:** SCIM_WEBHOOK_URL (unset disables delivery) and SCIM_WEBHOOK_SECRET; URL without secret is a startup error → .env.example:64-69, internal/webhook/dispatcher.go:82-106
- **Webhooks:** SCIM_WEBHOOK_ALLOW_HTTP (local receivers only), SCIM_WEBHOOK_MAX_ATTEMPTS (default 6) → .env.example:71-76
- **Webhooks:** SCIM_WEBHOOK_RETENTION_DAYS (default 30; 0 keeps forever) → .env.example:78-80
- **ARIA:** ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL; all three enable the LLM narration → .env.example:82-88
- **ARIA:** ARIA_TIMEZONE for the off-hours check; defaults to host zone → .env.example:90-91
- **Tooling:** with-env.sh keeps exported shell variables over .env values → scripts/with-env.sh:6, scripts/with-env.sh:12-18
- **Tooling:** MIGRATE_IMAGE overrides the migrate/migrate:v4.18.1 fallback image → scripts/migrate.sh:18
- **CI:** the workflow sets POSTGRES_* as job env and no DATABASE_URL → .github/workflows/ci.yml:32-36
