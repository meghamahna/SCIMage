---
id: command-dev-commands
kind: command
title: Dev commands
summary: Make targets and Go commands to install hooks, start Postgres, migrate, build, test, lint, format and run the server
tags: [build, commands, install, lint, run, test, make, makefile, format, gofmt, goimports, vet, docker compose, up, down, reset, hooks, seed]
links: [related:architecture-overview, related:workflow-docker, related:workflow-migrations, related:workflow-ci, related:workflow-precommit-hook, related:gotcha-bare-go-test-skips-postgres, related:api-env-vars]
sources: [Makefile:12-168, scripts/with-env.sh:1-21, CLAUDE.md:34-42]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Bare `make` prints the target list; `help` is the default goal → Makefile:16,19-24
- Build: `go build ./...` (no make target) → CLAUDE.md:35
- Test: `make test` runs `go test ./...` through with-env.sh so .env is loaded and tests reach compose Postgres → Makefile:94-96
- Lint: `go vet ./...` (no make target; pre-commit and CI run it) → .githooks/pre-commit:35, .github/workflows/ci.yml:53-54
- Format: `make fmt` runs `gofmt -w .` then `goimports -w .`; CI checks `gofmt -l .` → Makefile:102-105, .github/workflows/ci.yml:46-51
- Run the server on the host: `make run` → `go run ./cmd/server` via with-env.sh → Makefile:98-100
- Start Postgres: `make up` (optional SERVICE=) starts compose services and then runs migrations → Makefile:30-34
- Stop: `make stop` and `make restart` keep data; `make down` removes containers and network → Makefile:38-51
- `make reset` runs `docker compose down -v`, wiping the data volume, then re-inits → Makefile:54-59
- `make ps` and `make logs` wrap docker compose ps and logs -f → Makefile:61-66
- Migrations: `make migrate`, `make migrate-down` (one step), `make migrate-version` → Makefile:73-82
- Tenants and tokens: `make tenant NAME=`, `tenant-list`, `token TENANT= LABEL= [EXPIRES=]`, `token-list`, `token-revoke KEY=` → Makefile:110-126
- Admin console credentials: `make console-token`, `console-token-list`, `console-token-revoke KEY=` → Makefile:128-137
- Extra attributes and audit: `make attr-register/attr-list/attr-unregister` and `make audit-list [TENANT=]` → Makefile:143-154, 139-141
- ARIA advisory review: `make aria [TENANT=] [SINCE=] [TZ=]`; needs ARIA_LLM_* only when there is something to summarise → Makefile:158-162
- Helper targets call `scripts/with-env.sh`, which sources .env and keeps exported shell vars over .env values → scripts/with-env.sh:6,12-19
- One-time per clone: `make hooks-install` sets `core.hooksPath` to .githooks → Makefile:164-168
- Gotcha: bare `go test` without DB env skips the Postgres tests → gotcha-bare-go-test-skips-postgres
