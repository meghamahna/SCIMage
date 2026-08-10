# Implementation Plan: SCIM 2.0 Provisioning Server (Go + Postgres)

## Goal
Build a working SCIM 2.0 server in Go that implements the `/Users`
resource per RFC 7644, backed by Postgres running in Docker, with
production-grade security practices and an AI-assisted audit layer.

## Why Postgres
Running real Postgres in Docker demonstrates schema design, migrations,
and query correctness — the concrete backend skills this project is
built to show.

## Stack
- Go (`net/http`, stdlib for the HTTP layer)
- Postgres 16, run via Docker Compose
- `pgx` for the driver, raw SQL (writing real queries is more
  convincing than an ORM abstraction)
- `golang-migrate` for schema migrations

## Project structure
```
/cmd/server            - main.go, entrypoint
/cmd/sage        - AI-assisted audit log reviewer
/internal/scim          - HTTP handlers, request/response models
/internal/store          - Postgres-backed user store
/migrations             - SQL migration files
docker-compose.yml
README.md
```

## Milestones

### Phase 1 — Infrastructure
- [x] Write `docker-compose.yml` with a `postgres:16` service
- [x] Set connection config via env vars (`DATABASE_URL`)
- [x] Confirm `docker compose up` gives a running, reachable Postgres

### Phase 2 — Schema
- [ ] Write migration for a `users` table:
  ```sql
  CREATE TABLE users (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      user_name TEXT UNIQUE NOT NULL,
      given_name TEXT,
      family_name TEXT,
      email TEXT,
      active BOOLEAN NOT NULL DEFAULT true,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  CREATE INDEX idx_users_user_name ON users (user_name);
  ```
- [ ] Apply migration on container startup via a make target

### Phase 3 — Store layer
- [ ] Implement `internal/store` with plain SQL:
  - `CreateUser`
  - `GetUser(id)`
  - `ListUsers(limit, offset)`
  - `UpdateUser(id, ...)`
  - `DeactivateUser(id)` — sets `active = false`, preserving history
- [ ] Use a connection pool (`pgxpool`)

### Phase 4 — HTTP layer (SCIM endpoints)
- [ ] `POST /Users` — 201 + Location header on success, 409 on
      duplicate `userName`
- [ ] `GET /Users/{id}` — fetch a single user
- [ ] `GET /Users` — list with pagination via `startIndex` / `count`
- [ ] `PUT /Users/{id}` — full replace
- [ ] `DELETE /Users/{id}` — soft delete, sets `active = false`
- [ ] Map SCIM user JSON to and from `users` table rows in one place
      (`internal/scim/mapper.go`) to keep the HTTP layer clean

### Phase 5 — Auth
- [ ] Bearer token middleware, token from env var

### Phase 6 — Tests
- [ ] Spin up Postgres for tests via `docker-compose`
- [ ] Integration tests for create/get/update/deactivate against the
      real store
- [ ] HTTP handler tests via `httptest`
- [ ] Duplicate-`userName` conflict test — solid interview talking
      point on constraint handling

### Phase 7 — Security hardening
- [ ] Validate every incoming SCIM payload against the expected schema
      before it reaches the DB, with proper SCIM error responses
- [ ] Use `crypto/subtle.ConstantTimeCompare` for bearer token checks
- [ ] Structured audit log for every mutating call: actor, action,
      target user id, timestamp, before/after state, written as JSON
      lines
- [ ] Rate limiting per token (token bucket)
- [ ] All secrets via env vars, with rotation documented in the README
- [ ] `govulncheck` as a CI step for dependency scanning

### Phase 8 — SAGE: SCIM Audit & Governance Engine
This tool reads the audit log and produces a plain-English summary for
a human reviewer. It surfaces signal — it never makes an authorization
or provisioning decision. Keeping every actual decision in deterministic
code, with AI purely advisory, is the design choice worth explaining in
an interview. The name reflects that: a sage advises, it doesn't decide.
- [ ] CLI (`cmd/sage`) that reads the JSON-lines audit log
- [ ] Calls an LLM (Claude API) with recent entries to flag patterns
      worth a look: bulk deactivations in a short window, off-hours
      changes, a token spiking in call volume
- [ ] Outputs a plain-English summary a reviewer can read in under a
      minute
- [ ] README section explaining the advisory-only design

### Phase 9 — Polish and release
- [ ] README: setup (`docker compose up`, migrations, run server),
      endpoint table, schema, architecture diagram
- [ ] `make` targets: `make up`, `make migrate`, `make test`, `make run`
- [ ] Push to GitHub, tag v0.1.0

## Time estimate
Phases 1-2 (Docker + schema): one evening.
Phases 3-5 (store + endpoints + auth): two evenings.
Phase 6 (tests): one evening.
Phase 7 (security hardening): one evening.
Phase 8 (SAGE): one evening.
Phase 9 (polish): under an hour.
