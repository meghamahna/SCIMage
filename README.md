<div align="center">

<img src="docs/assets/scimage_logo.png" alt="SCIMage: SCIM Audit and Governance Engine" width="200">

<h1 align="center">🧭 A SCIM 2.0 provisioning server with signed change delivery and an AI-advisory audit trail</h1>

[![CI](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml/badge.svg)](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml)
[![govulncheck](https://img.shields.io/github/actions/workflow/status/meghamahna/SCIMage/ci.yml?label=govulncheck)](https://github.com/meghamahna/SCIMage/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/meghamahna/SCIMage?logo=go&logoColor=white)](go.mod)
![PostgreSQL](https://img.shields.io/badge/Postgres-16-4169E1?style=flat&logo=postgresql&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/meghamahna/SCIMage?sort=semver&logo=github)](https://github.com/meghamahna/SCIMage/releases)

</div>

SCIMage is a focused SCIM 2.0 server written in Go. It implements the core `/Users` and `/Groups` resources from [RFC 7644](https://datatracker.ietf.org/doc/html/rfc7644), backed by real Postgres, with security practices that are load-bearing and covered by tests.

<!-- toc -->
## 📑 Contents

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

<!-- /toc -->

## ✨ Why SCIMage

SCIMage is a SCIM 2.0 server for the receiving side of enterprise provisioning. Beyond the core endpoints, two choices shape it:

- **Extend by config:** register any extra or custom attribute per tenant and it round-trips, keeping the core minimal and honest.
- **Self-hosted and transparent:** plain Go + Postgres + raw SQL, no framework and no lock-in; you own the data and the audit trail.

**Best fit:** a SaaS that needs to *receive* enterprise provisioning with a defensible security-and-audit story, wants to self-host, and values correctness over feature breadth.

## 💡 The problem it solves

Enterprise buyers expect to run your SaaS from their own identity provider: a new hire gets access automatically, and a departing employee loses it the moment HR disables them in Okta or Entra. That handshake is SCIM, and the work lands on the *service provider* side, the application receiving the calls. That end is easy to underbuild: skip `/Groups`, keep no audit trail, share one token across customers, and a security review asking who deprovisioned an account has no answer.

SCIMage is that receiving end, built to hold up:

- **Win the deal without the build.** RFC 7644 `/Users` and `/Groups`, validated against Microsoft's Entra SCIM validator, so you can say yes to enterprise provisioning without writing and maintaining a spec-compliant server yourself.
- **Prove it in an audit.** Every change is written to an audit trail in the same transaction as the change itself, so the record can never drift from what happened, and refused mutations are recorded as `denied` entries. That is the answer a SOC 2 or access review is looking for.
- **Keep customers apart.** Each tenant has its own path, its own issued and rotatable token, and data isolation enforced in code and covered by tests.
- **Make provisioning reach your app.** Signed, retried webhooks turn each change into an event the rest of your system can act on, instead of a row that just sits in a table.

## 📡 What it does

SCIMage is the *service provider* side of SCIM: the endpoint an identity provider provisions into. One deployment serves many customer organizations at once: each is a **tenant**, addressed under its own `/scim/v2/{tenantID}` path with its own issued token, isolated from every other tenant's data.

| Method | Endpoint                          | Behaviour                                                                       |
|--------|-----------------------------------|----------------------------------------------------------------------------------|
| POST   | `/scim/v2/{tenantID}/Users`       | Creates a user. `201` with a `Location` header, `409` on a duplicate `userName` or `externalId` |
| GET    | `/scim/v2/{tenantID}/Users/{id}`  | Fetches one user                                                                |
| GET    | `/scim/v2/{tenantID}/Users`       | Lists users, paginated with `startIndex` and `count`                            |
| PUT    | `/scim/v2/{tenantID}/Users/{id}`  | Replaces a user's attributes                                                    |
| PATCH  | `/scim/v2/{tenantID}/Users/{id}`  | Applies operations to a user (how identity providers deprovision)               |
| DELETE | `/scim/v2/{tenantID}/Users/{id}`  | Deactivates a user, preserving the row and its history                          |
| POST   | `/scim/v2/{tenantID}/Groups`      | Creates a group. `201` with a `Location` header, `409` on a duplicate `displayName` or `externalId` |
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

`externalId` is unique within a tenant for users and for groups. A create, `PUT` or `PATCH` that reuses one answers `409` with `scimType: uniqueness`. The same `externalId` can exist in different tenants. An `externalId` longer than 256 bytes answers `400` with `scimType: invalidValue`.

**Validated against [Microsoft's Entra ID SCIM validator](https://scimvalidator.microsoft.com/).** Core CRUD, filtering, and PATCH all pass. The `User` schema is deliberately reduced to a minimal set of typed columns. `displayName`, `title`, `preferredLanguage`, `name.formatted`/`name.middleName`, the enterprise extension and typed multi-valued emails aren't modeled that way. When a provider needs them, an operator registers the extra attributes per tenant (`scimage-admin attribute register`, gated by `SCIM_EXTENDED_ATTRIBUTES`) and the server captures and returns them through a JSONB pass-through, which keeps the core minimal while still round-tripping whatever Okta or Entra maps. See [Configuration](docs/CONFIGURATION.md#-extensible-attributes).

## 🧱 How it's built

![SCIMage architecture](docs/assets/SCIMAGE_architecture.png)

*The diagram shows the SCIM request path, the webhook dispatcher and ARIA. The admin console and the admin CLI are described in [Architecture](docs/ARCHITECTURE.md).*

Everything lives in **one Postgres database**, with every query scoped by `tenant_id` and each mutation writing its row, audit entry and outbound event in one transaction. ARIA is the one advisory branch, reading `audit_log` off to the side, clear of the store and the auth path.

[Architecture](docs/ARCHITECTURE.md) covers the request path, storage model, multi-tenancy and the `UserStore`/`GroupStore` interface contract in full.

## 📤 Change delivery

Every mutation queues a signed webhook, at-least-once with retries and a dead-letter queue, so a user created by an identity provider lands in your application directly.

Set `SCIM_WEBHOOK_URL` to turn it on. [Architecture](docs/ARCHITECTURE.md#-change-delivery) covers the outbox, claim leases, retry rules, the signing scheme and the event payload; `webhook.Verify` is exported for Go receivers.

## 🔒 Security practices

Each control is load-bearing and covered by tests: issued, tenant-scoped tokens compared with `crypto/subtle.ConstantTimeCompare` against a stored hash; auth applied by the router itself, so it covers the whole surface; cross-tenant isolation scoped on `tenant_id` in every query; audit logging in the same transaction as the change, with refused mutations recorded as `denied` entries; and privileged CLI actions audited to a named operator. Schema validation bounds attribute lengths before the database, outbound webhooks are signed with HMAC-SHA256 and verified with `hmac.Equal`, callers are rate-limited to a `429`, and secrets come from the environment, never hardcoded or logged. CI scans dependencies for known vulnerabilities, and the pre-commit hook scans staged changes for leaked credentials.

See [Threat model](docs/THREAT-MODEL.md) for the full threat-by-threat reasoning behind each of these, and [Configuration](docs/CONFIGURATION.md) for every environment variable.

Operational logs are structured JSON on stdout and in a dated file under `LOG_DIR`. The audit trail is separate and lives in the `audit_log` table, so a change and its record commit together.

## 🤖 ARIA: The AI advisory audit reviewer

`aria` (**A**udit **R**isk **I**ntelligence **A**dvisor) reads the audit trail and prints a plain-English briefing a reviewer can read in under a minute: clustered deactivations, changes landing off-hours, callers spiking in volume or racking up denials. The name is deliberate: it advises, and the decision stays with the reviewer.

Deterministic Go computes every signal (the thresholds live in `internal/aria` as constants: five deactivations inside ten minutes, activity outside business hours), the LLM only narrates them, and the code gives it no path into the store or the auth layer.

ARIA works with **any OpenAI-compatible chat-completions endpoint** (Anthropic's compat endpoint, OpenAI, OpenRouter, a local Ollama or vLLM). Point it there with `ARIA_LLM_BASE_URL`, `ARIA_LLM_API_KEY` and `ARIA_LLM_MODEL`.

```bash
make aria                                  # last 24h, every tenant
make aria TENANT=tenant_9f2a... SINCE=7d   # one tenant, last week
```

A quiet window prints a deterministic "nothing tripped the thresholds" line and skips the model, so a clean review runs without a key. See [Configuration](docs/CONFIGURATION.md#-audit-review-aria).

## 🚀 Getting started

**Prerequisites:** Go (the version in [`go.mod`](go.mod)), Docker with Compose (for Postgres), GNU Make, and `jq`. [Local development](docs/LOCAL-DEVELOPMENT.md) has versions and platform notes.

This is the local-development path. Docker's only job in it is running Postgres; the server itself runs directly on your host with `make run`, no container involved. If you want to containerize the server too (for example, to deploy it), that's a separate, later step: [Deploy with Docker](#-deploy-with-docker).

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

Open `http://127.0.0.1:8090/console`, sign in with the token as the password (leave the username blank), and create a tenant and its token by clicking.

*Option B, the CLI.* Create the tenant and token from the shell, then start the server.

```bash
make tenant NAME="Acme Corp"                     # prints the TENANT ID and SCIM base URL
make token TENANT=<tenant-id> LABEL="Okta prod"  # prints the token, shown once
make run                                          # SCIM API :8080
```

With `SCIM_BASE_URL` unset, the CLI and the console print the literal `<SCIM_BASE_URL>` in place of the host. Set it in `.env` to your external URL to see a usable one.

Either way, `CREATED BY` defaults to `$USER`, and every tenant and token action is recorded in `admin_audit_log` (`make audit-list`). The full CLI, rotation, and the console-vs-CLI command map are in [Configuration](docs/CONFIGURATION.md#-console-ui-or-cli).

**4. Make your first request** with the tenant id and token from step 3. Export them first (the [first-request walkthrough](docs/LOCAL-DEVELOPMENT.md#-send-your-first-request) shows how to capture them from the CLI):

```bash
export TENANT_ID=<tenant-id> TOKEN=<token>

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

The repo ships a `Dockerfile`, so you can build a small (under 30 MB) container and run it anywhere. There is no image to pull; you build it once. The image holds only the `scimage` server binary; `scimage-admin` and its `make` targets are operator tooling and stay host-side.

```bash
make up

docker build -t scimage .
```

**Production.** Env vars come from the orchestrator (its secret manager, task definition, or Kubernetes `Secret`/`ConfigMap`), passed at `docker run` time or however your platform injects them. This is the standard way to configure a container: nothing is baked into the image and nothing is auto-loaded from a file inside it, so the same image runs unchanged in every environment. `.env` is a local-dev convenience only; it never ships in the image (it's gitignored) and the server code never reads it directly, it only reads real environment variables.

```bash
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@your-db:5432/scimage?sslmode=require" \
  -e SCIM_BASE_URL="https://scim.yourcompany.com" \
  -e LOG_DIR= \
  scimage
```

The container needs a Postgres you already operate, with the migrations applied. `make migrate` only reaches the Compose Postgres (it builds its connection from the `POSTGRES_*` values in `.env` and never reads `DATABASE_URL`), so for another database run the `golang-migrate` image over the files in [`migrations/`](migrations/):

```bash
docker run --rm \
  --volume "$PWD/migrations:/migrations:ro" \
  migrate/migrate:v4.18.1 \
  -path=/migrations \
  -database "postgres://user:pass@your-db:5432/scimage?sslmode=require" \
  up
```

The `golang-migrate` CLI takes the same `-path`, `-database` and `up` arguments. Add `--network` to the `docker run` if the database is reachable only from a Docker network.

Run it behind a proxy that terminates TLS, and set `SCIM_BASE_URL` to the public HTTPS URL so the links the server returns stay `https`. For an orchestrator, `GET /healthz` is the liveness check and `GET /readyz` is the readiness check. Every setting is an environment variable; see [Configuration](docs/CONFIGURATION.md).

**Local testing**, against the same Postgres and `.env` that `make run` uses. This assumes you've already done the [Getting started](#-getting-started) setup: `.env` filled in and `make up` run, so Postgres is up with migrations applied. `--env-file .env` passes every variable in the file to the container. Docker reads it literally, so keep values unquoted and comments on their own lines. If a value needs quotes for the shell, such as a URL containing `&`, add `-e NAME` for it after `--env-file` so the container gets the value the shell loaded. Inside the container, `localhost` and `127.0.0.1` point at the container itself, so the command overrides `DATABASE_URL` and `CONSOLE_ADDR`. It also sets `LOG_DIR` empty, which keeps logs on stdout (see [Configuration](docs/CONFIGURATION.md#-logging)). A webhook URL in `.env` that points at `localhost` fails to connect for the same reason.

```bash
(
  set -a; source .env; set +a
  export DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@postgres:5432/$POSTGRES_DB?sslmode=disable"
  docker run --rm -p 8080:8080 -p 127.0.0.1:8090:8090 \
    --network scimage_default \
    --env-file .env \
    -e DATABASE_URL \
    -e CONSOLE_ADDR="0.0.0.0:8090" \
    -e LOG_DIR= \
    scimage
)
```

- The parentheses keep the variables loaded from `.env` out of your terminal afterwards.
- `DATABASE_URL` points at the Compose database by its service name, `postgres`, so it works from inside the Compose network. It is exported and passed by name with `-e DATABASE_URL`, which keeps the password out of the `docker run` command line.
- `scimage_default` is the Compose network, named after the lowercased project folder plus `_default`. If your clone's folder has a different name, find the network with `docker network ls`.
- The URL works as written when the password uses only letters, digits, `-`, `.` and `_`. Percent-encode any other character in place of `$POSTGRES_PASSWORD`, or choose a password from that set.
- `CONSOLE_ADDR` binds `0.0.0.0` inside the container, while a bare-metal deploy uses `127.0.0.1`. Docker's port publishing forwards to the container's network interface, so a console bound to the container's `127.0.0.1` is unreachable even with `-p` mapped. `-p 127.0.0.1:8090:8090` applies the loopback restriction on the host, so the console stays reachable from this machine only.

Issue yourself a console login token from the host (the container has no `scimage-admin`): `make console-token LABEL="local docker test"`, then open `http://localhost:8090/console` with it.

`docker-compose.yml` is for local development. It publishes Postgres on `127.0.0.1` only, so other machines cannot connect. On Linux, a port published on `127.0.0.1` is closed to other machines on Docker Engine 28.3.3 or later, and that covers the console port in the command above as well. Processes on this host and containers on the Compose network can still reach the database, so its password is the only access control.

## ✅ Running tests

Tests need Postgres running, so `make up` first:

```bash
make up
make test
```

Store and audit tests hit real SQL and constraints in Postgres. Handlers run through `httptest`, and the webhook dispatcher against a real `httptest` receiver. Every suite cleans up after itself.

## 🎯 Roadmap

`v1.0.0` is tagged, covering phases 1 through 13. Phase 14 (the admin console and the OpenAPI spec with Swagger UI) is complete on top of that and ships in the next release.

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
