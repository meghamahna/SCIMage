---
id: gotcha-bare-go-test-skips-postgres
kind: gotcha
title: Bare go test skips the Postgres tests
summary: Running go test without POSTGRES_* or DATABASE_URL skips store and scim integration tests and can look green; use make test
tags: [gotcha, go test, skip, skipped, integration test, postgres, make test, with-env, env, false green, DATABASE_URL]
links: [related:command-dev-commands, related:workflow-ci, related:workflow-precommit-hook, related:decision-real-postgres-tests]
sources: [internal/store/store_test.go:21-24, internal/scim/handler_test.go:44-48, internal/scim/handler_test.go:117-118, Makefile:94-96, scripts/with-env.sh:12-19, internal/store/store.go:106-113]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- store tests call t.Skipf when DSNFromEnv fails, with a message pointing to `make test` → internal/store/store_test.go:21-24
- scim TestMain records skipReason and runs without a database when DSNFromEnv fails; those tests skip → internal/scim/handler_test.go:44-48, internal/scim/handler_test.go:117-118
- DSNFromEnv needs DATABASE_URL or all of POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB → internal/store/store.go:106-113
- `make test` loads .env through with-env.sh, so the real database is used → Makefile:94-96, scripts/with-env.sh:12-19
- Pre-commit and CI both set the env, so their test runs do hit Postgres → .githooks/pre-commit:40-42, .github/workflows/ci.yml:30-36
