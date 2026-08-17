<div align="center">

<img src="docs/assets/scimage_banner.png" alt="SCIMage: SCIM Audit and Governance Engine" width="820">

### A SCIM 2.0 provisioning server with signed change delivery and an AI-advisory audit trail

![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/Postgres-16-4169E1?style=flat&logo=postgresql&logoColor=white)
![License: MIT](https://img.shields.io/badge/license-MIT-green?style=flat)
![Status](https://img.shields.io/badge/status-work%20in%20progress-yellow?style=flat)

</div>

SCIMage is a focused SCIM 2.0 server written in Go. It implements the core `/Users` and `/Groups` resources from [RFC 7644](https://datatracker.ietf.org/doc/html/rfc7644), backed by real Postgres, with security practices that are load-bearing and covered by tests.

## ✨ Why SCIMage

Most SCIM servers are a thin CRUD layer bolted onto an app. SCIMage treats the parts that actually matter once real customers are provisioning into you as first-class:

- **Security is structural:** tenant isolation, hashed and rotatable tokens, and an audit entry written in the *same transaction* as every change, enforced in code and tested against real Postgres.
- **A tamper-evident audit trail:** every mutation *and every refusal* is recorded with before/after state, so every change leaves a record.
- **Changes actually reach your app:** signed, retried webhooks with a dead-letter queue turn provisioning into events the rest of your system can act on.
- **Extend by config:** register any extra or custom attribute per tenant and it round-trips, keeping the core minimal and honest.
- **Self-hosted and transparent:** plain Go + Postgres + raw SQL, no framework and no lock-in; you own the data and the audit trail.

**Best fit:** a SaaS that needs to *receive* enterprise provisioning with a defensible security-and-audit story, wants to self-host, and values correctness over feature breadth. It's portfolio-grade; release engineering (packaging, published images, tagged releases) is still in progress.

## 💡 Why I built this

I spent years building JML (joiner mover leaver) automation on the identity provider side, configuring Okta Workflows to push user provisioning into 60+ SaaS applications. That gave me a solid understanding of the SCIM spec from the client's point of view.

This project is the other half: the server that receives those SCIM calls and applies them, which is the side most SaaS products build and maintain themselves. It demonstrates both ends of the handshake.

## ⚙️ What it does

SCIMage is the *service provider* side of SCIM: the endpoint an identity provider provisions into. One deployment serves many customer organizations at once: each is a **tenant**, addressed under its own `/scim/v2/{tenantID}` path with its own issued token, isolated from every other tenant's data.

| Method | Endpoint                          | Behaviour                                                                       |
|--------|-----------------------------------|----------------------------------------------------------------------------------|
| POST   | `/scim/v2/{tenantID}/Users`       | Creates a user. `201` with a `Location` header, `409` on a duplicate `userName` |
| GET    | `/scim/v2/{tenantID}/Users/{id}`  | Fetches one user                                                                |
| GET    | `/scim/v2/{tenantID}/Users`       | Lists users, paginated with `startIndex` and `count`                            |
| PUT    | `/scim/v2/{tenantID}/Users/{id}`  | Replaces a user's attributes                                                    |
| PATCH  | `/scim/v2/{tenantID}/Users/{id}`  | Applies operations to a user (how identity providers deprovision)               |
| DELETE | `/scim/v2/{tenantID}/Users/{id}`  | Deactivates a user, preserving the row and its history                          |
| POST   | `/scim/v2/{tenantID}/Groups`      | Creates a group. `201` with a `Location` header, `409` on a duplicate `displayName` |
| GET    | `/scim/v2/{tenantID}/Groups/{id}` | Fetches one group, with its members                                            |
| GET    | `/scim/v2/{tenantID}/Groups`      | Lists groups, paginated with `startIndex` and `count`                           |
| PUT    | `/scim/v2/{tenantID}/Groups/{id}` | Replaces a group's attributes and its whole membership set                     |
| PATCH  | `/scim/v2/{tenantID}/Groups/{id}` | Adds, removes or replaces members (how identity providers push group membership) |
| DELETE | `/scim/v2/{tenantID}/Groups/{id}` | Deletes a group. Unlike Users there is no `active` attribute to deactivate into, so this is a real deletion |

The discovery endpoints `/ServiceProviderConfig`, `/ResourceTypes` and `/Schemas` (same `/scim/v2/{tenantID}` prefix) declare exactly the attributes this server stores. A client reads them before provisioning.

Two unauthenticated operational probes sit outside the tenant path, for an orchestrator or load balancer: `GET /healthz` is process liveness (always `200` while the process serves, with no database dependency, so a transient DB blip never triggers a restart loop), and `GET /readyz` is readiness (`200` when Postgres is reachable, `503` when it isn't, so a failing instance is pulled from rotation).

`GET .../Users` supports `filter=userName eq "…"` and `filter=externalId eq "…"`; `GET .../Groups` supports `filter=displayName eq "…"` and `filter=externalId eq "…"`. These are the lookups a provider uses to decide whether a resource already exists. Other expressions answer `400` with `scimType: invalidFilter`, telling the client plainly where the supported set ends.

Responses use `application/scim+json`, and errors use the SCIM Error schema with the appropriate `scimType`.

`userName` uniqueness is enforced case-insensitively, matching the spec's `caseExact=false` characteristic, so `bjensen` and `BJensen` are one identity.

**Validated against [Microsoft's Entra ID SCIM validator](https://scimvalidator.microsoft.com/).** Core CRUD, filtering, and PATCH all pass. The `User` schema is deliberately reduced to a minimal set of typed columns. `displayName`, `title`, `preferredLanguage`, `name.formatted`/`name.middleName`, the enterprise extension and typed multi-valued emails aren't modeled that way. When a provider needs them, an operator registers the extra attributes per tenant (`scimage-admin attribute register`, gated by `SCIM_EXTENDED_ATTRIBUTES`) and the server captures and returns them through a JSONB pass-through, which keeps the core minimal while still round-tripping whatever Okta or Entra maps. See [Configuration](docs/CONFIGURATION.md#extensible-attributes).

## 🏗️ How it's built

```mermaid
flowchart LR
    IDP["Identity provider"] -->|"SCIM request"| AUTH["Auth"]
    AUTH -.->|"verify token"| DB[("Postgres")]
    AUTH --> RL["Rate limiter"]
    RL --> ROUTER{"Router"}
    ROUTER --> DISCOVERY["Discovery"]
    ROUTER --> MUTATE["Mutate"]
    ROUTER --> READ["Read"]
    MUTATE ==>|"one txn"| DB
    READ --> DB

    DB -.->|"due rows"| DISPATCH["Dispatcher"]
    DISPATCH -->|"signed POST"| APP["Your app"]
    DISPATCH -.->|"parked"| DB

    ADMIN["Admin CLI"] -.->|"off-network"| DB

    DB -.->|"audit_log"| ARIA["ARIA"]
    ARIA <-->|"narrate"| LLM["LLM"]
    ARIA -->|"briefing"| HUMAN["Reviewer"]
```

A mutation writes the user or group row, its audit entry and its outbound event in one transaction, so a change always carries its record and its notification. `audit_log` carries a `resource_type` column so one trail covers both resources. Every query in that path is scoped by `tenant_id`, so one customer's token can never read or change another's data. The handler depends on `UserStore` and `GroupStore` interfaces, so an application with its own tables can supply an implementation and skip webhooks entirely.

Everything lives in **one Postgres database**, and both the `scimage-admin` CLI and ARIA reach it directly, off the network. ARIA is the one advisory branch: it *reads* `audit_log`, computes the signals in Go, and asks an LLM to narrate them. Its briefing goes to a human, and the code keeps it there, clear of the store and the auth path.

[Architecture](docs/ARCHITECTURE.md) covers the request path, the storage model and that interface's contract.

## 📤 Change delivery

Provisioning pays off once the change reaches the system that needs it. Every mutation queues a signed webhook, so a user created by an identity provider lands in your application directly.

The event is queued in the mutation's own transaction, so a committed change is always queued. Delivery is at-least-once with retries and a dead-letter queue, and requests are signed with HMAC-SHA256 over the timestamp, delivery id, event type and body. Events name what happened to the user: a person moving from active to inactive emits `user.deactivated` whether the provider sent `DELETE` or `PATCH active:false`.

Set `SCIM_WEBHOOK_URL` to turn it on. [Architecture](docs/ARCHITECTURE.md#change-delivery) covers the outbox, claim leases, retry rules, the signing scheme and the event payload; `webhook.Verify` is exported for Go receivers.

## 🔒 Security practices

These are load-bearing, and each one is covered by tests:

- **Issued, tenant-scoped tokens.** Each token is `scimage_<keyID>_<secret>`; only `sha256(secret)` is stored, and a lookup by key id is compared with `crypto/subtle.ConstantTimeCompare` against the stored hash. A token also has to name the right tenant in the URL. A valid token for one customer is a 401 against another's path.
- **Auth applied by the router itself.** `Routes()` wraps every path in the token check, so authentication covers the whole surface structurally, and rejects unregistered paths the same way as real ones.
- **Cross-tenant isolation is structural.** Every store query is scoped by `tenant_id`, including lookups by id, so a token from one tenant naming another tenant's real user id gets the same 404 as a made-up one.
- **Audit logging in the same transaction as the change.** Create, replace and deactivate each write an entry (actor, action, target user ID, timestamp, before/after state) inside the transaction that makes the change, so the entry and the change commit together. Refusals are recorded too, which makes a burst of denied deactivations visible to a reviewer.
- **Privileged CLI actions are audited too.** Creating a tenant, issuing a token and revoking one each write an `admin_audit_log` entry in the same transaction as the change, naming a real operator by default (`$USER`, overridable with `-created-by`). `scimage-admin audit list` reads it back.
- **Tenant names are unique, case-insensitively.** Each customer keeps a distinct display name; `tenant create` rejects an exact or case-variant duplicate the same way `userName` uniqueness does.
- **Schema validation before the database.** Every incoming SCIM payload is checked against the expected shape, with attribute lengths bounded, before it reaches a query.
- **Signed outbound webhooks.** Change events are signed with HMAC-SHA256 and verified with `hmac.Equal`. A configured endpoint requires a secret at startup, so every event that goes out is signed. Plaintext endpoints are opt-in for local receivers.
- **Rate limiting per caller.** A token bucket returns `429` with `Retry-After`, so a runaway sync loop stays bounded.
- **Secrets from the environment.** The webhook secret and database credentials are read from environment variables at runtime; bearer tokens are issued and stored in Postgres, so a leak is handled by revoking the token. Git hooks scan staged diffs for credential-shaped content, and CI runs `govulncheck` against dependencies.

Every setting comes from an environment variable; see [Configuration](docs/CONFIGURATION.md) for the full list.

Operational logs are structured JSON on stdout and in a dated file under `LOG_DIR`. The audit trail is separate and lives in the `audit_log` table, so a change and its record commit together.

## 🤖 ARIA, the advisory audit reviewer

`aria` reads the audit trail and prints a plain-English briefing a reviewer can read in under a minute: clustered deactivations, changes landing off-hours, callers spiking in volume or racking up denials.

The design is the point. **Deterministic Go computes every signal**, and the LLM only narrates the facts Go already found. What counts as a signal lives in `internal/aria` as constants (five deactivations inside ten minutes, activity outside business hours), so it stays auditable code. ARIA reads the audit log and prints a briefing; the human decides. Its output goes only to that human, and by design the code gives it no path into the store or the auth layer. ARIA advises on activity; the code decides.

ARIA works with **any OpenAI-compatible chat-completions endpoint** (Anthropic's compat endpoint, OpenAI, OpenRouter, a local Ollama or vLLM). Point it there with `ARIA_LLM_BASE_URL`, `ARIA_LLM_API_KEY` and `ARIA_LLM_MODEL`.

```bash
make aria                                  # last 24h, every tenant
make aria TENANT=tenant_9f2a... SINCE=7d   # one tenant, last week
```

A quiet window prints a deterministic "nothing tripped the thresholds" line and skips the model, so a clean review runs without a key. See [Configuration](docs/CONFIGURATION.md#audit-review-aria).

## 🚀 Getting started

```bash
git clone https://github.com/meghamahna/SCIMage.git
cd SCIMage

# copy the env template and fill in real values (.env is gitignored)
cp .env.example .env

# start Postgres and apply schema migrations
make up

# run the server
make run
```

The server starts on `:8080`. Migrations run through `golang-migrate`, and `make migrate` uses a host `migrate` binary when one is present and the official container otherwise. See [Local development](docs/LOCAL-DEVELOPMENT.md) for prerequisites and the full set of targets.

## 🐳 Deploy with Docker

The repo ships a `Dockerfile`, so you can build a small (about 20 MB) container and run it anywhere. There is no image to pull; you build it once.

```bash
docker build -t scimage .

docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@your-db:5432/scimage?sslmode=require" \
  -e SCIM_BASE_URL="https://scim.yourcompany.com" \
  scimage
```

The container runs the server only. It needs a Postgres you already operate, with the migrations applied. Apply them with `make migrate` against that database, or run the `golang-migrate` image over the files in [`migrations/`](migrations/). Point the server at the database with `DATABASE_URL`.

Run it behind a proxy that terminates TLS, and set `SCIM_BASE_URL` to the public HTTPS URL so the links the server returns stay `https`. For an orchestrator, `GET /healthz` is the liveness check and `GET /readyz` is the readiness check. Every setting is an environment variable; see [Configuration](docs/CONFIGURATION.md).

## 🏢 Creating a tenant

There's no `SCIM_TOKEN` to configure. A deployment starts with zero tenants and zero tokens, both issued through `cmd/scimage-admin`, which talks to Postgres directly, off the network:

```bash
make tenant NAME="Acme Corp"
# TENANT ID      tenant_9f2a1b3c...
# NAME           Acme Corp
# CREATED BY     megha
# SCIM BASE URL  <SCIM_BASE_URL>/scim/v2/tenant_9f2a1b3c...

make token TENANT=tenant_9f2a1b3c... LABEL="Okta prod"
# TOKEN ID    ...
# TENANT      tenant_9f2a1b3c...
# LABEL       Okta prod
# CREATED BY  megha
#
# Shown once, not stored anywhere. Save it now:
# scimage_...
```

`CREATED BY` defaults to `$USER`; pass `-created-by` (or run the underlying `go run ./cmd/scimage-admin ...` form directly) to attribute it to something else, e.g. automation. Every tenant created and every token issued or revoked is recorded in `admin_audit_log`; `go run ./cmd/scimage-admin audit list` reads it back.

That base URL and token are what get pasted into the customer's Okta or Entra app as its SCIM Base URL and Bearer token. `token list` / `token revoke` manage rotation from there; see [Configuration](docs/CONFIGURATION.md#tenants-and-tokens).

## 📬 Example request

```bash
# $TENANT_ID and $TOKEN are the values scimage-admin printed above.
curl -X POST "http://localhost:8080/scim/v2/$TENANT_ID/Users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/scim+json" \
  -d '{
    "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
    "userName": "jdoe",
    "name": {"givenName": "Jane", "familyName": "Doe"},
    "emails": [{"value": "jdoe@example.com", "primary": true}]
  }'
```

## ✅ Running tests

```bash
make up      # the integration tests use a real Postgres
make test
```

Store and audit tests run against a real Postgres instance via `docker-compose`, exercising the actual SQL and constraints. Handlers are driven through `httptest`, and the webhook dispatcher against a real `httptest` receiver. Every suite cleans up the rows it creates.

## 🗺️ Roadmap

Phases 1 through 12 are complete: schema, endpoints, auth, audit, hardening, identity-provider interoperability, change delivery, multi-tenancy with issued API tokens, the `/Groups` resource with membership and per-tenant extensible attributes, and ARIA, the advisory audit reviewer. Release-engineering work (packaging, published images, tagged releases) is ongoing.

[ROADMAP.md](ROADMAP.md) tracks what's deliberately left for later, and [CHANGELOG.md](CHANGELOG.md) records what's landed. The [implementation plan](docs/IMPLEMENTATION_PLAN.md) has the phase-by-phase detail, with the decisions and trade-offs recorded as they were made.

## 📚 Documentation

- [Architecture](docs/ARCHITECTURE.md): request path, storage model, change delivery internals
- [Configuration](docs/CONFIGURATION.md): every environment variable, and token rotation
- [Local development](docs/LOCAL-DEVELOPMENT.md): prerequisites and every `make` target
- [Connecting Okta](docs/OKTA.md) and [Entra ID](docs/MS-ENTRA.md): identity-provider setup guides
- [Threat model](docs/THREAT-MODEL.md): trust boundaries, threats and mitigations
- [Security policy](SECURITY.md), [contributing](CONTRIBUTING.md), [roadmap](ROADMAP.md), [changelog](CHANGELOG.md)
- [Implementation plan](docs/IMPLEMENTATION_PLAN.md): the phase-by-phase build, with decisions recorded

## 🧰 Tech

Go with the standard library `net/http`, Postgres 16 via `pgx` with raw SQL, and `golang-migrate` for schema migrations. Few layers between the code and what runs.

## 📄 License

SCIMage is released under the [MIT License](LICENSE). You are free to use, modify, and distribute it, including in commercial products. The one condition is attribution: keep the copyright line (`Copyright (c) 2026 Megha Mahna`) and the license notice in any copy or substantial portion. If you build on SCIMage, a link back to this repository is appreciated.
