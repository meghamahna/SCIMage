# 🧱 Architecture

How SCIMage is put together, and why. The [README](../README.md) covers what it
does; this covers the mechanics behind it.

<!-- toc -->
## 📑 Contents

- [Request path](#-request-path)
- [Storage model](#-storage-model)
- [Multi-tenancy](#-multi-tenancy)
- [The UserStore and GroupStore interfaces](#-the-userstore-and-groupstore-interfaces)
- [Extensible attributes](#-extensible-attributes)
- [Change delivery](#-change-delivery)
  - [The outbox](#-the-outbox)
  - [Claiming and leases](#-claiming-and-leases)
  - [Retry and dead-letter](#-retry-and-dead-letter)
  - [Retention](#-retention)
  - [Signing](#-signing)
  - [Event semantics](#-event-semantics)
- [Admin console](#-admin-console)
- [Audit review (ARIA)](#-audit-review-aria)
- [Logging](#-logging)
- [Shutdown](#-shutdown)

<!-- /toc -->

## 📡 Request path

```text
Identity provider
   ↓  SCIM request to /scim/v2/{tenantID}/..., Bearer token
Request logging      outermost, so a call auth rejects still gets a log line
   ↓
Request timeout      bounds everything below at SCIM_REQUEST_TIMEOUT (default
                     10s), so a stuck auth lookup or query fails the request
                     instead of holding a connection and a goroutine forever
   ↓
Auth middleware      looks up the token by key id, checks revoked/expired,
                     constant-time compares the secret, confirms the token's
                     own tenant matches {tenantID}, applied by Routes() itself
   ↓
Body capture         records the request body for the log, only for an
                     authenticated caller
   ↓
Rate limiter         token bucket, keyed per issued token
   ↓
Router               net/http method patterns
   ↓
Handler              validates the payload, maps it to a row, scoped to {tenantID}
   ↓
Store                raw SQL, one transaction per mutation, every query scoped by tenant_id
```

The path is deliberately plain. Standard library `net/http` and raw SQL keep the
behaviour visible in the code, and Postgres is the single source of truth.

Request logging wraps the timeout so that rejected calls are logged too, and
body capture sits inside auth so only an authenticated caller's body is ever
read for the log (see [Logging](#-logging)). The request timeout sits outside
auth, on purpose: the token lookup
`requireToken` performs is itself a database call, and it runs ahead of the
rate limiter (below), so it's the one query in this path a flood of
unauthenticated requests can reach unthrottled. Bounding it here means that
flood costs bounded time and one connection per request, not an open-ended
hold. `DATABASE_MAX_CONNS` bounds the other side of the same problem: how many
of those connections can exist at once.

Authentication is applied by `Routes()` rather than per handler, so it covers the
whole surface structurally, including paths that resolve to nothing, which are
rejected before routing so responses stay uniform for an unauthenticated caller.
It reads `{tenantID}` straight out of the URL path rather than through the
router's own path-value matching: `requireToken` wraps the mux and runs before
the mux has matched anything, which is the only point `net/http`'s own
`r.PathValue` gets populated.

Three paths are mounted on the SCIM listener outside that auth, because none
carries tenant data. `/healthz` reports that the process is up and `/readyz`
reports whether the database answers, so an orchestrator needs no token or
tenant. `/docs` serves a hand-written OpenAPI 3.0 spec and a vendored Swagger UI
(`internal/apidocs`), unauthenticated so an integrator can read it before they
have a token.

The admin console (`internal/console`) is a separate surface on its own
listener. See [Admin console](#-admin-console).

## 💾 Storage model

Ten tables, described in `/migrations`:

| Table | Holds |
| --- | --- |
| `tenants` | One row per customer organization. `id` is an app-generated, prefixed opaque string (`tenant_...`), never a renamable slug: it's pasted into the customer's IdP once and has to stay stable. `name` is unique, case-insensitively, so two customers can't silently share a display name |
| `scim_tokens` | Issued credentials: `sha256` of the secret half, never the secret; label, timestamps, `revoked_at`/`expires_at` |
| `console_tokens` | System-wide credentials for the admin console, a sibling of `scim_tokens` but with no `tenant_id`: the console's admin surface spans every tenant, so its credential isn't scoped to one. Same `sha256`-only storage, label, timestamps, `revoked_at`/`expires_at` |
| `users` | The provisioned directory, scoped by `tenant_id`. Deactivation is a soft delete, so history keeps a subject |
| `groups` | Groups, scoped by `tenant_id`. Unlike `users`, `DELETE` is a real deletion: the Group schema has no `active` attribute to soft-delete into |
| `group_members` | The membership join table: `(group_id, user_id)`, with every reference validated against the same tenant before it's inserted |
| `audit_log` | One row per mutating SCIM call, for either resource: tenant, resource type, actor, action, target, timestamp, before/after |
| `admin_audit_log` | One row per privileged CLI action: who created a tenant, issued or revoked a token, registered an attribute, and when |
| `tenant_attributes` | The per-tenant extensible-attribute registry: which extra attribute names to capture into `users.extended_attributes`, and the type to declare in `/Schemas` |
| `webhook_deliveries` | The outbound queue: tenant, payload, status, attempts, lease |

Migration `000009` also puts a `BEFORE INSERT` trigger on `audit_log` that
raises an error when the session setting `scimage.simulate_audit_failure` is
`true`. A store test sets it to prove that a failed audit write rolls back the
change it belongs to. In any session that never sets the setting, the trigger
does nothing.

## 🏢 Multi-tenancy

One SCIMage process and one Postgres database serve every tenant: the shared
pool model, not one deployment per customer. A tenant is a row plus a foreign
key on everything else; isolation is enforced by scoping every query on
`tenant_id`, not by separate infrastructure.

**Addressing.** `https://<host>/scim/v2/{tenantID}/Users`: one host, one
certificate. That URL, plus an issued token, is what a customer pastes into
Okta's *Base URL* or Entra's *Tenant URL*.

**Tokens.** `scimage_<keyID>_<secret>`. `keyID` is the indexable lookup key
(not secret, the same role a username plays), and `secret` is 32 bytes from
`crypto/rand`. Only `sha256(secret)` is stored. Verification order: parse the
key id, look up the row, check `revoked_at`/`expires_at`, constant-time
compare the secret's hash, then confirm the row's `tenant_id` matches the
`{tenantID}` in the path. Every failure (unknown key, revoked, expired, wrong
secret, right secret but wrong tenant) answers the same generic 401, so a
caller can't learn which check failed or probe which tenants exist.

**Isolation holds even against a known id.** `GetUser`/`UpdateUser`/
`DeactivateUser` and their Group equivalents are scoped by `tenant_id` *and*
`id` in the same `WHERE` clause, so a token from tenant A naming tenant B's
real user or group id gets the same "not found" as a made-up one: the
isolation doesn't depend on a caller never guessing a valid UUID. A group's
membership carries the same guarantee one level deeper: every member id is
validated against this tenant's own `users` in the same statement that adds
it, so a group can't reference another tenant's user even if a caller
already knows its id.

**Issuing is a CLI, not an endpoint.** `cmd/scimage-admin` creates tenants and
issues/lists/revokes tokens by talking to Postgres directly. Keeping that
surface off the network means there's no HTTP endpoint that can create a
credential, which is a smaller attack surface than an admin API would be.

**Privileged actions are audited too, not just SCIM API mutations.**
Creating a tenant, issuing a token and revoking a token each write an
`admin_audit_log` entry (actor, action, target, timestamp) in the same
transaction as the change, the identical discipline `audit_log` already
applies to user mutations. `actor` defaults to `$USER`/`$USERNAME` and can be
overridden with `-created-by`, so the trail names a real operator. A revoke that
turns out to be a no-op (the
token was already dead) writes nothing, so a retried command doesn't pad the
trail with duplicate entries. `scimage-admin audit list [-tenant <id>]`
surfaces it.

**A mutation writes its row (user or group) and its audit entry in one
transaction**, plus its outbound event when change delivery is configured.
They commit together, so a change always carries its record and its
notification. The store owns those writes, which keeps a handler from being
able to skip them.

`audit_log` carries a `resource_type` column (`user` or `group`) and its
before/after images are raw JSON rather than a type-specific pair, so one
table serves both resources without one drifting out of step with the
other, the design ARIA (Phase 12) reads from.

With `SCIM_WEBHOOK_URL` unset the store skips the outbox, so the queue stays
empty while nothing is draining it.

The before-image in an audit entry comes from a CTE reading the row in the same
statement as the `UPDATE`, since `OLD` in `RETURNING` arrived in Postgres 18 and
this runs on 16. Every update path locks the row first, inside the same
transaction, scoped by `tenant_id`, so the before-image is the row as it stood
when the lock was taken and no concurrent write can land before the commit.

`PATCH` applies its operations after taking that lock. The store methods
`PatchUser` and `PatchGroup` lock the row, read it, run the handler's patch
function on what they read, and write the result, all in one transaction. The
patch function does no I/O. Two concurrent `PATCH` requests to one resource run
one after the other, so a deactivation or a removed group member is never
overwritten by a request that read the older state. The server does not support
`ETag` or `If-Match`.

Refusals are recorded too, as rows with result `denied`: a duplicate
`userName`, `displayName` or `externalId` (users and groups, on create, replace
and `PATCH`), a missing user or group (including on `PATCH`), an invalid
`PATCH`, an invalid group member reference, and the handler's refusals: `413`
for an oversized body on `POST`, `PUT` and `PATCH`, and `400` for an unparseable
body, a schema validation failure or a body `id` that differs from the path. A refusal is written by `RecordRefusal` after any
open transaction has rolled back, since no row changed and there is nothing to
commit together. The write ignores client disconnects, times out after 5
seconds, and is best-effort: a failure is logged. A target id that isn't a uuid
is stored as null. A burst of denials is visible to a reviewer.

Requests rejected before a handler runs write no audit row. A failed
authentication (`401`) logs one `authentication failed` warning with a reason
class, the key id, the tenant id from the path and the remote address. The key
id and tenant id are logged only when they have the shape of an issued key id
and tenant id, and are blank otherwise. A `429` logs one `rate limit exceeded`
warning with the key id, tenant id, method and path, with the path truncated to
256 bytes.

## 🧩 The UserStore and GroupStore interfaces

The handler depends on `scim.UserStore` and `scim.GroupStore` rather than on
Postgres directly. The bundled store is the default implementation of both,
and an application with its own tables can supply either or both.

An implementation carries three obligations the compiler leaves open:

1. **Write the audit entry in the change's own transaction.** This is why
   `AuditRecord` is a parameter on every mutating method rather than something
   the handler records afterwards.
2. **Return a non-nil result alongside a nil error.** The handler dereferences
   what it gets back directly.
3. **Scope every method to the `tenantID` argument.** The handler passes the
   tenant resolved from the caller's own token, not from anything the request
   body names. An implementation that ignored it would reopen the cross-tenant
   isolation the bundled store enforces structurally.

The interface currently lives in `internal/scim`, so supplying an implementation
means forking rather than importing. Moving the domain types to an importable
package turns it into a published extension point.

## 🧬 Extensible attributes

The typed `users` columns are a deliberate, minimal set. Rather than grow them
to chase every attribute an identity provider might map, the server offers a
controlled extension point: a tenant registers the extra attribute names it
wants (`tenant_attributes`), and those keys are captured from incoming payloads
into one JSONB column (`users.extended_attributes`) and merged back on reads.
Unregistered attributes are still dropped.

The design keeps the core honest and the addition additive:

- **Off by default.** With `SCIM_EXTENDED_ATTRIBUTES` unset or nothing
  registered, the registry is never consulted and a user serialises exactly as
  before: one JSONB column that is `NULL`.
- **Capture and PATCH consult the registry; plain reads don't.** A `GET` merges
  whatever is already stored, so it never depends on a registry lookup. Only
  writes (to know which body keys to keep) and the `/Schemas` document (to
  advertise them) query `tenant_attributes`.
- **Core attributes always win.** A captured value can never shadow a typed
  attribute: the merge skips any key the core resource already carries, and the
  registry refuses to register a core name in the first place.
- **It rides the existing guarantees.** The blob is part of the `store.User`
  the audit log serialises, so an extended attribute's before/after state is in
  the trail like everything else; a `PATCH` on a registered name is applied in
  the same full-replace transaction as the rest of the change.

The registered name is a top-level SCIM key, which covers the enterprise
extension as a whole object but not an individually-addressable sub-attribute
path, a documented v1 boundary.

## 📤 Change delivery

### 🔔 The outbox

The outbound event is written to a `webhook_deliveries` row inside the same
transaction as the change, so a committed change is always queued and a
rolled-back one leaves the queue as it was.

Sending from the handler after `COMMIT` would separate the two: the process can
die between them, leaving no way to tell afterwards whether the event went out.
The outbox keeps that decision in the database, where the change already is.

### 🔒 Claiming and leases

A dispatcher claims due rows in a single statement:

```sql
UPDATE webhook_deliveries SET
    attempts = attempts + 1,
    next_attempt_at = now() + make_interval(secs => $2)
WHERE id IN (
    SELECT id FROM webhook_deliveries
    WHERE status = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at, id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, tenant_id, event_type, target_id, payload, attempts, next_attempt_at
```

The statuses are query parameters in the code and appear as literals here.

`SKIP LOCKED` makes concurrent dispatchers take disjoint sets. The lease is
`next_attempt_at` pushed forward, rather than an open transaction, because
delivery is an HTTP call and holding a row lock across it would tie a database
connection to the receiver's latency.

Two properties worth keeping in mind when changing this:

- **The attempt is counted at claim time.** A delivery that kills its dispatcher
  mid-flight still spends an attempt, so it reaches the dead-letter queue rather
  than looping forever.
- **The lease spans the whole batch**: `Poll + Batch × Timeout`. A batch is
  delivered sequentially, so a lease sized for one send would let the rows queued
  behind the current attempt come due mid-batch. They would be delivered twice,
  and each row would spend its retry budget at double rate.

An interrupted dispatcher strands nothing: its rows stay `pending` and return
once the lease expires, with the attempt already counted.

### 🔁 Retry and dead-letter

Failures retry on a doubling backoff from 5s, capped at 5 minutes, with up to 20%
jitter so a batch that failed together returns spread out.

| Response | Outcome |
| --- | --- |
| `2xx` | Delivered |
| `5xx`, `408`, `429`, transport error | Retry until attempts run out |
| Other `4xx` | Dead-letter immediately: the receiver has given its verdict |
| `3xx` | Dead-letter: redirects stay unfollowed, so a signed payload of user attributes remains on its intended host |

A parked row keeps its payload and last error. Replay moves it back to
`pending` with a fresh budget, which `scimage-admin webhook replay <id>` (or
`webhook replay-all`) and the console's **Webhooks** page both do. The
console can replay one row or several selected at once, each call running the
same statement below independently. The console refuses to replay at all while
webhooks are disabled: with no dispatcher running, a requeued row would just
sit `pending` indefinitely. A Pending deliveries view lists currently-queued
rows (whether freshly replayed or mid-retry) alongside the parked ones, so a
replay's outcome stays visible instead of the row disappearing the moment it's
no longer dead-lettered.

```sql
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL
WHERE id = $1 AND status = 'dead_letter'
RETURNING tenant_id, event_type;
```

The returned tenant and event type feed the `webhook.replay` audit entry.

`attempts = 0` matters: leaving the old count would let the row retry once and
park again immediately. The `status = 'dead_letter'` guard keeps replay from
disturbing a delivered or in-flight row, and the requeue writes a
`webhook.replay` entry to `admin_audit_log` in the same transaction, so the
operator action is attributed like any other mutation.

The three outcome writes are each guarded on `status = 'pending'`, so `delivered`
and `dead_letter` are terminal: a dispatcher whose lease expired mid-send can
report late without overwriting an outcome another dispatcher already recorded.

`last_error` holds whatever the receiver sent back, so it is bounded in runes and
stripped of NUL and invalid UTF-8 before it reaches the column. A malformed
error body stays writable, and the delivery keeps moving.

### 🧹 Retention

Every mutation queues a row, so a delivered one would sit in the outbox forever
without a sweep. The dispatcher prunes delivered rows older than
`SCIM_WEBHOOK_RETENTION_DAYS` (default 30) on a slow, hourly cadence, separate
from the poll. Setting the value to
`0` disables the sweep and keeps delivered rows indefinitely.

Only delivered rows are touched. A pending row is still in flight, and a
dead-lettered one is kept for a human to inspect and replay. The sweep is deployment-wide
rather than tenant-scoped: one dispatcher runs it for the whole server, and a
partial index on `(delivered_at) WHERE status = 'delivered'` keeps the periodic
`DELETE` a range scan rather than a growing full-table scan. A delivered row is
an operational receipt, not the audit trail. `audit_log` is the authoritative,
untouched record of every change.

### 🔑 Signing

A delivery on the wire:

```http
POST /scim-events HTTP/1.1
X-SCIMage-Event: user.deactivated
X-SCIMage-Delivery-Id: 4172
X-SCIMage-Signature: t=1772357400,v1=9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
Content-Type: application/json

{"type":"user.deactivated","occurredAt":"2026-03-01T09:30:00Z","userId":"…","before":{…},"after":{…}}
```

The signature header carries the scheme version, the timestamp and the MAC:

```text
X-SCIMage-Signature: t=<unix seconds>,v1=<hex hmac-sha256>
```

The signed material is newline-separated, body last:

```text
<timestamp> \n <delivery id> \n <event type> \n <body>
```

The timestamp, delivery id and event type are all decimal or lowercase
identifiers, so no field can contain the separator and no boundary is ambiguous.
Only the body is arbitrary, and nothing follows it.

Each field is covered because a receiver is told to act on it:

- **Timestamp**: lets a receiver reject stale requests, knowing a capture
  cannot be re-stamped without the secret.
- **Delivery id**: the deduplication key. An unauthenticated key would let a
  capture replay inside the freshness window under a fresh id.
- **Event type**: receivers may route on the header without parsing the body.

`webhook.Verify` is the receiving half, exported for Go consumers. It compares
with `hmac.Equal`, which is constant-time.

### 🧾 Event semantics

Events name what happened to the resource rather than which endpoint was called:

| Event | Emitted when |
| --- | --- |
| `user.created` | A user is created |
| `user.deactivated` | A replace takes a user from active to inactive, or a `DELETE` arrives |
| `user.replaced` | Any other change, including reactivation |
| `group.created` | A group is created |
| `group.replaced` | A group's attributes or its membership are replaced, including through a membership `PATCH` |
| `group.deleted` | A group is deleted |

Identity providers deprovision with `PATCH active:false` far more often than with
`DELETE`, and that `PATCH` is applied through the same full-replace path as a
`PUT`. Classifying a replace by the active transition is what lets a consumer
subscribe to deactivations and see every real deprovisioning. Groups have no
`active` attribute to classify by, so a membership `PATCH` (the shape an IdP
actually uses to push group membership) reports the same `group.replaced` a
`PUT` would, rather than a separate membership-change event.

`DELETE` means "deactivate whatever the current state", so it reports
`user.deactivated` even for a user who was already inactive. A replace in that
same situation reports `user.replaced`, since it changed something other than
`active`. Both are no-op changes that a receiver applying idempotently absorbs.

Both images travel with the event, so a receiver reconciling its own copy sees
the transition itself. Delivery is at-least-once and retries are independent, so
events for one user can arrive in any order. `occurredAt` carries the database
clock, and the intended pattern is to apply idempotently and prefer the newest
`occurredAt` per user.

## 🛂 Admin console

The admin console (`internal/console`) is a web UI for the same tasks as
`scimage-admin`: tenants, tokens, extended attributes, the webhook queue, both
audit logs and ARIA. It is opt-in and runs on its own `http.Server`, started
only when `CONSOLE_ADDR` is set. The recommended value binds loopback, so the
privileged surface never shares a socket with tenant traffic. Setup and options
are in [Configuration](CONFIGURATION.md#-admin-console).

**Middleware order.** Each layer runs before the next:

```text
Security headers     X-Frame-Options, CSP (frame-ancestors 'none'), nosniff,
                     Referrer-Policy: same-origin, Cache-Control: no-store
   ↓
Host allowlist       any Host outside the list gets 421, before any credential
                     is read
   ↓
Static assets        /console/static/ is served here, ahead of auth, so the
                     login dialog's page can load its own styles
   ↓
Console auth         every other route needs a console token (HTTP Basic
                     password or Bearer header), compared in constant time
   ↓
Router               GET pages, then POST mutations
   ↓
Handler              each POST checks origin, caps the body at 64 KiB, then
                     verifies the form token before touching the store
```

**Routes.** Read pages: home, tenants, tokens, attributes, webhooks (plus a
delivery-status poll), audit, admin audit and ARIA. `POST` routes: create a
tenant, issue and revoke a token, register and unregister an attribute, replay a
dead-lettered delivery, and generate an ARIA narration.

**Credential.** Console tokens live in `console_tokens`, with no `tenant_id`,
because the console spans every tenant. They are issued, listed and revoked only
through `scimage-admin console-token`. No console page creates one, so a
console session can't mint a credential for itself.

**CSRF.** Browsers re-send Basic credentials on any request to the console, so a
valid login doesn't show that the operator meant a POST. Two checks cover that.
The origin check accepts a request whose `Sec-Fetch-Site` is `same-origin`, or
whose `Origin` matches the request's host. It accepts a request with neither
header, which is a non-browser client. The form token is an HMAC over a time
bucket and the operator's console key id, keyed with a random value generated at
startup. A token works only for the operator it was rendered for, for between
one and two hours (the current time bucket and the previous one), and does not
survive a restart. The check runs on headers first, so a cross-site POST is
refused before its body is parsed.

**Listener limits.** Both listeners set `ReadHeaderTimeout`, `ReadTimeout` and
`IdleTimeout`, and cap request headers at 64 KiB (`MaxHeaderBytes`). Only the
SCIM listener sets a `WriteTimeout`: the console's ARIA page can wait up to a
minute on an LLM call.

Every console change to tenants, tokens, attributes and the webhook queue calls
the same audited `store.*` function as the CLI, so each writes its
`admin_audit_log` entry in the change's own transaction.
[THREAT-MODEL.md](THREAT-MODEL.md) covers the trust boundary.

## 🤖 Audit review (ARIA)

`cmd/aria` (ARIA, the Audit Risk Intelligence Advisor) is the one AI-assisted
component, and it is deliberately a leaf. It reads `audit_log` through
`store.ListAuditEntriesSince` over a time window, optionally across every
tenant, and computes the signals in Go: deactivations clustered in a short
window, changes landing off-hours, per-caller volume, and denial bursts. The
thresholds live as constants in `internal/aria`, so what counts as notable
stays auditable code.

Only then does an LLM take over, and only to narrate: it receives the
already-computed facts and returns prose. The endpoint is provider-neutral, any
OpenAI-compatible chat-completions API set with `ARIA_LLM_*`, which keeps
`go.mod` at `pgx` and `golang.org/x/time`. It reads the key
from the environment and sends it only in the request's `Authorization` header,
and a window with no findings prints a deterministic line and skips the model,
so a clean review runs without a key.

ARIA prints its briefing for a human, and the
code routes it only there, clear of the store and the auth path. The human
decides, and the model only advises on activity. Wiring an LLM into a
provisioning or authorization decision would break that guarantee, so the
`code-reviewer` gate treats it as a critical finding.

## 📜 Logging

Operational logs are structured JSON with RFC 3339 timestamps, written to stdout
and to a dated file under `LOG_DIR`. One file per day keeps each file bounded for
a long-running process; in a container, set `LOG_DIR=` empty and let the runtime
collect stdout.

`SCIM_LOG_REQUESTS=1` adds request bodies to the request log for diagnosing a
client's behaviour. Bodies are logged only for authenticated requests. Secret-like
attribute values (a password, for example) are replaced with `[redacted]`, and
each logged body is cut to 2 KiB. A body that isn't valid JSON, or is over 64 KiB,
is logged by size only, since redaction can't see inside it. Because the entries
can still carry user attributes, the log directory is created with mode `0700`
and each log file with `0600`. See [Configuration](CONFIGURATION.md#-logging).

These are operational logs. The audit trail is separate and lives in Postgres, so
a change and its record share a commit.

## 🛑 Shutdown

`SIGINT`/`SIGTERM` drains the HTTP listener first, then stops the dispatcher, so
a request still in flight can queue its event. Anything left queued, or abandoned
mid-send, keeps its row and goes out when the server returns: its lease
expires. That is what the outbox is for: shutdown never waits on a receiver.
