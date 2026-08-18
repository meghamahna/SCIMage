<div align="center">

<img src="docs/assets/scimage_logo.png" alt="SCIMage: SCIM Audit and Governance Engine" width="200">

### A SCIM 2.0 provisioning server with signed change delivery and an AI-advisory audit trail

[![CI](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml/badge.svg)](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml)
[![govulncheck](https://img.shields.io/github/actions/workflow/status/meghamahna/SCIMage/ci.yml?label=govulncheck)](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/meghamahna/SCIMage?logo=go&logoColor=white)](go.mod)
![PostgreSQL](https://img.shields.io/badge/Postgres-16-4169E1?style=flat&logo=postgresql&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/meghamahna/SCIMage?sort=semver&logo=github)](https://github.com/meghamahna/SCIMage/releases)

</div>

SCIMage is a focused SCIM 2.0 server written in Go. It implements the core `/Users` and `/Groups` resources from [RFC 7644](https://datatracker.ietf.org/doc/html/rfc7644), backed by real Postgres, with security practices that are load-bearing and covered by tests.

- [Why SCIMage](#-why-scimage)
- [Why I built this](#-why-i-built-this)
- [What it does](#-what-it-does)
- [How it's built](#-how-its-built)
- [Change delivery](#-change-delivery)
- [Security practices](#-security-practices)
- [ARIA, the advisory audit reviewer](#-aria-the-advisory-audit-reviewer)
- [Getting started](#-getting-started): the ordered, start-to-finish path
  - [Quickstart via the UI](#-quickstart-via-the-ui)
  - [Quickstart via the CLI](#-quickstart-via-the-cli)
- [Deploy with Docker](#-deploy-with-docker)
- [Running tests](#-running-tests)
- [Roadmap](#-roadmap)
- [Documentation](#-documentation)
- [Tech](#-tech)
- [License](#-license)

## ✨ Why SCIMage

Most SCIM servers are a thin CRUD layer bolted onto an app. SCIMage treats the parts that actually matter once real customers are provisioning into you as first-class:

- **Security is structural:** tenant isolation, hashed and rotatable tokens, and an audit entry written in the *same transaction* as every change, enforced in code and tested against real Postgres.
- **A tamper-evident audit trail:** every mutation *and every refusal* is recorded with before/after state, so every change leaves a record.
- **Changes actually reach your app:** signed, retried webhooks with a dead-letter queue turn provisioning into events the rest of your system can act on.
- **Extend by config:** register any extra or custom attribute per tenant and it round-trips, keeping the core minimal and honest.
- **Self-hosted and transparent:** plain Go + Postgres + raw SQL, no framework and no lock-in; you own the data and the audit trail.

**Best fit:** a SaaS that needs to *receive* enterprise provisioning with a defensible security-and-audit story, wants to self-host, and values correctness over feature breadth. It's portfolio-grade; a published registry image and a tagged `v1.0.0` release are the remaining release steps.

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

An interactive API reference (Swagger UI) is served, unauthenticated, at `GET /docs`. An optional **ops console** (a loopback admin UI for whoever runs the deployment) is available at `/console` when `CONSOLE_ADDR` is set; both are covered in [Getting started](#-getting-started).

`GET .../Users` supports `filter=userName eq "…"` and `filter=externalId eq "…"`; `GET .../Groups` supports `filter=displayName eq "…"` and `filter=externalId eq "…"`. These are the lookups a provider uses to decide whether a resource already exists. Other expressions answer `400` with `scimType: invalidFilter`, telling the client plainly where the supported set ends.

Responses use `application/scim+json`, and errors use the SCIM Error schema with the appropriate `scimType`.

`userName` uniqueness is enforced case-insensitively, matching the spec's `caseExact=false` characteristic, so `bjensen` and `BJensen` are one identity.

**Validated against [Microsoft's Entra ID SCIM validator](https://scimvalidator.microsoft.com/).** Core CRUD, filtering, and PATCH all pass. The `User` schema is deliberately reduced to a minimal set of typed columns. `displayName`, `title`, `preferredLanguage`, `name.formatted`/`name.middleName`, the enterprise extension and typed multi-valued emails aren't modeled that way. When a provider needs them, an operator registers the extra attributes per tenant (`scimage-admin attribute register`, gated by `SCIM_EXTENDED_ATTRIBUTES`) and the server captures and returns them through a JSONB pass-through, which keeps the core minimal while still round-tripping whatever Okta or Entra maps. See [Configuration](docs/CONFIGURATION.md#-extensible-attributes).

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

Everything lives in **one Postgres database**. A mutation writes its row, audit entry and outbound event in one transaction, so a change always carries its record and its notification, and every query is scoped by `tenant_id`. ARIA is the one advisory branch, reading `audit_log` off to the side, clear of the store and the auth path.

[Architecture](docs/ARCHITECTURE.md) covers the request path, storage model, multi-tenancy and the `UserStore`/`GroupStore` interface contract in full.

## 📤 Change delivery

Provisioning pays off once the change reaches the system that needs it. Every mutation queues a signed webhook, at-least-once with retries and a dead-letter queue, so a user created by an identity provider lands in your application directly.

Set `SCIM_WEBHOOK_URL` to turn it on. [Architecture](docs/ARCHITECTURE.md#-change-delivery) covers the outbox, claim leases, retry rules, the signing scheme and the event payload; `webhook.Verify` is exported for Go receivers.

## 🔒 Security practices

These are load-bearing, and each one is covered by tests:

- **Issued, tenant-scoped tokens**, compared with `crypto/subtle.ConstantTimeCompare` against a stored hash.
- **Auth applied by the router itself**, so it covers the whole surface structurally.
- **Cross-tenant isolation is structural**, not just convention.
- **Audit logging in the same transaction as the change**, including refusals.
- **Privileged CLI actions are audited too**, naming a real operator.
- **Tenant names are unique, case-insensitively.**
- **Schema validation before the database**, with attribute lengths bounded.
- **Signed outbound webhooks**, HMAC-SHA256, verified with `hmac.Equal`.
- **Rate limiting per caller**, a token bucket returning `429`.
- **Secrets from the environment**, never hardcoded or logged.
- **Secret and dependency scanning in CI.** Git hooks block staged diffs that look like credentials; `govulncheck` runs against every dependency.

See [Threat model](docs/THREAT-MODEL.md) for the full threat-by-threat reasoning behind each of these, and [Configuration](docs/CONFIGURATION.md) for every environment variable.

Operational logs are structured JSON on stdout and in a dated file under `LOG_DIR`. The audit trail is separate and lives in the `audit_log` table, so a change and its record commit together.

## 🤖 ARIA, the advisory audit reviewer

`aria` reads the audit trail and prints a plain-English briefing a reviewer can read in under a minute: clustered deactivations, changes landing off-hours, callers spiking in volume or racking up denials.

The design is the point. **Deterministic Go computes every signal**, and the LLM only narrates the facts Go already found. What counts as a signal lives in `internal/aria` as constants (five deactivations inside ten minutes, activity outside business hours), so it stays auditable code. ARIA reads the audit log and prints a briefing; the human decides. Its output goes only to that human, and by design the code gives it no path into the store or the auth layer. ARIA advises on activity; the code decides.

ARIA works with **any OpenAI-compatible chat-completions endpoint** (Anthropic's compat endpoint, OpenAI, OpenRouter, a local Ollama or vLLM). Point it there with `ARIA_LLM_BASE_URL`, `ARIA_LLM_API_KEY` and `ARIA_LLM_MODEL`.

```bash
make aria                                  # last 24h, every tenant
make aria TENANT=tenant_9f2a... SINCE=7d   # one tenant, last week
```

A quiet window prints a deterministic "nothing tripped the thresholds" line and skips the model, so a clean review runs without a key. See [Configuration](docs/CONFIGURATION.md#-audit-review-aria).

## 🚀 Getting started

Two quick paths from a fresh clone, depending on whether you'd rather click or type. Both assume the [prerequisites](#step-by-step) below and that you're in the repo root. Every admin task works from either side; the full command map is in [Configuration → Console UI or CLI](docs/CONFIGURATION.md#-console-ui-or-cli).

### ⚡ Quickstart via the UI

Bring up the stack, mint a console credential, and start the server with the admin UI enabled. Once you're in the console you create tenants and tokens by clicking.

```bash
# clone, configure, and turn the console UI on
git clone https://github.com/meghamahna/SCIMage.git && cd SCIMage
cp .env.example .env
echo 'CONSOLE_ADDR=127.0.0.1:8090' >> .env

# start Postgres and apply migrations
make up

# mint a console credential (shown once: copy the scimage_console_... line)
make console-token LABEL="my laptop"

# start the server: SCIM API on :8080, console UI on :8090
make run
```

Then open `http://127.0.0.1:8090/console` and paste the token as the password (leave the username blank). That's the working UI. 🎉 The interactive API reference is live alongside it at `http://localhost:8080/docs`.

### ⚡ Quickstart via the CLI

Same result without the browser: a running SCIM API serving one tenant, provisioned and driven entirely from the shell.

```bash
# clone, configure, bring up Postgres + migrations
git clone https://github.com/meghamahna/SCIMage.git && cd SCIMage
cp .env.example .env
make up

# create a tenant, then issue it a token (both shown once)
make tenant NAME="Acme Corp"                    # note the printed TENANT ID
make token TENANT=<tenant-id> LABEL="Okta prod" # copy the scimage_... token

# start the server
make run
```

With the server up, POST your first user with `curl` (see [step 6](#step-by-step) below), and manage everything through `scimage-admin` or the `make` targets.

### Step by step

A fresh clone to a running server with one tenant provisioned, in order. Every step assumes you're in the repo root.

**Prerequisites:** Go (the version in [`go.mod`](go.mod)), Docker with Compose (for Postgres), GNU Make, and `jq`. [Local development](docs/LOCAL-DEVELOPMENT.md) has versions and platform notes.

**1. Clone and configure.**

```bash
git clone https://github.com/meghamahna/SCIMage.git
cd SCIMage
cp .env.example .env      # .env is gitignored; fill in real values
make hooks-install        # once per clone: enables the secret-scan / gofmt / vet / test pre-commit hook
```

**2. Start Postgres and apply migrations.**

```bash
make up
```

Migrations run through `golang-migrate`; `make migrate` uses a host `migrate` binary when one is present and the official container otherwise.

**3. Run the server.** The SCIM API listens on `:8080`.

```bash
make run
```

**4. Verify it's up.**

```bash
curl localhost:8080/healthz   # {"status":"ok"} (process is live)
curl localhost:8080/readyz    # 200 once Postgres is reachable
```

**5. Create a tenant and issue a token.** A deployment starts with zero of each; both are minted through `cmd/scimage-admin`, which talks to Postgres directly, off the network. There is no `SCIM_TOKEN` to set.

```bash
make tenant NAME="Acme Corp"
# TENANT ID      tenant_9f2a1b3c...
# SCIM BASE URL  <SCIM_BASE_URL>/scim/v2/tenant_9f2a1b3c...

make token TENANT=tenant_9f2a1b3c... LABEL="Okta prod"
# ...
# Shown once, not stored anywhere. Save it now:
# scimage_...
```

`CREATED BY` defaults to `$USER`. Every tenant created and every token issued or revoked is recorded in `admin_audit_log` (`make audit-list`). Rotation, expiry, and the full CLI are in [Configuration → Tenants and tokens](docs/CONFIGURATION.md#-tenants-and-tokens).

**6. Make a request** with the tenant id and token from step 5:

```bash
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

**7. Connect an identity provider.** In production, the base URL and token from step 5 are what you paste into the customer's Okta or Entra app as its SCIM Base URL and Bearer token: [Connecting Okta](docs/OKTA.md), [Entra ID](docs/MS-ENTRA.md).

**8. (Optional) Open the ops console.** A loopback admin UI with the same reach as `scimage-admin`: view and mutate tenants, tokens and attributes, and read the audit trails and ARIA's report.

```bash
echo 'CONSOLE_ADDR=127.0.0.1:8090' >> .env          # opt-in; loopback-bound
go run ./cmd/scimage-admin console-token issue -label "my laptop"
make run                                             # restart; console now on :8090
```

Open `http://127.0.0.1:8090/console` and supply the shown-once token as the HTTP Basic password (what a browser's login dialog prompts for) or a `Bearer` header. See [Configuration → Ops console](docs/CONFIGURATION.md#-ops-console).

**9. Browse the API reference.** Interactive Swagger UI, served from the SCIM server with no auth and no CDN, at `http://localhost:8080/docs`.

For prerequisites in depth and the full set of `make` targets, see [Local development](docs/LOCAL-DEVELOPMENT.md).

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

## ✅ Running tests

```bash
make up      # the integration tests use a real Postgres
make test
```

Store and audit tests run against a real Postgres instance via `docker-compose`, exercising the actual SQL and constraints. Handlers are driven through `httptest`, and the webhook dispatcher against a real `httptest` receiver. Every suite cleans up the rows it creates.

## 🗺️ Roadmap

Phases 1 through 14 are complete: schema, endpoints, auth, audit, hardening, identity-provider interoperability, change delivery, multi-tenancy with issued API tokens, the `/Groups` resource with membership and per-tenant extensible attributes, ARIA the advisory audit reviewer, release engineering, and the operator tooling: the opt-in ops console and the interactive OpenAPI/Swagger reference. A published registry image and a tagged `v1.0.0` release are the remaining steps.

[ROADMAP.md](ROADMAP.md) tracks what's deliberately left for later, and [CHANGELOG.md](CHANGELOG.md) records what's landed. The [implementation plan](docs/IMPLEMENTATION_PLAN.md) has the phase-by-phase detail, with the decisions and trade-offs recorded as they were made.

## 📚 Documentation

**Start here:** [Getting started](#-getting-started) is the ordered, start-to-finish path, from a clone through a provisioned tenant, the ops console, and the API reference. The rest of the docs are the deep-dives it links into, roughly in the order you'd reach for them:

- [Local development](docs/LOCAL-DEVELOPMENT.md): prerequisites, every `make` target, and a hands-on runbook
- [Configuration](docs/CONFIGURATION.md): the authoritative reference for every environment variable, plus the `scimage-admin` CLI (tenants, tokens, the ops console) and token rotation
- [Connecting Okta](docs/OKTA.md) and [Entra ID](docs/MS-ENTRA.md): identity-provider setup guides
- [Architecture](docs/ARCHITECTURE.md): request path, storage model, change delivery internals
- [Threat model](docs/THREAT-MODEL.md): trust boundaries, threats and mitigations
- [Implementation plan](docs/IMPLEMENTATION_PLAN.md): the phase-by-phase build, with decisions recorded
- [Security policy](SECURITY.md), [contributing](CONTRIBUTING.md), [roadmap](ROADMAP.md), [changelog](CHANGELOG.md)

## 🧰 Tech

Go with the standard library `net/http`, Postgres 16 via `pgx` with raw SQL, and `golang-migrate` for schema migrations. Few layers between the code and what runs.

## 📄 License

SCIMage is released under the [MIT License](LICENSE). You are free to use, modify, and distribute it, including in commercial products. The one condition is attribution: keep the copyright line (`Copyright (c) 2026 Megha Mahna`) and the license notice in any copy or substantial portion. If you build on SCIMage, a link back to this repository is appreciated.
