---
id: api-env-vars
kind: api
title: Environment variables
summary: Catalogue of env var names grouped by purpose (database, server, console, rate limit, logging, webhooks, ARIA, tooling); names and defaults only; docs/CONFIGURATION.md is the authoritative reference
tags: [env, environment variables, config, configuration, .env, .env.example, settings, POSTGRES_USER, POSTGRES_PORT, DATABASE_URL, SCIM_ADDR, SCIM_WEBHOOK, CONSOLE_ADDR, CONSOLE_ALLOWED_HOSTS, ARIA_LLM, LOG_DIR, flags, defaults]
links: [related:command-dev-commands, related:workflow-docker, related:module-logging, related:module-webhook, related:module-aria, related:module-console, related:decision-console-host-allowlist, related:gotcha-container-log-dir]
sources: [docs/CONFIGURATION.md:1-6, docs/CONFIGURATION.md:141, docs/CONFIGURATION.md:222, .env.example:1-96, docker-compose.yml:10, scripts/with-env.sh:6-19, internal/store/store.go:51-55, internal/store/store.go:63-72, internal/store/store.go:104-113, internal/store/store.go:133-150, .gitignore:2-4]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Authoritative env reference is docs/CONFIGURATION.md; this node is a name-only index → docs/CONFIGURATION.md:1-6
- Source file: copy .env.example to .env; the example is kept in step with docs/CONFIGURATION.md → .env.example:1-3
- Secrets come from env only; .env is gitignored, .env.example is the exception → .gitignore:2-4, .env.example:1-3
- Tenant SCIM tokens are not env vars; they are issued with scimage-admin → .env.example:31-32
- **Database:** POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB, POSTGRES_PORT (compose, migrations, tests) → .env.example:6-9
- **Database:** POSTGRES_PORT is the host port the Compose Postgres publishes on 127.0.0.1 (default 5432); the container port stays 5432 → docs/CONFIGURATION.md:158, docker-compose.yml:10
- **Database:** DATABASE_URL is preferred over POSTGRES_* when set → internal/store/store.go:106-111, .env.example:13
- **Database:** DATABASE_MAX_CONNS (pool ceiling) and DATABASE_MAX_CONN_LIFETIME; unset means pgx defaults → internal/store/store.go:68-72, internal/store/store.go:133-150, .env.example:16-17
- **Server:** SCIM_ADDR (default :8080) → .env.example:20
- **Server:** SCIM_REQUEST_TIMEOUT, default 10s; non-positive disables it → .env.example:22-25
- **Server:** SCIM_BASE_URL sets external host and scheme in Location and meta.location → .env.example:27-29
- **Console:** CONSOLE_ADDR is opt-in; the console starts only when set; loopback default → .env.example:34-39
- **Console:** CONSOLE_ALLOWED_HOSTS adds Host names the console answers to, beyond loopback and CONSOLE_ADDR's host; a malformed entry stops startup → .env.example:41-44, internal/console/host.go:20-42, docs/CONFIGURATION.md:222
- **Console:** SCIMAGE_ENV is a cosmetic sidebar label → .env.example:47
- **Rate limit:** SCIM_RATE_LIMIT (req/s per caller, 0 disables) and SCIM_RATE_BURST → .env.example:49-52
- **Logging:** LOG_DIR (default logs; empty means stdout only), LOG_LEVEL (default info) → .env.example:54-58, internal/logging/logging.go:20-34
- **Logging:** SCIM_LOG_REQUESTS logs request bodies, which contain user attributes → .env.example:60-62
- **Extensible attributes:** SCIM_EXTENDED_ATTRIBUTES=1 enables the tenant attribute registry → .env.example:64-67
- **Webhooks:** SCIM_WEBHOOK_URL (unset disables delivery) and SCIM_WEBHOOK_SECRET; URL without secret is a startup error → .env.example:69-74, internal/webhook/dispatcher.go:82-106
- **Webhooks:** SCIM_WEBHOOK_ALLOW_HTTP (local receivers only), SCIM_WEBHOOK_MAX_ATTEMPTS (default 6) → .env.example:76-81
- **Webhooks:** SCIM_WEBHOOK_RETENTION_DAYS (default 30; 0 keeps forever) → .env.example:83-85
- **ARIA:** ARIA_LLM_BASE_URL, ARIA_LLM_API_KEY, ARIA_LLM_MODEL; all three enable the LLM narration → .env.example:87-93
- **ARIA:** ARIA_TIMEZONE for the off-hours check; defaults to host zone → .env.example:95-96
- **Tooling:** with-env.sh keeps exported shell variables over .env values → scripts/with-env.sh:6, scripts/with-env.sh:12-18
- **Tooling:** MIGRATE_IMAGE overrides the migrate/migrate:v4.18.1 fallback image → scripts/migrate.sh:18
- **CI:** the workflow sets POSTGRES_* as job env and no DATABASE_URL → .github/workflows/ci.yml:32-36
