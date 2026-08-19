# Roadmap

`v1.0.0` is tagged, covering phases 1 through 13 of the
[implementation plan](docs/IMPLEMENTATION_PLAN.md). Phase 14 (the admin
console and the OpenAPI spec with Swagger UI) is complete on top of that and
ships in the next release; see [CHANGELOG.md](CHANGELOG.md) for what's in each.
This document tracks what's deliberately left for later. Each item is a
considered deferral, with a note on why it waited.

## Near-term

- **Per-endpoint webhook subscriptions.** Today there is one delivery target from
  the environment, shared by every tenant. The delivery row already carries a
  `tenant_id` and would gain a subscription reference when per-endpoint delivery
  ships.
- **Dual-token rotation guidance.** Rotation with overlap works today (several
  live tokens per tenant, revoked individually); the roadmap item is documenting
  and smoothing the swap procedure.
- **JSON-lines audit export** for log shipping, layered on top of the
  authoritative `audit_log` table rather than replacing it.

## Later

- **A real extension point for storage.** The `UserStore`/`GroupStore` interfaces
  live under `internal/`, so supplying an implementation means forking. Moving the
  domain types to an importable package turns this into a genuine extension point
  when someone needs it.
- **Broader SCIM coverage** as clients ask for it: bulk operations, sorting,
  ETags, a wider filter grammar, and individually addressable paths into schema
  extensions (today an extension is registered and replaced as a whole object).
- **Pre-authentication rate limiting.** Rate limiting is currently per
  authenticated caller, applied after the token lookup. A pre-auth limiter was
  raised and deferred. Front the service with one at the proxy if unauthenticated
  volume is a concern. It earns a place in the server itself if a real deployment
  needs it.

## Non-goals

- **Wiring ARIA into any decision.** ARIA is advisory: it reads the audit log and
  narrates signals for a human. Giving the model a path into an authorization or
  provisioning decision, or into the auth middleware, would break the core
  security design. This is a permanent non-goal, not a deferral. See
  [SECURITY.md](SECURITY.md).
- **A password or credential store.** SCIMage never accepts or stores user
  passwords; `changePassword` is advertised as unsupported. Identity providers own
  authentication.
