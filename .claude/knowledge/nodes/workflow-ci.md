---
id: workflow-ci
kind: workflow
title: CI (GitHub Actions)
summary: ci.yml runs gofmt, go vet, migrations and go test -p 1 against a Postgres 16 service on pushes to main and on PRs, plus govulncheck
tags: [ci, continuous integration, github actions, workflow, pipeline, gate, govulncheck, postgres service, pull request, test gate, merge check]
links: [related:command-dev-commands, related:workflow-migrations, related:workflow-precommit-hook, related:workflow-docker, related:decision-real-postgres-tests, related:gotcha-bare-go-test-skips-postgres]
sources: [.github/workflows/ci.yml:1-80]
updated: 2026-11-26
verified_commit: ec35d99
---
## Facts
- Triggers: push to main and every pull_request → .github/workflows/ci.yml:3-6
- Job `test` runs on ubuntu-latest with a postgres:16 service container, scimage credentials and a pg_isready health check → .github/workflows/ci.yml:9-28
- Service credentials are also job env; no DATABASE_URL is set, so the app builds the DSN from POSTGRES_* → .github/workflows/ci.yml:30-36
- Go version comes from go.mod via setup-go with module cache → .github/workflows/ci.yml:41-44, go.mod:3
- Step order: checkout, setup-go, gofmt check, go vet, migrations, go test → .github/workflows/ci.yml:38-67
- gofmt gate fails the job if `gofmt -l .` prints any file → .github/workflows/ci.yml:46-51
- Migrations run in a migrate/migrate:v4.18.1 container with host networking, before the tests → .github/workflows/ci.yml:56-63
- Tests run as `go test -p 1 ./...` because both packages share the users table → .github/workflows/ci.yml:65-67
- Job `govulncheck` is separate, needs no database, and runs `govulncheck ./...` → .github/workflows/ci.yml:69-80
- Gotcha: govulncheck is installed at `@latest`, so the scanner version is unpinned → .github/workflows/ci.yml:79
- Gotcha: CI has no secret-scan step; the scan runs only in the local pre-commit hook and the Claude commit hook → .github/workflows/ci.yml:38-80, .githooks/pre-commit:11-14
- Dockerfile pins golang:1.26.11 separately from go.mod; keep the two in step → Dockerfile:2, go.mod:3
