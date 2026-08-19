# Changelog

All notable changes to SCIMage are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it tags a
release.

## [Unreleased]

Phase 14 of the [implementation plan](docs/IMPLEMENTATION_PLAN.md): operator
and integrator tooling, built on top of the `1.0.0` surface below.

### Added

- **Admin console** (`/console`): an opt-in admin web UI that starts on a second
  listener only when `CONSOLE_ADDR` is set (`127.0.0.1:8090` recommended,
  loopback). It covers the day-to-day of the `scimage-admin` CLI: a landing page;
  view and mutate tenants (each with its SCIM base URL), tokens and attributes;
  watch webhook delivery health and replay parked events; read the SCIM audit
  log, the admin audit log, and ARIA's report with an optional on-demand ARIA
  briefing. Issuing the console's own login credential stays CLI-only, by design.
  It authenticates with a dedicated system-wide credential
  issued by `scimage-admin console-token issue` (with `list`/`revoke`), compared
  in constant time and accepted as an HTTP Basic password or a Bearer header.
  Every mutating route reuses the same audited `store.*` functions the CLI calls
  and carries a stateless signed CSRF token.
- **Webhook dead-letter replay**: one or several parked deliveries can be
  requeued with a fresh retry budget from `scimage-admin webhook replay <id>` /
  `replay-all` or the console's Webhooks page (per-row or bulk-select). The
  requeue is guarded on the row still being parked, refused while webhooks are
  disabled (nothing would ever pick a requeued delivery up), and writes a
  `webhook.replay` admin-audit row per delivery in its own transaction. A
  Pending deliveries view keeps a replayed or retrying event visible until it
  lands or parks again, instead of it disappearing between replay and outcome.
- **OpenAPI spec and Swagger UI** (`/docs`): a hand-written OpenAPI 3.0 document
  and a vendored Swagger UI (no CDN), embedded and served on the SCIM server. It
  is unauthenticated by design, since it describes the public protocol and
  carries no tenant data.

### Security

- **Bounded database exposure to request volume**: `DATABASE_MAX_CONNS` /
  `DATABASE_MAX_CONN_LIFETIME` cap the Postgres pool instead of relying on
  pgx's unconfigured defaults; `SCIM_REQUEST_TIMEOUT` (default 10s) wraps the
  request context outside auth, so a stuck token lookup (the one query an
  unauthenticated flood can already reach, ahead of the per-caller rate
  limiter) fails the request instead of holding a connection and a goroutine
  indefinitely; both listeners gained `ReadTimeout`/`IdleTimeout`, and the
  SCIM listener also `WriteTimeout` (30s, left unset on the console since its
  ARIA narration can legitimately run close to a minute). Narrows, rather
  than closes, the pre-auth-volume risk already on record in
  [THREAT-MODEL.md](docs/THREAT-MODEL.md).

## [1.0.0] - 2026-08-16

The first tagged release. Captures phases 1 through 13 of the
[implementation plan](docs/IMPLEMENTATION_PLAN.md), where each phase's
decisions and trade-offs are recorded as they were made.

### Added

- **SCIM 2.0 `/Users`**: create, fetch, list (paginated with
  `startIndex`/`count`), full replace, `PATCH`, and soft-delete. `DELETE` and
  `PATCH active:false` converge on the same inactive state, preserving the row.
- **SCIM 2.0 `/Groups`**: create, fetch, list, replace, `PATCH` (including member
  add/remove/replace), and hard delete. Membership is validated against the
  tenant's own users in the same statement that writes it.
- **Discovery endpoints**: `/ServiceProviderConfig`, `/ResourceTypes`,
  `/Schemas`, declaring exactly what the server supports, pinned to behaviour by
  tests.
- **Filtering**: `userName eq`, `displayName eq` and `externalId eq`, the
  reconciliation lookups clients send before a create. Other expressions return
  `invalidFilter`.
- **Identity-provider interop**: `externalId`, tolerant boolean parsing for
  providers that send `"true"`, and `excludedAttributes=members`. Validated
  against Microsoft's Entra ID SCIM validator.
- **Multi-tenancy with issued API tokens**: tenant in the path
  (`/scim/v2/{tenantID}/…`), `scimage_<keyID>_<secret>` tokens with only
  `sha256(secret)` stored, rotation with overlap, and the `scimage-admin` CLI for
  tenant and token administration.
- **Per-tenant extensible attributes**: an operator can register extra attribute
  names per tenant, captured into a JSONB column and advertised in `/Schemas`;
  gated by `SCIM_EXTENDED_ATTRIBUTES`, inert when off.
- **Signed outbound webhooks**: every mutation queues a change event in the
  mutation's own transaction, delivered at-least-once with retries, a dead-letter
  queue, and HMAC-SHA256 signatures over the timestamp, delivery id, event type
  and body.
- **Retention for delivered webhook rows**: the dispatcher prunes delivered rows
  older than `SCIM_WEBHOOK_RETENTION_DAYS` (default 30, `0` disables) on an hourly
  sweep. Pending and dead-lettered rows are never touched.
- **ARIA** (`cmd/aria`): an advisory audit reviewer that computes activity
  signals in Go and asks an LLM only to narrate them, over any OpenAI-compatible
  endpoint. Advisory only, by design.
- **Operational endpoints**: `/healthz` (liveness, no database dependency) and
  `/readyz` (readiness, pings Postgres), mounted outside the auth and tenant path.
- **Structured JSON logging** with RFC 3339 timestamps, to stdout and a dated
  file under `LOG_DIR`. Optional request-body logging (`SCIM_LOG_REQUESTS=1`)
  writes to `0700`/`0600` paths since bodies carry user attributes.
- **Graceful shutdown**: SIGINT/SIGTERM drains the listener, then stops the
  webhook dispatcher.

### Security

- Bearer tokens compared with `crypto/subtle.ConstantTimeCompare` over SHA-256
  digests; authentication applied by the router itself; a too-short token fails
  closed.
- Cross-tenant isolation enforced on every query by `tenant_id`, covered by
  tests.
- Every mutation and every privileged CLI action writes an audit entry in the
  same transaction as the change; refusals are recorded too.
- `govulncheck` runs in CI for dependency scanning.

[Unreleased]: https://github.com/meghamahna/SCIMage/compare/v1.0.0...main
[1.0.0]: https://github.com/meghamahna/SCIMage/releases/tag/v1.0.0
