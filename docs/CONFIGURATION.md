# 🔧 Configuration

Every setting comes from an environment variable. Copy `.env.example` to `.env`
and fill it in; `.env` is gitignored, and `make` targets load it for you.

Generate secrets with `openssl rand -hex 32`. The server requires at least 16
characters for `SCIM_WEBHOOK_SECRET`.

<!-- toc -->
## 📑 Contents

- [Core](#-core)
- [Rate limiting](#-rate-limiting)
- [Change delivery](#-change-delivery)
- [Logging](#-logging)
- [Extensible attributes](#-extensible-attributes)
- [Database](#-database)
- [Tenants and tokens](#-tenants-and-tokens)
- [Admin console](#-admin-console)
  - [Allowed hosts and request limits](#-allowed-hosts-and-request-limits)
- [Console UI or CLI](#-console-ui-or-cli)
- [Audit review (ARIA)](#-audit-review-aria)

<!-- /toc -->

## 🧱 Core

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Postgres connection string. When absent, it is assembled from `POSTGRES_USER`, `POSTGRES_PASSWORD` and `POSTGRES_DB` (all three required, `POSTGRES_PORT` optional), pointing at `localhost` with `sslmode=disable`. |
| `DATABASE_MAX_CONNS` | Pool size ceiling. Unset leaves pgx's own default, the larger of 4 and the CPU count. |
| `DATABASE_MAX_CONN_LIFETIME` | Go duration (e.g. `30m`) after which a pooled connection is recycled. Unset leaves pgx's own default (1h). |
| `SCIM_ADDR` | Listen address. Defaults to `:8080`. |
| `SCIM_BASE_URL` | External base URL, used for `Location`, `meta.location`, and the SCIM base URL that `scimage-admin tenant create` and the console's Tenants page show, behind a proxy. |
| `SCIM_REQUEST_TIMEOUT` | Go duration bounding one request's database work, auth's token lookup included. Defaults to `10s`; a non-positive value disables it. |

`SCIM_BASE_URL` matters behind a TLS-terminating proxy: the request arrives as
plain HTTP there, so links derived from the `Host` header would advertise `http`.
`DATABASE_MAX_CONNS` and `SCIM_REQUEST_TIMEOUT` exist so a flood of requests
(authenticated or not, since the timeout wraps token verification too) costs a
bounded amount of database work instead of an unbounded one; see
[THREAT-MODEL.md](THREAT-MODEL.md) for the residual pre-auth-volume risk they
narrow but don't close. `SCIM_REQUEST_TIMEOUT` is capped in practice by the
server's own 30s write timeout: setting it above 30s has no effect, since the
HTTP response gets cut off first.

## 🚦 Rate limiting

| Variable | Purpose |
| --- | --- |
| `SCIM_RATE_LIMIT` | Sustained requests per second per caller. Defaults to 20. |
| `SCIM_RATE_BURST` | Burst allowance. Defaults to 40. |

Sized for provisioning traffic: an identity provider pushing a bulk import sends
bursts, then goes quiet for hours. Set `SCIM_RATE_LIMIT=0` to opt out.

## 📤 Change delivery

| Variable | Purpose |
| --- | --- |
| `SCIM_WEBHOOK_URL` | Endpoint for change events. Set it to turn change delivery on. |
| `SCIM_WEBHOOK_SECRET` | HMAC signing secret. Required alongside a webhook URL. |
| `SCIM_WEBHOOK_ALLOW_HTTP` | Set to `1` to allow a plaintext endpoint. |
| `SCIM_WEBHOOK_MAX_ATTEMPTS` | Attempts before a delivery is dead-lettered. Defaults to 6. |
| `SCIM_WEBHOOK_RETENTION_DAYS` | Days a delivered row is kept before the dispatcher prunes it. Defaults to 30; `0` keeps them forever. Pending and dead-lettered rows are never swept. |

Leaving `SCIM_WEBHOOK_URL` unset keeps change delivery off: the store skips
queueing events.

A URL configured with no secret is a startup error, so every event that goes out
is signed. `SCIM_WEBHOOK_ALLOW_HTTP=1` is meant for a local receiver during
development: events carry user attributes, and the signature protects
authenticity rather than confidentiality.

Six attempts on the default backoff (5s, doubling, with up to 20% jitter, capped
at 5m) span roughly three minutes of receiver downtime. See [ARCHITECTURE.md](ARCHITECTURE.md#-retry-and-dead-letter)
for what retries and what parks immediately.

A parked (dead-lettered) delivery keeps its payload so it can be replayed once
the receiver is fixed. Replay flips it back onto the queue with a fresh retry
budget (the signature is re-computed at send time, so an old event still arrives
inside the receiver's freshness window):

```bash
scimage-admin webhook replay <deliveryID>   # one parked delivery
scimage-admin webhook replay-all            # every parked delivery
```

The console's **Webhooks** page shows the same delivery health (pending,
delivered, parked) with a per-row **Replay** button. Either path records the
replay in `admin_audit_log`, attributed to the operator, in the same
transaction as the requeue.

## 📜 Logging

| Variable | Purpose |
| --- | --- |
| `LOG_DIR` | Directory for dated log files. Defaults to `logs/`; set it empty for stdout only. |
| `LOG_LEVEL` | `debug`, `info`, `warn` or `error`. Defaults to `info`. |
| `SCIM_LOG_REQUESTS` | Set to `1` to record request bodies. Off by default. |

`SCIM_LOG_REQUESTS=1` records the body, query string and user agent of each
authenticated request, which is how a client's actual behaviour gets diagnosed.
Attribute names are compared ignoring case and punctuation, and values of
attributes whose names contain `password`, `passwd`, `pwd`, `passphrase`,
`secret`, `token`, `apikey`, `privatekey` or `credential` are replaced with
`[redacted]`, as is every value in a PATCH operation that targets one or whose
`path` isn't a string. A body that isn't valid JSON or is over 64 KiB is
recorded by size only, a body over the request limit is recorded as
`body_over_limit` with no content, and each logged body is cut at 2 KB. Query
strings are recorded as sent, without redaction. A request that fails
authentication gets its method, path and status only, with the method cut at 16
bytes and the path at 256 bytes. The `Authorization` header is never recorded.
Entries carry user attributes, so
treat the logs as sensitive: the directory is created `0700` and files `0600`.
In a container, set `LOG_DIR=` empty and let the runtime collect stdout.

## 🧬 Extensible attributes

| Variable | Purpose |
| --- | --- |
| `SCIM_EXTENDED_ATTRIBUTES` | Set to `1` to capture and return attributes a tenant has registered. Off by default. |

By design this server models a minimal, honest set of User attributes as typed
columns. When an identity provider needs to sync more, whether a known SCIM
attribute this server doesn't model (`displayName`, `title`, `phoneNumbers`, the
enterprise extension, …) or a fully custom field, an operator can register it
per tenant, and the server captures it into a single JSONB column and returns
it. Registered attributes are advertised in that
tenant's `/Schemas` document, so an IdP admin can discover and map to them.

```bash
scimage-admin attribute register -tenant <tenantID> -name displayName [-type string]
scimage-admin attribute register -tenant <tenantID> -name "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"
scimage-admin attribute list -tenant <tenantID>
scimage-admin attribute unregister -tenant <tenantID> -name displayName
```

A registered name is a **top-level** key of the SCIM resource. That covers
`displayName`, `phoneNumbers`, `addresses`, custom fields, and the enterprise
extension as its whole `urn:…:enterprise:2.0:User` object. A path into an
extension (e.g. patching `urn:…:department` on its own) isn't individually
addressable. Register the URN and replace the whole object, or map the IdP to
a top-level custom attribute. Core attributes (`userName`, `emails`, `active`,
…) can't be registered; they're already modelled, and a captured value is never
allowed to shadow one. Unregistering stops future capture and advertising; it
doesn't touch values already stored.

## 💾 Database

Used when `DATABASE_URL` is absent, and by `docker-compose.yml`.

| Variable | Purpose |
| --- | --- |
| `POSTGRES_USER` | Database user. |
| `POSTGRES_PASSWORD` | Database password. |
| `POSTGRES_DB` | Database name. |
| `POSTGRES_PORT` | Host port the Compose Postgres publishes on `127.0.0.1`. Defaults to 5432. |

## 🔑 Tenants and tokens

There is no bearer-token environment variable: authentication is issued,
tenant by tenant, through `cmd/scimage-admin`, which connects to Postgres
directly. The privileged surface for creating a
tenant or minting a credential is never a network endpoint.

Build the CLI once with `go install ./cmd/scimage-admin` to put `scimage-admin`
on your PATH, or run it in place with `go run ./cmd/scimage-admin`. Locally, the
`make tenant`, `make token`, `make token-list`, `make token-revoke` and
`make audit-list` targets wrap the common commands.

```bash
scimage-admin tenant create -name "Acme Corp" [-created-by "who"]
scimage-admin tenant list
scimage-admin token issue -tenant <tenantID> -label "Okta prod" [-expires 90d] [-created-by "who"]
scimage-admin token list -tenant <tenantID>
scimage-admin token revoke <keyID>
scimage-admin audit list [-tenant <tenantID>]
```

A token is shown once, at `issue` time, and only its `sha256` hash is ever
stored. `label` is what a `token list` row is recognised by later, so name it
after what's calling ("Okta prod", "Entra staging"), not who ran the command:
`created_by` (see below) already answers who and when.

**Tenant names are unique, case-insensitively.** `tenant create` rejects a
name that only differs from an existing one by casing, the same reasoning
`userName` uniqueness already uses. If it comes back as taken, check
`tenant list` for the existing customer before assuming it's a new one.

**Every privileged action is attributed.** `-created-by` defaults to
`$USER` (`$USERNAME` on Windows) when omitted, so `tenant create` and
`token issue` record a real operator by default. Pass it explicitly for
automation, e.g. `-created-by "provisioning-automation"`, so the trail says
what ran the command. `scimage-admin audit list [-tenant
<tenantID>]` reads that trail back: every tenant created, every token issued
or revoked, who did it, and when.

**Rotation with overlap.** A tenant can hold several live tokens at once, so
rotation doesn't require downtime: issue a new token, update it in the
identity provider, confirm traffic has moved (`token list` shows
`last_used_at`), then `token revoke` the old one. Revoking is immediate and
irreversible: a new token has to be issued if one is needed again.

Treat every issued token as a privileged credential (it authorizes changes
to that tenant's directory), and revoke it as soon as exposure is suspected.

## 🌐 Admin console

The admin console is an optional web UI for whoever runs the deployment, with
essentially the same reach as `scimage-admin`: a landing page, view and mutate
tenants (with each tenant's SCIM base URL), tokens and attributes, watch webhook
delivery health and replay a parked event, and read the SCIM audit log and the
admin audit log. When `ARIA_LLM_*` is configured, it also shows ARIA's report
with an on-demand ARIA Briefing. It is for that one operator, not a
customer-facing self-service portal: a tenant's own IT staff never log in
here. The one thing it can't do is mint its own login credential (see below).

| Variable | Purpose |
| --- | --- |
| `CONSOLE_ADDR` | Listen address for the console. Unset (the default) means the console does not start. The recommended value, `127.0.0.1:8090`, binds loopback so it isn't reachable off-host. |
| `CONSOLE_ALLOWED_HOSTS` | Comma-separated host names the console answers to, beyond the defaults below, e.g. `console.example.com,10.0.0.5`. Each entry is a specific IP address or ASCII DNS name (no wildcards) with an optional `:port`, which is ignored; see [Allowed hosts and request limits](#-allowed-hosts-and-request-limits). With the console enabled, an invalid entry stops the server at startup. Unset adds nothing. |
| `SCIMAGE_ENV` | Cosmetic label shown in the console's sidebar badge, e.g. `prod` or `staging`. Defaults to `local`. |

The console is a **second listener**, separate from the internet-facing SCIM
port, opt-in (it starts only when `CONSOLE_ADDR` is set) and loopback-bound, so
reaching this full-mutation admin surface off-host takes a deliberate tunnel.

It authenticates with its own credential, separate from the tenant-scoped SCIM
tokens, issued through `scimage-admin`:

```bash
scimage-admin console-token issue -label "ops laptop" [-expires 90d] [-created-by "who"]
scimage-admin console-token list
scimage-admin console-token revoke <keyID>
```

Like a SCIM token, the console token is shown once at `issue` time, only its
`sha256` hash is stored, and its issue and revocation are recorded in
`admin_audit_log`. A console token is system-wide (it isn't scoped to a tenant),
so those audit rows carry no tenant. Open `http://<CONSOLE_ADDR>/console` and
supply the token as the HTTP Basic password (what a browser's login dialog
prompts for) or an `Authorization: Bearer` header. Every mutating action reuses
the same `scimage-admin` code paths, so it is audit-logged in the same
transaction as the change. A browser POST must come from a console page
(checked with `Sec-Fetch-Site`, or `Origin` against the request's host), and
clients that send neither header are checked by the token alone. Every POST
must carry a form token tied to the signed-in console key. A reverse proxy in
front of the console must forward the original `Host` header.

### 🔒 Allowed hosts and request limits

The console checks the `Host` header of every request, including static assets,
before it checks credentials. It answers only to these names:

- `localhost`, `127.0.0.1` and `::1`, always.
- The host of `CONSOLE_ADDR` when it is a name or a specific IP. A wildcard
  bind such as `0.0.0.0:8090` adds nothing.
- Each entry in `CONSOLE_ALLOWED_HOSTS`.

Any other `Host` gets `421 Misdirected Request`. The port is not compared. This
blocks DNS rebinding: a page that re-points its own domain at `127.0.0.1` still
sends that domain as `Host`, so it can't read the console.

The defaults cover a loopback bind, an SSH tunnel and the Docker
`-p 127.0.0.1:8090:8090` setup. If you reach the console through a reverse proxy
or by a LAN name or IP, list that name:

```bash
CONSOLE_ALLOWED_HOSTS=console.example.com,10.0.0.5
```

An entry is `host` or `host:port`. The host is one of:

- A specific IPv4 or IPv6 address. Wrap an IPv6 address in brackets when it carries a
  port, e.g. `[fd00::1]:8090`. Zone ids such as `%eth0` are rejected. Addresses
  match in canonical form, so `2001:DB8::0001` matches the `[2001:db8::1]` a
  browser sends.
- A DNS name made of ASCII letters, digits and hyphens. Write an
  internationalised name in its punycode form (`xn--...`). Wildcards such as
  `*.example.com` are not supported; list each name.

The port is optional, from 1 to 65535, and ignored when matching. Any other
entry stops the server at startup, when the console is enabled, with an error
naming the entry. That covers a scheme, path, `@`, `?`, `#`, a space, a bad
port, an empty host, an unspecified address such as `0.0.0.0` or `::` (browsers
never send it as a host) and a mistyped IP address such as `10.0.0`.

Two size limits apply as well:

- Console form posts are capped at 64 KiB. A larger body gets `413` with
  "request body is too large". The forms are under 1 KiB.
- Both listeners (SCIM and console) cap request headers at 64 KiB, down from
  Go's 1 MiB default. Larger headers get `431` from `net/http`.

## 🔀 Console UI or CLI

Every administrative task can be done two ways: click through the admin console,
or run `scimage-admin` (locally, the `make` targets wrap it). They are
interchangeable, because the console calls the exact same `store.*` functions
the CLI does, so either path writes the same `admin_audit_log` row in the same
transaction.

| Task | In the console UI | On the CLI |
| --- | --- | --- |
| Create a tenant | **Tenants → New tenant** | `scimage-admin tenant create -name "Acme Corp"` |
| List tenants | **Tenants** | `scimage-admin tenant list` |
| Issue a token | **Tokens → Issue token** | `scimage-admin token issue -tenant <id> -label "Okta prod"` |
| List / revoke tokens | **Tokens** (Revoke) | `scimage-admin token list -tenant <id>`; `token revoke <keyID>` |
| Register / remove an attribute | **Attributes** | `scimage-admin attribute register\|unregister -tenant <id> -name <attr>` |
| Read the SCIM audit log | **Audit Log** | direct SQL on `audit_log`, or a webhook consumer |
| Read the admin audit log | **Admin Log** | `scimage-admin audit list [-tenant <id>]` |
| Watch webhook delivery / replay a parked event | **Webhooks** (Replay) | `scimage-admin webhook replay <id>` or `replay-all` |
| ARIA activity briefing | **ARIA** (Generate briefing), shown only once `ARIA_LLM_*` is set | `aria [-tenant <id>] [-since 7d]` (or `make aria`) |
| Issue / revoke a **console** credential | not in the UI | `scimage-admin console-token issue\|list\|revoke` |

Two things sit outside this symmetry, by design:

- **A console credential is CLI-only.** It's the key to the UI, so it's minted
  from the CLI (`console-token issue`); the console can't grant its own way in.
- **There is no "delete tenant."** A tenant owns a customer's whole directory,
  so it is created once and kept, never deleted, from either path. Rotate or
  revoke its tokens instead. Users and groups aren't created here either: an
  identity provider provisions those over SCIM, and the console shows their
  audit trail rather than an editor.

## 🤖 Audit review (ARIA)

`cmd/aria` (ARIA, the Audit Risk Intelligence Advisor) reads the `audit_log` and
prints a plain-English briefing of activity worth a human's attention. It
computes the signals in Go (clustered deactivations, off-hours changes,
per-caller volume, and denial bursts), then asks an LLM to narrate those
already-computed facts. It stays advisory: the code routes its output only to
the human, clear of the store and the auth path.

| Variable | Purpose |
| --- | --- |
| `ARIA_LLM_BASE_URL` | Base URL of an OpenAI-compatible chat-completions API (e.g. `https://api.anthropic.com/v1`). |
| `ARIA_LLM_API_KEY` | Provider key. Read from the environment and sent only in the request's `Authorization` header, and only when a window has findings. |
| `ARIA_LLM_MODEL` | Model id to request, e.g. `claude-sonnet-4-5`. |
| `ARIA_TIMEZONE` | Optional. IANA zone for the off-hours check. Defaults to the host's local zone. |

ARIA works with any OpenAI-compatible chat-completions API: Anthropic's
compatibility endpoint, OpenAI, OpenRouter, a local Ollama or vLLM, and so on.
Set the three `ARIA_LLM_*` variables to whichever you run.

The `cmd/aria` CLI and the console's **ARIA** page produce the same briefing
from the same signals. The CLI reads `ARIA_LLM_*` in its own process; the
console's ARIA nav entry only appears once the three variables are set in
the *server's* environment too, and narrates on demand when you click
**Generate briefing** (it caches the last briefing per tenant and window to
avoid a call on every click). If the LLM call itself fails, the console shows
a generic "LLM is currently unavailable" message rather than the underlying
error, which is logged server-side instead. The narration stays advisory
either way: it is only ever shown to the operator, never fed back into the
store or the auth path.

```bash
aria [-tenant <tenantID>] [-since 24h] [-timezone America/Vancouver]
```

`-tenant` scopes the review to one customer; omit it for a deployment-wide pass
across every tenant. `-since` accepts a bare day count (`7d`) or any Go duration
(`24h`, `90m`), and defaults to `24h`. The console page offers 1h, 24h, 7d
and 30d, and falls back to 24h for any other value.

ARIA reads the newest 200 entries in a window. When a window holds more, the
console shows a "Partial review" notice, and the CLI prints a note in its
header above the briefing, on a quiet window too. A shorter window may bring
the count within 200, and so may choosing one tenant. A busy window can still
exceed 200 at the shortest setting.

A window with nothing notable prints a deterministic "nothing tripped the
thresholds" line and skips the model, so a quiet review runs without a key. The
thresholds live in `internal/aria` as constants (for example, five
deactivations in ten minutes), so what counts as a signal stays auditable code.
