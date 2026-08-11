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
```text
/cmd/server           entrypoint for the SCIM server
/cmd/scimage-admin    tenant and token administration (Phase 10)
/cmd/sage             audit reviewer (Phase 12)
/internal/scim        HTTP handlers, SCIM models, auth, rate limiting
/internal/store       Postgres-backed store and audit log, raw SQL
/internal/logging     structured logging setup
/migrations           SQL migration files
/scripts              env loading, migrations, secret scanning
/.github/workflows    CI
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

Routing is `net/http`'s method-pattern mux. Responses are
`application/scim+json`; errors use the SCIM Error schema with `status` as a
string. `cmd/server` wires the store to the handler.

### Phase 5 — Auth
- [x] Bearer token middleware, token from env var

`Routes()` applies the middleware itself, so authentication covers the whole
surface. A token below the minimum length rejects every request, which keeps
configuration errors failing closed. Unknown paths are rejected before
routing, so responses stay uniform for an unauthenticated caller.

Comparison is `subtle.ConstantTimeCompare` over SHA-256 digests. Hashing
gives both sides a fixed width, so the comparison is constant-time with
respect to the token's length as well as its content.

### Phase 6 — Tests
These landed alongside the code they cover, so each phase shipped verified.
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
      `-count=1`. `make test` and the pre-commit hook now pass `-p 1`,
      which serialises packages within a run. Per-tenant scoping of the
      store will make that isolation structural, at which point `-p 1`
      becomes redundant

### Phase 7 — Security hardening
- [x] Validate every incoming SCIM payload against the expected schema
      before it reaches the DB, with proper SCIM error responses (landed
      with Phase 4; extended here to reject a body `id` that contradicts
      the path, which RFC 7643 §3.1 makes readOnly)
- [x] Use `crypto/subtle.ConstantTimeCompare` for bearer token checks
      (landed with Phase 5)
- [x] Structured audit log for every mutating call: actor, action,
      target user id, timestamp, before/after state. Written to an
      `audit_log` table **in the mutation's own transaction**, so a user
      cannot be created, replaced or deactivated without its entry — if
      the entry fails to insert, the change rolls back with it. The
      before-image comes from a data-modifying CTE reading the row in
      the same statement as the `UPDATE`; Postgres only gained `OLD` in
      `RETURNING` at 18 and this runs on 16. Refusals are recorded as
      well, which makes a burst of denials visible to a reviewer. The log
      lives in Postgres so it shares the mutation's commit — the property
      that makes "atomic" accurate
- [x] Rate limiting per token (token bucket), keyed on the caller and
      applied inside auth, so each authenticated caller has its own budget
- [x] All secrets via env vars, with the rotation procedure documented in
      the README. Dual-token rotation, which removes the restart from that
      procedure, is on the roadmap
- [x] `govulncheck` as a CI step for dependency scanning. It earned its
      place immediately: `golang.org/x/text` v0.29.0 had a reachable
      infinite-loop bug via `pgxpool.New`, now on v0.40.0

SAGE reads the `audit_log` table directly. Keeping one authoritative copy of
the audit trail is what makes the transactional guarantee meaningful; a
JSON-lines export for log shipping can be layered on top of it.

### Phase 8 — Identity provider interoperability
What an identity provider exercises during setup. Built against a live client
rather than the spec alone, which changed the result twice — see the interop
notes below.
- [x] `externalId` on `users` — the attribute IdPs use as their own key for
      reconciliation. Migration, model, mapper
- [x] Discovery endpoints: `/ServiceProviderConfig`, `/ResourceTypes`,
      `/Schemas`. Static documents that declare exactly what this server
      supports. A test pins the declaration to the behaviour, so a
      capability can't be advertised before it works or stay denied after
- [x] Filtering for `userName eq` and `externalId eq`. A narrow, correct
      subset covers what clients send before every create; other
      expressions answer with `invalidFilter`. `userName` matches through
      the same `lower(user_name)` index that enforces uniqueness, so a
      lookup agrees with what a create would allow
- [x] `PATCH /Users/{id}` for the operations clients actually send, chiefly
      `replace` on `active` for deprovisioning

**Interop notes.** Two client behaviours that RFC 7643 alone would have left
broken, both pinned by tests:

- Some providers send `emails[].primary` as the string `"true"` where the spec
  types it as boolean, which fails the whole decode and turns every create into
  a 400. Booleans accept a JSON boolean or a quoted one, and always marshal
  back as a real boolean.
- Deprovisioning arrives as `PATCH {"op":"replace","path":"active","value":
  false}`, followed by a re-read *by id and by filter* which are expected to
  agree — not as `DELETE`.

`DELETE` is a soft delete by decision: it returns `204`, sets `active = false`
and keeps the row, so a later read still shows the user as inactive. RFC 7644
§3.6 allows a provider to retain the resource and describes answering `404`
afterwards; keeping it readable preserves the audit trail's subject and matches
how identity providers actually deprovision, which is `PATCH active:false`.
Both routes converge on the same state on purpose.

### Phase 9 — Change delivery
How provisioned users reach the application's own data, which is what makes
this deployable by someone other than its author.
- [x] Signed outbound webhooks on every mutation, with retries and a
      dead-letter path. The event is queued in the mutation's own
      transaction, the same discipline as the audit entry — a transactional
      outbox rather than a send after `COMMIT`, which would lose events on a
      crash and could fire for a change that rolled back. HMAC-SHA256 over
      timestamp and body, with the timestamp *inside* the signed material so
      a capture can't be replayed. A dispatcher claims due rows with `FOR
      UPDATE SKIP LOCKED`, counting the attempt and pushing a lease forward
      in the same statement, so two dispatchers never send the same event
      and a crashed one's rows come back on lease expiry
- [x] A `UserStore` interface, with the current Postgres store as the
      default implementation, so a Go application can back SCIMage with its
      own schema

**Deferred:** the interface lives in `internal/scim`, so supplying an
implementation means forking rather than importing. Moving the domain types to
an importable package is what would make it a real extension point; it is a
mechanical change and nothing external depends on the current layout, so it
waits until someone needs it.

**Notes.** A `4xx` other than `408`/`429` is dead-lettered immediately rather
than retried: the receiver understood the request and rejected it, so another
attempt sends identical bytes for the same answer. Redirects are not followed —
the payload carries user attributes and is signed for the configured endpoint,
so a `302` would hand both to a host nobody configured. `last_error` holds a
receiver's response body, so it is bounded in runes and stripped of NUL and
invalid UTF-8; without that, a malformed error body fails its own write and
leaves the delivery stuck in flight. Graceful shutdown landed here, ahead of
Phase 13, because the dispatcher needs a defined stop.

Two things review caught that are worth remembering. The claim lease has to
cover the whole batch, not one send: a batch is delivered sequentially, so a
per-send lease lets rows queued behind the current attempt come due again
mid-batch — double-delivering them and, because the attempt is counted at claim
time, spending each row's retry budget twice as fast. And the delivery id and
event type are inside the signed material, not just headers alongside it,
because a receiver is told to deduplicate and route on them; an unauthenticated
dedup key would let a capture replay under a fresh id.

**Still open.** `delivered` rows are never pruned, so the table grows without
bound — retention belongs with the Phase 13 operational work. `DeadLetters`
reads the parked queue but nothing replays it yet; the natural home is
`cmd/scimage-admin` in Phase 10. Per-endpoint subscriptions are Phase 10 too:
today there is one endpoint from the environment, and the delivery row gains a
subscription reference when tenants arrive.

### Phase 10 — Multi-tenancy and issued API tokens
The shape real SCIM service providers ship. It also gives the audit `actor`
and SAGE's per-caller volume signal something to distinguish.

**Addressing.** Tenant in the path — one host, one certificate, no wildcard
DNS for self-hosters: `https://<host>/scim/v2/{tenantID}/Users`. That URL is
what a customer enters as Okta's *Base URL* or Entra's *Tenant URL*.

**Token format.** `scimage_<keyID>_<secret>`. The key ID makes the row
indexable, since a constant-time comparison needs a single candidate, and the
secret authenticates. The `scimage_` prefix lets GitHub secret scanning
recognise a leaked token.

- [ ] Migration: `tenants` and `scim_tokens`; `users` gains `tenant_id`;
      uniqueness becomes `(tenant_id, lower(user_name))`, so the same
      `userName` at two customers is two people
- [ ] Issue 32 bytes from `crypto/rand`, store `sha256(secret)`. SHA-256
      suits a high-entropy machine-generated secret and keeps bulk
      provisioning fast; a password KDF is designed for low-entropy human
      input
- [ ] Show the full token once at creation
- [ ] Verification order: parse key ID → look up row → check revoked and
      expired → constant-time compare the hash → confirm the token's tenant
      matches the tenant in the URL
- [ ] Token metadata: label, `created_at`, `created_by`, `last_used_at`,
      `expires_at`, `revoked_at`
- [ ] Rotation with overlap: several live tokens per tenant, revoked
      individually, so a rotation needs no restart
- [ ] `cmd/scimage-admin`: `tenant create`, `token issue`, `token list`,
      `token revoke`. A CLI keeps the privileged surface off the network
- [ ] Every store query scoped by `tenant_id`, with cross-tenant isolation
      covered by tests. Per-tenant scoping also makes test isolation
      structural, retiring `-p 1`

### Phase 11 — Groups
- [ ] `/Groups` resource: create, fetch, list, replace, delete
- [ ] Membership, including `PATCH` on members
- [ ] Group tests against the real store

### Phase 12 — SAGE: SCIM Audit & Governance Engine
This tool reads the audit log and produces a plain-English summary for a
human reviewer. It surfaces signal, while every authorization and
provisioning decision stays in deterministic code — AI purely advisory, which
is the design choice worth explaining in an interview. The name reflects
that: a sage advises, the code decides.
- [ ] CLI (`cmd/sage`) that reads the `audit_log` table
- [ ] Calls an LLM (Claude API) with recent entries to flag patterns
      worth a look: bulk deactivations in a short window, off-hours
      changes, a token spiking in call volume
- [ ] Outputs a plain-English summary a reviewer can read in under a
      minute
- [ ] README section explaining the advisory-only design

### Phase 13 — Release engineering
- [x] README: setup, endpoint table, security practices, architecture diagram
- [x] `make` targets: `make up`, `make migrate`, `make test`, `make run`
- [x] Structured logging: JSON with RFC 3339 timestamps, to stdout and a
      dated file under `LOG_DIR` (default `logs/`, empty for stdout only in
      a container). `SCIM_LOG_REQUESTS=1` adds request bodies, which carry
      user attributes, so the directory is `0700` and files `0600`. Landed
      during Phase 8, where reading a client's real requests is what made
      the interop work tractable
- [ ] `CHANGELOG.md`, `ROADMAP.md`, `SECURITY.md`, `CONTRIBUTING.md`
- [ ] `/healthz` and `/readyz`. Graceful shutdown landed in Phase 9, which
      needed a defined stop for the webhook dispatcher: SIGINT/SIGTERM drains
      the listener, then stops the dispatcher
- [ ] Published container image and tagged releases via GoReleaser
- [ ] Okta and Entra setup guides, and a threat model
- [ ] Tag v1.0.0

## Time estimate
Phases 1-2 (Docker + schema): one evening.
Phases 3-5 (store + endpoints + auth): two evenings.
Phase 6 (tests): landed alongside the phases above.
Phase 7 (security hardening): one evening.
Phase 8 (IdP interoperability): a few evenings, paced by a live tenant.
Phase 9 (change delivery): several evenings — webhook delivery earns its
own design pass.
Phase 10 (multi-tenancy + tokens): a week, touching schema, store, router
and CLI.
Phase 11 (Groups): a week.
Phase 12 (SAGE): one evening.
Phase 13 (release engineering): spread across the phases above.
