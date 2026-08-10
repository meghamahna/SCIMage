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
- [x] Write migration for a `users` table:
  ```sql
  CREATE TABLE users (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      user_name TEXT NOT NULL,
      given_name TEXT,
      family_name TEXT,
      email TEXT,
      active BOOLEAN NOT NULL DEFAULT true,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  CREATE UNIQUE INDEX idx_users_user_name_lower ON users (lower(user_name));
  ```
  Changed from the SQL originally sketched here: `userName` is
  `caseExact=false` (RFC 7643 §4.1), so uniqueness goes on
  `lower(user_name)` — otherwise `bjensen` and `BJensen` both insert and
  the Phase 4 409 means nothing. That index also serves lookups, so the
  separate `CREATE INDEX` alongside a column `UNIQUE` was redundant.
- [x] Apply migration on container startup via a make target — `make up`
      chains into `make migrate`; no host `migrate` binary needed

### Phase 3 — Store layer
- [x] Implement `internal/store` with plain SQL:
  - `CreateUser`
  - `GetUser(id)`
  - `ListUsers(limit, offset)` — also returns the total row count, which
    SCIM's `ListResponse` needs for `totalResults`
  - `UpdateUser(id, ...)`
  - `DeactivateUser(id)` — sets `active = false`, preserving history
- [x] Use a connection pool (`pgxpool`)

For Phase 4: `ErrNotFound` and `ErrDuplicateUserName` map to 404 and 409,
and a malformed id returns `ErrNotFound` so junk path parameters are 404s,
not 500s. `ListUsers` clamps to `store.MaxPageSize` (200) — report the
returned slice length as `itemsPerPage`, not the requested count.

### Phase 4 — HTTP layer (SCIM endpoints)
- [x] `POST /Users` — 201 + Location header on success, 409 on
      duplicate `userName`
- [x] `GET /Users/{id}` — fetch a single user
- [x] `GET /Users` — list with pagination via `startIndex` / `count`
- [x] `PUT /Users/{id}` — full replace
- [x] `DELETE /Users/{id}` — soft delete, sets `active = false`
- [x] Map SCIM user JSON to and from `users` table rows in one place
      (`internal/scim/mapper.go`) to keep the HTTP layer clean

Routing is `net/http`'s method-pattern mux, no framework. Responses are
`application/scim+json`; errors use the SCIM Error schema with `status` as a
string. `cmd/server` wires the store to the handler — no auth yet, that's
Phase 5, so **don't expose this build.**

### Phase 5 — Auth
- [x] Bearer token middleware, token from env var

`Routes()` applies the middleware itself, and a missing or too-short token
rejects every request rather than serving openly — hashing both sides means
an empty token would otherwise match a request carrying no header at all.
Unknown paths are rejected before routing, so a 404 can't tell an anonymous
caller which resources exist. Comparison is `subtle.ConstantTimeCompare`
over SHA-256 digests: comparing raw tokens returns early on a length
mismatch and would leak the token's length.

### Phase 6 — Tests
These landed alongside the code they cover rather than in one late pass — a
store or handler with no tests against the real database isn't verified.
- [x] Spin up Postgres for tests via `docker-compose` (`make test`)
- [x] Integration tests for create/get/update/deactivate against the
      real store
- [x] HTTP handler tests via `httptest` (landed with Phase 4)
- [x] Duplicate-`userName` conflict test, including the case-variant
      collision — solid interview talking point on constraint handling
- [x] Deterministic under repeated runs. Both packages write to the same
      `users` table and `go test` parallelises across packages, so the
      store's exact row-count assertions raced against the handler tests
      creating users — reproducible at `-count=20`, invisible at
      `-count=1`. `make test` and the pre-commit hook now pass `-p 1`.
      That's a stopgap: it serialises packages but doesn't stop two
      developers sharing one compose Postgres from colliding. Per-tenant
      scoping of the store would make the isolation structural and let
      `-p 1` go

### Phase 7 — Security hardening
- [ ] Validate every incoming SCIM payload against the expected schema
      before it reaches the DB, with proper SCIM error responses
- [x] Use `crypto/subtle.ConstantTimeCompare` for bearer token checks
      (landed with Phase 5)
- [ ] Structured audit log for every mutating call: actor, action,
      target user id, timestamp, before/after state, written as JSON
      lines. `UpdateUser`/`DeactivateUser` currently return only the
      after-state, so capturing before/after atomically needs either
      `UPDATE ... RETURNING` of the old row or a transaction on the
      store — decide that rather than doing a non-atomic read-then-write
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
