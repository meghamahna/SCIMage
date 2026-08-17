# Roadmap

Phases 1 through 12 are complete, and phase 13 (release engineering) is in
progress. The [implementation plan](docs/IMPLEMENTATION_PLAN.md) has the
phase-by-phase detail. This document tracks what's deliberately left for later —
each item is a considered deferral, with a note on why it
waited.

## Finishing 1.0.0

The remaining release-engineering work before the first tag:

- **A published container image, optionally.** A `Dockerfile` already ships, so
  anyone can build and run the server (see the README Deploy section). Pushing a
  prebuilt image to a registry such as GitHub Container Registry (`ghcr.io`) can
  layer on later; GoReleaser was considered and dropped as unnecessary for a
  container-run server.
- **Tag `v1.0.0`** once the docs below have landed.

## Near-term

- **`webhook replay` subcommand** for `scimage-admin` — `DeadLetters` already
  reads the parked queue and replay is a documented SQL update; a CLI wrapper
  makes it a first-class operation.
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
  raised and deferred — front the service with one at the proxy if unauthenticated
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
