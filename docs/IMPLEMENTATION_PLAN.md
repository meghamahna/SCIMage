# Implementation Plan: SCIM 2.0 Provisioning Server (Go + Postgres)

## Goal
Build a working SCIM 2.0 server in Go that implements the `/Users` and
`/Groups` resources per RFC 7644, backed by Postgres running in Docker, with
production-grade security practices and an AI-assisted audit layer.

## Why Postgres
Running real Postgres in Docker demonstrates schema design, migrations,
and query correctness: the concrete backend skills this project is
built to show.

## Stack
- Go (`net/http`, stdlib for the HTTP layer)
- Postgres 16, run via Docker Compose
- `pgx` for the driver, raw SQL: real queries make the behaviour visible
- `golang-migrate` for schema migrations

## Project structure
```text
/cmd/server           entrypoint for the SCIM server
/cmd/scimage-admin    tenant and token administration (Phase 10)
/cmd/aria             audit reviewer (Phase 12)
/internal/scim        HTTP handlers, SCIM models, auth, rate limiting
/internal/store       Postgres-backed store, audit log and outbox, raw SQL
/internal/webhook     signing and the outbound delivery dispatcher
/internal/logging     structured logging setup
/migrations           SQL migration files
/scripts              env loading, migrations, secret scanning
/.github/workflows    CI
docker-compose.yml
README.md
```

## Milestones

### Phase 1: Infrastructure
- [x] Write `docker-compose.yml` with a `postgres:16` service
- [x] Set connection config via env vars (`DATABASE_URL`)
- [x] Confirm `docker compose up` gives a running, reachable Postgres

### Phase 2: Schema
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
  `userName` is `caseExact=false` (RFC 7643 §4.1), so uniqueness goes on
  `lower(user_name)`. That keeps `bjensen` and `BJensen` one identity and
  gives the Phase 4 409 something to enforce. The same index serves
  lookups, so it replaces the separately sketched `CREATE INDEX`.
- [x] Apply migration on container startup via a make target: `make up`
      chains into `make migrate`, using the official container when the
      host has no `migrate` binary

### Phase 3: Store layer
- [x] Implement `internal/store` with plain SQL:
  - `CreateUser`
  - `GetUser(id)`
  - `ListUsers(limit, offset)`: also returns the total row count, which
    SCIM's `ListResponse` needs for `totalResults`
  - `UpdateUser(id, ...)`
  - `DeactivateUser(id)`: sets `active = false`, preserving history
- [x] Use a connection pool (`pgxpool`)

For Phase 4: `ErrNotFound` and `ErrDuplicateUserName` map to 404 and 409, and
a malformed id returns `ErrNotFound` so junk path parameters answer 404.
`ListUsers` clamps to `store.MaxPageSize` (200): report the returned slice
length as `itemsPerPage`.

### Phase 4: HTTP layer (SCIM endpoints)
- [x] `POST /Users`: 201 + Location header on success, 409 on
      duplicate `userName`
- [x] `GET /Users/{id}`: fetch a single user
- [x] `GET /Users`: list with pagination via `startIndex` / `count`
- [x] `PUT /Users/{id}`: full replace
- [x] `DELETE /Users/{id}`: soft delete, sets `active = false`
- [x] Map SCIM user JSON to and from `users` table rows in one place
      (`internal/scim/mapper.go`) to keep the HTTP layer clean

Routing is `net/http`'s method-pattern mux. Responses are
`application/scim+json`; errors use the SCIM Error schema with `status` as a
string. `cmd/server` wires the store to the handler.

### Phase 5: Auth
- [x] Bearer token middleware, token from env var

`Routes()` applies the middleware itself, so authentication covers the whole
surface. A token below the minimum length rejects every request, so a
configuration error fails closed. Unknown paths are rejected before routing,
keeping responses uniform for an unauthenticated caller.

Comparison is `subtle.ConstantTimeCompare` over SHA-256 digests. Hashing gives
both sides a fixed width, so the comparison is constant-time with respect to
the token's length as well as its content.

### Phase 6: Tests
These landed alongside the code they cover, so each phase shipped verified.
- [x] Spin up Postgres for tests via `docker-compose` (`make test`)
- [x] Integration tests for create/get/update/deactivate against the
      real store
- [x] HTTP handler tests via `httptest` (landed with Phase 4)
- [x] Duplicate-`userName` conflict test, including the case-variant
      collision, a good talking point on constraint handling
- [x] Deterministic under repeated runs. Both packages write to the same
      `users` table and `go test` parallelises across packages, so the
      store's row-count assertions raced against the handler tests
      creating users, reproducible at `-count=20`. The original fix passed
      `-p 1` to serialise packages within a run. Per-tenant scoping (Phase 10)
      later made that isolation structural, so `make test` dropped `-p 1` and
      runs packages concurrently; CI and the pre-commit hook still pass it as
      a belt-and-suspenders

### Phase 7: Security hardening
- [x] Validate every incoming SCIM payload against the expected schema
      before it reaches the DB, with proper SCIM error responses (landed
      with Phase 4; extended here to reject a body `id` that contradicts
      the path, which RFC 7643 §3.1 makes readOnly)
- [x] Use `crypto/subtle.ConstantTimeCompare` for bearer token checks
      (landed with Phase 5)
- [x] Structured audit log for every mutating call: actor, action, target
      user id, timestamp, before/after state. Written to an `audit_log`
      table **in the mutation's own transaction**, so the entry and the
      change commit together: a failed insert rolls the change back with
      it. The before-image comes from a CTE reading the row in the same
      statement as the `UPDATE`, since `OLD` in `RETURNING` arrived in
      Postgres 18 and this runs on 16. Refusals are recorded too, making a
      burst of denials visible to a reviewer
- [x] Rate limiting per token (token bucket), keyed on the caller and
      applied inside auth, so each authenticated caller has its own budget
- [x] All secrets via env vars, with the rotation procedure documented in
      the README. Dual-token rotation is on the roadmap
- [x] `govulncheck` as a CI step for dependency scanning. It earned its
      place immediately: `golang.org/x/text` v0.29.0 had a reachable
      infinite-loop bug via `pgxpool.New`, now on v0.40.0

ARIA reads the `audit_log` table directly. One authoritative copy of
the audit trail is what gives the transactional guarantee its meaning; a
JSON-lines export for log shipping can layer on top.

### Phase 8: Identity provider interoperability
What an identity provider exercises during setup. Built against a live client
as well as the spec, which changed the result twice; see the interop notes.
- [x] `externalId` on `users`: the attribute IdPs use as their own key for
      reconciliation. Migration, model, mapper
- [x] Discovery endpoints: `/ServiceProviderConfig`, `/ResourceTypes`,
      `/Schemas`. Static documents declaring exactly what this server
      supports. A test pins the declaration to the behaviour, so an
      advertised capability matches a working one
- [x] Filtering for `userName eq` and `externalId eq`. A narrow, correct
      subset covers what clients send before every create; other
      expressions answer with `invalidFilter`. `userName` matches through
      the same `lower(user_name)` index that enforces uniqueness, so a
      lookup agrees with what a create would allow
- [x] `PATCH /Users/{id}` for the operations clients actually send, chiefly
      `replace` on `active` for deprovisioning

**Interop notes.** Two client behaviours the spec alone left open, both pinned
by tests:

- Some providers send `emails[].primary` as the string `"true"` where the spec
  types it as boolean. Booleans now accept a JSON boolean or a quoted one, and
  always marshal back as a real boolean.
- Deprovisioning arrives as `PATCH {"op":"replace","path":"active","value":
  false}`, followed by a re-read *by id and by filter* that are expected to
  agree.

`DELETE` is a soft delete by decision: it returns `204`, sets `active = false`
and keeps the row, so a later read shows the user as inactive. RFC 7644 §3.6
allows a provider to retain the resource; keeping it readable preserves the
audit trail's subject and matches how identity providers deprovision in
practice. Both routes converge on the same state on purpose.

### Phase 9: Change delivery
How provisioned users reach the application's own data, which is what makes
this deployable by someone other than its author.
- [x] Signed outbound webhooks on every mutation, with retries and a
      dead-letter path. The event is queued in the mutation's own
      transaction, the same discipline as the audit entry, so a committed
      change is always queued and a rolled-back one leaves the queue as it
      was. HMAC-SHA256 covers the timestamp, delivery id, event type and
      body: signing the timestamp lets a receiver enforce freshness, and
      signing the id and event type keeps its deduplication key and routing
      header authentic. A dispatcher claims due rows with `FOR UPDATE SKIP
      LOCKED`, counting the attempt and extending a lease in one statement,
      so concurrent dispatchers take disjoint sets and an interrupted one's
      rows return on lease expiry
- [x] A `UserStore` interface, with the Postgres store as the default
      implementation, so a Go application can back SCIMage with its own
      schema

**Decisions.** The lease spans the whole batch, matching the sequential send.
A per-send lease would let rows queued behind the current attempt come due
mid-batch, delivering them twice and spending each row's retry budget at
double rate. A `4xx` other than `408`/`429` parks immediately, since the
receiver has already given its verdict. Requests reach the configured endpoint
only, so a `3xx` is reported for the operator to resolve and a signed payload of
user attributes stays on its intended host. `last_error` holds a receiver's
response body, bounded in runes and sanitised of NUL and invalid UTF-8, which
keeps a malformed error body writable and the delivery moving. Events name the
user's transition, so `DELETE` and `PATCH active:false` both emit
`user.deactivated`: the event a receiver most needs to act on. Graceful
shutdown landed here, ahead of Phase 13, to give the dispatcher a defined stop.

**Open for later.** Retention for `delivered` rows belongs with the Phase 13
operational work. `DeadLetters` reads the parked queue; a `webhook replay`
subcommand for `cmd/scimage-admin`, and per-endpoint subscriptions, remain
unbuilt — Phase 10 landed tenants and tokens but not these. Today there is one
endpoint from the environment, and the delivery row would gain a subscription
reference if per-endpoint delivery ships. The `UserStore` interface lives in
`internal/scim`, so supplying an implementation means forking; moving the
domain types to an importable package turns it into a real extension point when
someone needs it.

### Phase 10: Multi-tenancy and issued API tokens
The shape real SCIM service providers ship. It also gives the audit `actor`
and ARIA's per-caller volume signal something to distinguish.

**Addressing.** Tenant in the path (one host, one certificate, plain DNS for
self-hosters): `https://<host>/scim/v2/{tenantID}/Users`. That URL is what a
customer enters as Okta's *Base URL* or Entra's *Tenant URL*.

**Token format.** `scimage_<keyID>_<secret>`. The key ID makes the row
indexable, since a constant-time comparison needs a single candidate, and the
secret authenticates. The `scimage_` prefix lets GitHub secret scanning
recognise a leaked token.

- [x] Migration: `tenants` and `scim_tokens`; `users` gains `tenant_id`;
      uniqueness becomes `(tenant_id, lower(user_name))`, so the same
      `userName` at two customers is two people
- [x] Issue 32 bytes from `crypto/rand`, store `sha256(secret)`. SHA-256
      suits a high-entropy machine-generated secret and keeps bulk
      provisioning fast; a password KDF suits low-entropy human input
- [x] Show the full token once at creation
- [x] Verification order: parse key ID → look up row → check revoked and
      expired → constant-time compare the hash → confirm the token's tenant
      matches the tenant in the URL
- [x] Token metadata: label, `created_at`, `created_by`, `last_used_at`,
      `expires_at`, `revoked_at`
- [x] Rotation with overlap: several live tokens per tenant, revoked
      individually, so a rotation keeps the server running
- [x] `cmd/scimage-admin`: `tenant create`, `token issue`, `token list`,
      `token revoke`. A CLI keeps the privileged surface off the network
- [x] Every store query scoped by `tenant_id`, with cross-tenant isolation
      covered by tests. Per-tenant scoping also makes test isolation
      structural, so `make test` dropped `-p 1` and runs packages
      concurrently (CI and the pre-commit hook keep it as a safety belt)

**Addendum: enterprise governance gaps found after shipping.** Reviewing
Phase 10 against what an enterprise operator would actually need surfaced
three gaps the checklist above didn't ask for:
- [x] `tenants.name` had no uniqueness constraint; two customers could
      silently share a display name. Fixed with a case-insensitive unique
      index, the same reasoning `lower(user_name)` already uses
- [x] `token issue`'s `created_by` was always the hardcoded string
      `"scimage-admin"`, and `tenant create` had no `created_by` at all.
      Both now default to `$USER`/`$USERNAME`, overridable with
      `-created-by`, so provenance is a real operator, not a constant
- [x] Creating a tenant, issuing a token and revoking one weren't
      themselves audited, only the SCIM API mutations were. A new
      `admin_audit_log` table, written in the same transaction as each
      action (identical discipline to `audit_log`), closes that; surfaced
      via `scimage-admin audit list [-tenant <id>]`

### Phase 11: Groups and Extended Attributes
- [x] `/Groups` resource: create, fetch, list, replace, delete
- [x] Membership, including `PATCH` on members
- [x] Group tests against the real store
- [x] Group interop (Entra validator): an empty group returns `members: []`,
      and `excludedAttributes=members` (which Okta and Entra send) is honored
- [x] Per-tenant extensible attributes (Users): a `tenant_attributes` registry
      via `scimage-admin attribute register|list|unregister` (admin-audited),
      with registered keys captured into an additive `users.extended_attributes`
      JSONB column and advertised in `/Schemas`; gated by
      `SCIM_EXTENDED_ATTRIBUTES`, inert when off

**Decisions.** `DELETE /Groups/{id}` is a hard delete, unlike Users: RFC 7643's
Group schema has no `active` attribute, so there is nothing to soft-delete
into. `displayName` uniqueness is enforced per tenant, case-insensitive, the
same reconciliation reasoning `userName` already uses. Membership lives in a
`group_members` join table rather than an array column, validated against
this tenant's `users` in the same statement that inserts it — a foreign or
made-up member id rolls the whole mutation back rather than being silently
dropped. `audit_log` gained a `resource_type` column and `AuditEntry`'s
before/after images became raw JSON rather than a `*store.User`-typed pair:
one audit trail now covers both resources, which is what Phase 12's
ARIA needs to see a bulk group deletion alongside a bulk user
deactivation. Group mutations also enqueue webhook events
(`group.created`/`replaced`/`deleted`) through the existing, resource-agnostic
outbox and dispatcher from Phase 9 — group-to-role mapping is a common reason
enterprises adopt SCIM groups at all, and the dispatcher needed no changes to
carry them.

### Phase 12: ARIA
This tool reads the audit log and produces a plain-English summary for a human
reviewer. It surfaces signal, while every authorization and provisioning
decision stays in deterministic code, AI purely advisory, which is the design
choice worth explaining in an interview. The name reflects that: it advises
on activity, the code decides.
- [ ] CLI (`cmd/aria`) that reads the `audit_log` table
- [ ] Calls an LLM (Claude API) with recent entries to flag patterns
      worth a look: bulk deactivations in a short window, off-hours
      changes, a token spiking in call volume
- [ ] Outputs a plain-English summary a reviewer can read in under a
      minute
- [ ] README section explaining the advisory-only design

### Phase 13: Release engineering
- [x] README: setup, endpoint table, security practices, architecture diagram
- [x] `make` targets: `make up`, `make migrate`, `make test`, `make run`
- [x] Structured logging: JSON with RFC 3339 timestamps, to stdout and a
      dated file under `LOG_DIR` (default `logs/`, empty for stdout only in
      a container). `SCIM_LOG_REQUESTS=1` adds request bodies, which carry
      user attributes, so the directory is `0700` and files `0600`. Landed
      during Phase 8, where reading a client's real requests is what made
      the interop work tractable
- [x] Graceful shutdown: SIGINT/SIGTERM drains the listener, then stops the
      webhook dispatcher. Landed in Phase 9, which needed a defined stop
- [ ] `CHANGELOG.md`, `ROADMAP.md`, `SECURITY.md`, `CONTRIBUTING.md`
- [ ] `/healthz` and `/readyz`
- [ ] Retention for delivered webhook rows
- [ ] Published container image and tagged releases via GoReleaser
- [ ] Okta and Entra setup guides, and a threat model
- [ ] Tag v1.0.0

