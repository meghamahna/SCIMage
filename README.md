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
- [The problem it solves](#-the-problem-it-solves)
- [What it does](#-what-it-does)
- [How it's built](#-how-its-built)
- [Change delivery](#-change-delivery)
- [Security practices](#-security-practices)
- [ARIA: The AI advisory audit reviewer](#-aria-the-ai-advisory-audit-reviewer)
- [Getting started](#-getting-started)
- [Deploy with Docker](#-deploy-with-docker)
- [Running tests](#-running-tests)
- [Roadmap](#-roadmap)
- [Documentation](#-documentation)
- [Tech](#-tech)
- [License](#-license)

## ✨ Why SCIMage

SCIMage is a SCIM 2.0 server for the receiving side of enterprise provisioning. Beyond the core endpoints, two choices shape it:

- **Extend by config:** register any extra or custom attribute per tenant and it round-trips, keeping the core minimal and honest.
- **Self-hosted and transparent:** plain Go + Postgres + raw SQL, no framework and no lock-in; you own the data and the audit trail.

**Best fit:** a SaaS that needs to *receive* enterprise provisioning with a defensible security-and-audit story, wants to self-host, and values correctness over feature breadth.

## 💡 The problem it solves

Enterprise buyers expect to run your SaaS from their own identity provider: a new hire gets access automatically, and a departing employee loses it the moment HR disables them in Okta or Entra. That handshake is SCIM, and the work lands on the *service provider* side, the application receiving the calls. That end is easy to underbuild: skip `/Groups`, keep no audit trail, share one token across customers, and a security review asking who deprovisioned an account has no answer.

SCIMage is that receiving end, built to hold up:

- **Win the deal without the build.** RFC 7644 `/Users` and `/Groups`, validated against Microsoft's Entra SCIM validator, so you can say yes to enterprise provisioning without writing and maintaining a spec-compliant server yourself.
- **Prove it in an audit.** Every change, and every refusal, is written to an audit trail in the same transaction as the change itself, so the record can never drift from what happened. That is the answer a SOC 2 or access review is looking for.
- **Keep customers apart.** Each tenant has its own path, its own issued and rotatable token, and data isolation enforced in code and tested, not left to convention.
- **Make provisioning reach your app.** Signed, retried webhooks turn each change into an event the rest of your system can act on, instead of a row that just sits in a table.

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

An interactive API reference (Swagger UI) is served, unauthenticated, at `GET /docs`. An optional **admin console** (a loopback admin UI for whoever runs the deployment) is available at `/console` when `CONSOLE_ADDR` is set; both are covered in [Getting started](#-getting-started).

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

Everything lives in **one Postgres database**, with every query scoped by `tenant_id` and each mutation writing its row, audit entry and outbound event in one transaction. ARIA is the one advisory branch, reading `audit_log` off to the side, clear of the store and the auth path.

[Architecture](docs/ARCHITECTURE.md) covers the request path, storage model, multi-tenancy and the `UserStore`/`GroupStore` interface contract in full.

## 📤 Change delivery

Every mutation queues a signed webhook, at-least-once with retries and a dead-letter queue, so a user created by an identity provider lands in your application directly.

Set `SCIM_WEBHOOK_URL` to turn it on. [Architecture](docs/ARCHITECTURE.md#-change-delivery) covers the outbox, claim leases, retry rules, the signing scheme and the event payload; `webhook.Verify` is exported for Go receivers.

## 🔒 Security practices

Each control is load-bearing and covered by tests: issued, tenant-scoped tokens compared with `crypto/subtle.ConstantTimeCompare` against a stored hash; auth applied by the router itself, so it covers the whole surface; cross-tenant isolation scoped on `tenant_id` in every query; audit logging in the same transaction as the change, refusals included; and privileged CLI actions audited to a named operator. Schema validation bounds attribute lengths before the database, outbound webhooks are signed with HMAC-SHA256 and verified with `hmac.Equal`, callers are rate-limited to a `429`, and secrets come from the environment, never hardcoded or logged. CI scans for both leaked credentials and vulnerable dependencies.

See [Threat model](docs/THREAT-MODEL.md) for the full threat-by-threat reasoning behind each of these, and [Configuration](docs/CONFIGURATION.md) for every environment variable.

Operational logs are structured JSON on stdout and in a dated file under `LOG_DIR`. The audit trail is separate and lives in the `audit_log` table, so a change and its record commit together.

## 🤖 ARIA: The AI advisory audit reviewer

`aria` (**A**udit **R**isk **I**ntelligence **A**dvisor) reads the audit trail and prints a plain-English briefing a reviewer can read in under a minute: clustered deactivations, changes landing off-hours, callers spiking in volume or racking up denials. The name is deliberate: it advises, it never decides.

Deterministic Go computes every signal (the thresholds live in `internal/aria` as constants: five deactivations inside ten minutes, activity outside business hours), the LLM only narrates them, and the code gives it no path into the store or the auth layer. ARIA advises; the human decides.

ARIA works with **any OpenAI-compatible chat-completions endpoint** (Anthropic's compat endpoint, OpenAI, OpenRouter, a local Ollama or vLLM). Point it there with `ARIA_LLM_BASE_URL`, `ARIA_LLM_API_KEY` and `ARIA_LLM_MODEL`.

```bash
make aria                                  # last 24h, every tenant
make aria TENANT=tenant_9f2a... SINCE=7d   # one tenant, last week
```

A quiet window prints a deterministic "nothing tripped the thresholds" line and skips the model, so a clean review runs without a key. See [Configuration](docs/CONFIGURATION.md#-audit-review-aria).

## 🚀 Getting started

**Prerequisites:** Go (the version in [`go.mod`](go.mod)), Docker with Compose (for Postgres), GNU Make, and `jq`. [Local development](docs/LOCAL-DEVELOPMENT.md) has versions and platform notes.

**1. Clone and configure.**

```bash
git clone https://github.com/meghamahna/SCIMage.git && cd SCIMage
cp .env.example .env      # .env is gitignored; fill in real values
make hooks-install        # once per clone: enables the secret-scan / gofmt / vet / test pre-commit hook
```

**2. Start Postgres and apply migrations.**

```bash
make up
```

**3. Provision a tenant and token, via the UI *or* the CLI.**

*Option A, the admin console (UI).* Enable the loopback admin UI, mint a sign-in credential, and start the server.

```bash
echo 'CONSOLE_ADDR=127.0.0.1:8090' >> .env   # opt-in; loopback-bound
make console-token LABEL="my laptop"          # sign-in credential, shown once
make run                                       # SCIM API :8080, console UI :8090
```

Open `http://127.0.0.1:8090/console`, sign in with the token as the password (leave the username blank), and create a tenant and its token by clicking. 🎉

*Option B, the CLI.* Create the tenant and token from the shell, then start the server.

```bash
make tenant NAME="Acme Corp"                     # prints the TENANT ID and SCIM base URL
make token TENANT=<tenant-id> LABEL="Okta prod"  # prints the token, shown once
make run                                          # SCIM API :8080
```

Either way, `CREATED BY` defaults to `$USER`, and every tenant and token action is recorded in `admin_audit_log` (`make audit-list`). The full CLI, rotation, and the console-vs-CLI command map are in [Configuration](docs/CONFIGURATION.md#-console-ui-or-cli).

**4. Make your first request** with the tenant id and token from step 3:

```bash
curl localhost:8080/healthz   # {"status":"ok"} once the server is up

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

**5. Connect an identity provider.** In production, the base URL and token from step 3 are what you paste into the customer's Okta or Entra app as its SCIM Base URL and Bearer token: [Connecting Okta](docs/OKTA.md), [Entra ID](docs/MS-ENTRA.md).

**6. Browse the API reference.** Interactive Swagger UI, served with no auth and no CDN, at `http://localhost:8080/docs`.

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

Phases 1 through 14 are complete. A published registry image and a tagged `v1.0.0` release are the remaining steps.

[ROADMAP.md](ROADMAP.md) tracks what's deliberately left for later, [CHANGELOG.md](CHANGELOG.md) records what's landed, and the [implementation plan](docs/IMPLEMENTATION_PLAN.md) has the phase-by-phase detail with the decisions recorded as they were made.

## 📚 Documentation

**Start here:** [Getting started](#-getting-started) takes you from a clone through a provisioned tenant, the admin console, and the API reference. The rest of the docs are the deep-dives it links into, roughly in the order you'd reach for them:

- [Local development](docs/LOCAL-DEVELOPMENT.md): prerequisites, every `make` target, and a hands-on runbook
- [Configuration](docs/CONFIGURATION.md): the authoritative reference for every environment variable, plus the `scimage-admin` CLI (tenants, tokens, the admin console) and token rotation
- [Connecting Okta](docs/OKTA.md) and [Entra ID](docs/MS-ENTRA.md): identity-provider setup guides
- [Architecture](docs/ARCHITECTURE.md): request path, storage model, change delivery internals
- [Threat model](docs/THREAT-MODEL.md): trust boundaries, threats and mitigations
- [Implementation plan](docs/IMPLEMENTATION_PLAN.md): the phase-by-phase build, with decisions recorded
- [Security policy](SECURITY.md), [contributing](CONTRIBUTING.md), [roadmap](ROADMAP.md), [changelog](CHANGELOG.md)

## 🧰 Tech

Go with the standard library `net/http`, Postgres 16 via `pgx` with raw SQL, and `golang-migrate` for schema migrations.

## 📄 License

SCIMage is released under the [MIT License](LICENSE). You are free to use, modify, and distribute it, including in commercial products. The one condition is attribution: keep the copyright line (`Copyright (c) 2026 Megha Mahna`) and the license notice in any copy or substantial portion. If you build on SCIMage, a link back to this repository is appreciated.
