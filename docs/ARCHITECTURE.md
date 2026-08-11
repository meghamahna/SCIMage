# Architecture

How SCIMage is put together, and why. The [README](../README.md) covers what it
does; this covers the mechanics behind it.

## Request path

```text
Identity provider
   ↓  SCIM request, Bearer token
Auth middleware      constant-time token check, applied by Routes() itself
   ↓
Rate limiter         token bucket, keyed per caller
   ↓
Router               net/http method patterns
   ↓
Handler              validates the payload, maps it to a row
   ↓
Store                raw SQL, one transaction per mutation
```

The path is deliberately plain. Standard library `net/http` and raw SQL keep the
behaviour visible in the code, and Postgres is the single source of truth.

Authentication is applied by `Routes()` rather than per handler, so it covers the
whole surface structurally — including paths that resolve to nothing, which are
rejected before routing so responses stay uniform for an unauthenticated caller.

## Storage model

Three tables, described in `/migrations`:

| Table | Holds |
|---|---|
| `users` | The provisioned directory. Deactivation is a soft delete, so history keeps a subject |
| `audit_log` | One row per mutating call: actor, action, target, timestamp, before/after |
| `webhook_deliveries` | The outbound queue: payload, status, attempts, lease |

**A mutation writes its user row and its audit entry in one transaction**, plus
its outbound event when change delivery is configured. They commit together, so a
change always carries its record and its notification. The store owns those
writes, which keeps a handler from being able to skip them.

With `SCIM_WEBHOOK_URL` unset the store skips the outbox, so the queue stays
empty while nothing is draining it.

The before-image in an audit entry comes from a CTE reading the row in the same
statement as the `UPDATE`, since `OLD` in `RETURNING` arrived in Postgres 18 and
this runs on 16. Both halves see one snapshot, so there is no read-then-write gap
where a concurrent update could slip between them.

Refusals are recorded too — a duplicate `userName`, a missing user — which makes
a burst of denials visible to a reviewer.

## The UserStore interface

The handler depends on `scim.UserStore` rather than on Postgres directly. The
bundled store is the default implementation, and an application with its own user
table can supply another.

An implementation carries two obligations the compiler leaves open:

1. **Write the audit entry in the change's own transaction.** This is why
   `AuditRecord` is a parameter on every mutating method rather than something
   the handler records afterwards.
2. **Return a non-nil result alongside a nil error.** The handler dereferences
   what it gets back directly.

The interface currently lives in `internal/scim`, so supplying an implementation
means forking rather than importing. Moving the domain types to an importable
package turns it into a published extension point.

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
RETURNING id, event_type, target_id, payload, attempts
```

`SKIP LOCKED` makes concurrent dispatchers take disjoint sets. The lease is
`next_attempt_at` pushed forward, rather than an open transaction, because
delivery is an HTTP call and holding a row lock across it would tie a database
connection to the receiver's latency.

Two properties worth keeping in mind when changing this:

- **The attempt is counted at claim time.** A delivery that kills its dispatcher
  mid-flight still spends an attempt, so it reaches the dead-letter queue rather
  than looping forever.
- **The lease spans the whole batch** — `Poll + Batch × Timeout`. A batch is
  delivered sequentially, so a lease sized for one send would let the rows queued
  behind the current attempt come due mid-batch. They would be delivered twice,
  and each row would spend its retry budget at double rate.

An interrupted dispatcher strands nothing: its rows stay `pending` and return
once the lease expires, with the attempt already counted.

### Retry and dead-letter

Failures retry on a doubling backoff from 5s, capped at 5 minutes, with up to 20%
jitter so a batch that failed together returns spread out.

| Response | Outcome |
|---|---|
| `2xx` | Delivered |
| `5xx`, `408`, `429`, transport error | Retry until attempts run out |
| Other `4xx` | Dead-letter immediately — the receiver has given its verdict |
| `3xx` | Dead-letter — redirects stay unfollowed, so a signed payload of user attributes remains on its intended host |

A parked row keeps its payload and last error. Replay is moving it back to
`pending` with a fresh budget:

```sql
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL
WHERE id = $1;
```

`attempts = 0` matters — leaving the old count would let the row retry once and
park again immediately. A `webhook replay` subcommand arrives with
`cmd/scimage-admin`.

The three outcome writes are each guarded on `status = 'pending'`, so `delivered`
and `dead_letter` are terminal: a dispatcher whose lease expired mid-send can
report late without overwriting an outcome another dispatcher already recorded.

`last_error` holds whatever the receiver sent back, so it is bounded in runes and
stripped of NUL and invalid UTF-8 before it reaches the column — a malformed
error body stays writable, and the delivery keeps moving.

### Signing

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

- **Timestamp** — so a receiver can reject stale requests, knowing a capture
  cannot be re-stamped without the secret.
- **Delivery id** — the deduplication key. An unauthenticated key would let a
  capture replay inside the freshness window under a fresh id.
- **Event type** — receivers may route on the header without parsing the body.

`webhook.Verify` is the receiving half, exported for Go consumers. It compares
with `hmac.Equal`, which is constant-time.

### Event semantics

Events name what happened to the user rather than which endpoint was called:

| Event | Emitted when |
|---|---|
| `user.created` | A user is created |
| `user.deactivated` | A replace takes a user from active to inactive, or a `DELETE` arrives |
| `user.replaced` | Any other change, including reactivation |

Identity providers deprovision with `PATCH active:false` far more often than with
`DELETE`, and that `PATCH` is applied through the same full-replace path as a
`PUT`. Classifying a replace by the active transition is what lets a consumer
subscribe to deactivations and see every real deprovisioning.

`DELETE` means "deactivate whatever the current state", so it reports
`user.deactivated` even for a user who was already inactive. A replace in that
same situation reports `user.replaced`, since it changed something other than
`active`. Both are no-op changes that a receiver applying idempotently absorbs.

Both images travel with the event, so a receiver reconciling its own copy sees
the transition itself. Delivery is at-least-once and retries are independent, so
events for one user can arrive in any order — `occurredAt` carries the database
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
mid-send, keeps its row and goes out when the server returns — its lease simply
expires. That is what the outbox is for: shutdown never waits on a receiver.
