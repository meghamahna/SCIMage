# Architecture

How SCIMage is put together, and why. The [README](../README.md) covers what it
does; this covers the mechanics behind it.

## Request path

```text
Identity provider
   ↓  SCIM request to /scim/v2/{tenantID}/..., Bearer token
Auth middleware      looks up the token by key id, checks revoked/expired,
                     constant-time compares the secret, confirms the token's
                     own tenant matches {tenantID}, applied by Routes() itself
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

Authentication is applied by `Routes()` rather than per handler, so it covers the
whole surface structurally, including paths that resolve to nothing, which are
rejected before routing so responses stay uniform for an unauthenticated caller.
It reads `{tenantID}` straight out of the URL path rather than through the
router's own path-value matching: `requireToken` wraps the mux and runs before
the mux has matched anything, which is the only point `net/http`'s own
`r.PathValue` gets populated.

## Storage model

Nine tables, described in `/migrations`:

| Table | Holds |
| --- | --- |
| `tenants` | One row per customer organization. `id` is an app-generated, prefixed opaque string (`tenant_...`), never a renamable slug: it's pasted into the customer's IdP once and has to stay stable. `name` is unique, case-insensitively, so two customers can't silently share a display name |
| `scim_tokens` | Issued credentials: `sha256` of the secret half, never the secret; label, timestamps, `revoked_at`/`expires_at` |
| `users` | The provisioned directory, scoped by `tenant_id`. Deactivation is a soft delete, so history keeps a subject |
| `groups` | Groups, scoped by `tenant_id`. Unlike `users`, `DELETE` is a real deletion: the Group schema has no `active` attribute to soft-delete into |
| `group_members` | The membership join table: `(group_id, user_id)`, with every reference validated against the same tenant before it's inserted |
| `audit_log` | One row per mutating SCIM call, for either resource: tenant, resource type, actor, action, target, timestamp, before/after |
| `admin_audit_log` | One row per privileged CLI action: who created a tenant, issued or revoked a token, registered an attribute, and when |
| `tenant_attributes` | The per-tenant extensible-attribute registry: which extra attribute names to capture into `users.extended_attributes`, and the type to declare in `/Schemas` |
| `webhook_deliveries` | The outbound queue: tenant, payload, status, attempts, lease |

## Multi-tenancy

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
membership carries the same guarantee one level deeper — every member id is
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
overridden with `-created-by`, so the trail names a real operator rather than
always reading `scimage-admin`. A revoke that turns out to be a no-op (the
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
other — the design ARIA (Phase 12) reads from.

With `SCIM_WEBHOOK_URL` unset the store skips the outbox, so the queue stays
empty while nothing is draining it.

The before-image in an audit entry comes from a CTE reading the row in the same
statement as the `UPDATE`, since `OLD` in `RETURNING` arrived in Postgres 18 and
this runs on 16. Both halves see one snapshot, so there is no read-then-write gap
where a concurrent update could slip between them.

Refusals are recorded too (a duplicate `userName` or `displayName`, a missing
user or group, an invalid group member reference), which makes
a burst of denials visible to a reviewer.

## The UserStore and GroupStore interfaces

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

## Extensible attributes

The typed `users` columns are a deliberate, minimal set. Rather than grow them
to chase every attribute an identity provider might map, the server offers a
controlled extension point: a tenant registers the extra attribute names it
wants (`tenant_attributes`), and those keys are captured from incoming payloads
into one JSONB column (`users.extended_attributes`) and merged back on reads.
Unregistered attributes are still dropped.

The design keeps the core honest and the addition additive:

- **Off by default.** With `SCIM_EXTENDED_ATTRIBUTES` unset or nothing
  registered, the registry is never consulted and a user serialises exactly as
  before — one JSONB column that is simply `NULL`.
- **Capture and PATCH consult the registry; plain reads don't.** A `GET` merges
  whatever is already stored, so it never depends on a registry lookup. Only
  writes (to know which body keys to keep) and the `/Schemas` document (to
  advertise them) query `tenant_attributes`.
- **Core attributes always win.** A captured value can never shadow a typed
  attribute — the merge skips any key the core resource already carries, and the
  registry refuses to register a core name in the first place.
- **It rides the existing guarantees.** The blob is part of the `store.User`
  the audit log serialises, so an extended attribute's before/after state is in
  the trail like everything else; a `PATCH` on a registered name is applied in
  the same full-replace transaction as the rest of the change.

The registered name is a top-level SCIM key, which covers the enterprise
extension as a whole object but not an individually-addressable sub-attribute
path — a documented v1 boundary.

## Change delivery

### The outbox

The outbound event is written to a `webhook_deliveries` row inside the same
transaction as the change, so a committed change is always queued and a
rolled-back one leaves the queue as it was.

Sending from the handler after `COMMIT` would separate the two: the process can
die between them, leaving no way to tell afterwards whether the event went out.
The outbox keeps that decision in the database, where the change already is.

### Claiming and leases

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
RETURNING id, tenant_id, event_type, target_id, payload, attempts
```

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

### Retry and dead-letter

Failures retry on a doubling backoff from 5s, capped at 5 minutes, with up to 20%
jitter so a batch that failed together returns spread out.

| Response | Outcome |
| --- | --- |
| `2xx` | Delivered |
| `5xx`, `408`, `429`, transport error | Retry until attempts run out |
| Other `4xx` | Dead-letter immediately: the receiver has given its verdict |
| `3xx` | Dead-letter: redirects stay unfollowed, so a signed payload of user attributes remains on its intended host |

A parked row keeps its payload and last error. Replay is moving it back to
`pending` with a fresh budget:

```sql
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL
WHERE id = $1;
```

`attempts = 0` matters: leaving the old count would let the row retry once and
park again immediately. A `webhook replay` subcommand arrives with
`cmd/scimage-admin`.

The three outcome writes are each guarded on `status = 'pending'`, so `delivered`
and `dead_letter` are terminal: a dispatcher whose lease expired mid-send can
report late without overwriting an outcome another dispatcher already recorded.

`last_error` holds whatever the receiver sent back, so it is bounded in runes and
stripped of NUL and invalid UTF-8 before it reaches the column, so a malformed
error body stays writable, and the delivery keeps moving.

### Signing

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

### Event semantics

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
`active` attribute to classify by, so a membership `PATCH` — the shape an IdP
actually uses to push group membership — reports the same `group.replaced` a
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

## Logging

Operational logs are structured JSON with RFC 3339 timestamps, written to stdout
and to a dated file under `LOG_DIR`. One file per day keeps each file bounded for
a long-running process; in a container, set `LOG_DIR=` empty and let the runtime
collect stdout.

`SCIM_LOG_REQUESTS=1` adds full request bodies, which is how a client's actual
behaviour gets diagnosed. Those entries carry user attributes, so the directory
is created `0700`, files `0600`, and `logs/` is gitignored.

These are operational logs. The audit trail is separate and lives in Postgres, so
a change and its record share a commit.

## Shutdown

`SIGINT`/`SIGTERM` drains the HTTP listener first, then stops the dispatcher, so
a request still in flight can queue its event. Anything left queued, or abandoned
mid-send, keeps its row and goes out when the server returns: its lease simply
expires. That is what the outbox is for: shutdown never waits on a receiver.
