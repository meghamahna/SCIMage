# Threat Model

This document walks SCIMage's trust boundaries, the assets on each side, the
threats that cross them, and the mitigations already in the code. It also states
the assumptions the design makes about its deployment and the risks it
deliberately leaves to the operator.

It is a working threat model for a portfolio-grade server, not a formal
certification. The mechanisms named here are implemented and covered by tests;
[ARCHITECTURE.md](ARCHITECTURE.md) has the detail, and [SECURITY.md](../SECURITY.md)
has the reporting process.

## Scope

In scope: the SCIM HTTP server (`cmd/server`), the admin CLI
(`cmd/scimage-admin`), the webhook dispatcher, and ARIA (`cmd/aria`), plus the
Postgres schema they share.

Out of scope: the security of the identity provider itself, the TLS-terminating
proxy, the host OS, the Postgres deployment's own hardening, and the webhook
receiver — each is an assumption listed at the end.

## Assets

| Asset | Why it matters |
| --- | --- |
| User and group PII | Names, `userName`, emails, and any registered extended attributes. The data the server exists to move. |
| Bearer tokens | The credential for the whole SCIM surface. Stored only as `sha256(secret)`. |
| Webhook signing secret | Lets a receiver trust a delivery. A leak allows forged change events. |
| Database credentials | Full access to every tenant's data. |
| Audit trail | `audit_log` and `admin_audit_log` are the record of who changed what. Their integrity is the point. |
| ARIA's LLM API key | A credential for an external service, and the channel audit-derived text travels over. |

## Trust boundaries

```text
                        ┌──────────────── trusted (private) ────────────────┐
   Identity provider    │                                                    │
   (Okta / Entra) ──B1──▶  SCIM server ──B2──▶ Postgres ◀──B4── admin CLI    │
                        │       │                  ▲                          │
                        │       └──B3──▶ receiver  └──B5── ARIA ──B5'──▶ LLM  │
                        └────────────────────────────────────────────────────┘
   B1 crosses the public network. B3 and B5' leave the trusted zone outbound.
```

- **B1 — IdP → server.** Public, authenticated. The primary attack surface.
- **B2 — server → Postgres.** Private. Assumed not exposed to the network.
- **B3 — server → webhook receiver.** Outbound, signed, to one configured host.
- **B4 — operator → admin CLI → Postgres.** Off-network, direct to the database.
- **B5 — ARIA → audit log** (read) and **B5' — ARIA → LLM** (outbound).

## Threats and mitigations

### B1 — Identity provider to server

- **Spoofing a caller / stolen or forged token.** Tokens are
  `scimage_<keyID>_<secret>`; only `sha256(secret)` is stored. Verification looks
  up the row by key id and compares with `crypto/subtle.ConstantTimeCompare`
  against the stored hash — constant-time in both content and length, since the
  compare is over fixed-width digests. A malformed token (wrong prefix, empty key
  id or secret) is rejected before any lookup; an unknown key id, a revoked or
  expired token, a wrong secret, or a tenant that doesn't match the URL each fail
  in turn — so anything that isn't a live, correct token for this tenant fails
  closed.
- **Reaching another tenant's data (elevation / information disclosure).** The
  token must name the tenant in the URL, and *every* store query is scoped by
  `tenant_id`, including lookups by id. A valid token for one tenant naming
  another tenant's real resource id gets the same `404` as a made-up id — the
  isolation is structural. Covered by cross-tenant tests.
- **Reaching an unauthenticated path.** `Routes()` wraps the whole surface in the
  token check, and unknown paths are rejected uniformly, so there is no
  before-auth path to probe and responses don't distinguish real resources from
  invented ones to an unauthenticated caller.
- **Tampering via malformed payloads.** Every payload is validated against the
  expected schema before it touches the database. A body `id` that contradicts the
  path (which the spec makes read-only) is refused. Request bodies are capped
  (`1 MiB`) and individual attributes are length-bounded, so an oversized field
  can't become a `500` against an indexed column.
- **Repudiation.** Every create/replace/deactivate/delete on Users and Groups
  writes an `audit_log` row — actor, action, resource type, target id, timestamp,
  before/after — **inside the transaction that makes the change**, so the record
  and the change commit together or not at all. Refused mutations are recorded
  too, so a burst of denials is visible. The audit actor's IP comes from the
  connection, not a caller-supplied `X-Forwarded-For`, which would otherwise let a
  caller forge the recorded origin.
- **Denial of service (authenticated).** Rate limiting is per authenticated
  caller (token-bucket), so one tenant's flood spends only its own budget. See the
  residual-risk note on pre-authentication volume.

### B2 — Server to Postgres

- **Injection.** Queries are parameterized throughout `internal/store`; no SQL is
  built by string concatenation from request data.
- **Credential exposure.** The DSN comes from the environment, and the password is
  percent-encoded when assembled from parts so reserved characters can't corrupt
  the connection string. Credentials are never logged.

### B3 — Server to webhook receiver

- **Forged events at the receiver.** Deliveries are signed with HMAC-SHA256 over
  the timestamp, delivery id, event type and body. Signing the timestamp lets a
  receiver enforce freshness; signing the id and type keeps its dedup key and
  routing authentic. A configured endpoint requires a secret at startup, so
  nothing goes out unsigned.
- **Exfiltration via redirect (information disclosure).** The payload carries user
  attributes and is signed for the configured host only. Redirects are never
  followed — a `3xx` is reported for the operator, not chased to another host.
  Plaintext `http` is refused unless explicitly opted in for a local receiver.
- **A hostile receiver response.** The error body stored in `last_error` is read
  bounded, truncated in runes, and stripped of NUL and invalid UTF-8, so a
  malformed or oversized response can't stream unbounded data into a column or
  wedge a delivery.

### B4 — Operator to admin CLI

- **Repudiation of privileged actions.** Creating a tenant, issuing a token and
  revoking one each write an `admin_audit_log` entry in the same transaction as
  the action, naming a real operator by default (`$USER`, overridable).
- **Keeping the privileged surface off the network.** Tenant and token
  administration is a CLI against the database, not an HTTP endpoint, so it isn't
  reachable from the internet at all.

### B5 — ARIA

- **The advisory-only guarantee (tampering with decisions).** ARIA computes its
  signals in deterministic Go and calls an LLM only to narrate already-computed
  facts. The model has no path into the auth middleware or any mutating code, and
  cannot make or influence an authorization or provisioning decision. This is the
  central design property; wiring model output into a decision would break it.
- **Data leaving the boundary (B5', information disclosure).** ARIA sends
  audit-derived text (counts, timestamps, actor key ids) to the configured LLM
  endpoint. The endpoint is operator-chosen and can be a local model, so the
  operator decides whether any data leaves their environment at all. The API key
  comes from the environment like every other secret.

## Assumptions and residual risks

These are the edges the design leaves to the deployment. Each is a deliberate
boundary, noted here so an operator can close it.

- **TLS terminates upstream.** The server speaks HTTP and assumes a
  TLS-terminating proxy or load balancer in front. Set `SCIM_BASE_URL` so
  generated links stay `https` even though `r.TLS` is nil behind the proxy.
- **Postgres and the admin host are private.** B2 and B4 assume the database and
  the machine running `scimage-admin` are not exposed. Encryption at rest and
  Postgres hardening are the operator's responsibility.
- **Pre-authentication request volume.** Token verification does a database lookup
  *before* the per-caller rate limiter applies, so a flood of unauthenticated
  requests still costs a lookup each. A pre-auth limiter was considered and
  deferred; front the service with one at the proxy if this matters. `/readyz` is
  likewise unauthenticated and touches the pool on each hit — standard for a
  readiness probe, but a load source to be aware of.
- **Concurrent updates to one user.** `PATCH` reads, folds operations, and writes
  back in separate statements, so two concurrent changes to the same user could
  lose one. Provisioning traffic for a single user is serial in practice; making
  it airtight would require applying operations inside the store transaction.
- **Token and secret custody.** A token is shown once and never recoverable; its
  security after issuance is the holder's. Rotation with overlap exists so a
  suspected-leaked token can be revoked without downtime.
- **The webhook receiver.** Once a signed event is delivered, what the receiver
  does with it — and whether it verifies the signature (`webhook.Verify` is
  provided) — is outside this boundary.
