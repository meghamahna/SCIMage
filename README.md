<div align="center">

# 🛡️ SCIMage

### A SCIM 2.0 provisioning server, built from the other side of the wire

![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/Postgres-16-4169E1?style=flat&logo=postgresql&logoColor=white)
![License: MIT](https://img.shields.io/badge/license-MIT-green?style=flat)
![Status](https://img.shields.io/badge/status-work%20in%20progress-yellow?style=flat)

</div>

SCIMage is a focused SCIM 2.0 server written in Go. It implements the core `/Users` resource from [RFC 7644](https://datatracker.ietf.org/doc/html/rfc7644), backed by real Postgres, with security practices that are load-bearing and covered by tests.

## 💡 Why I built this

I spent two years building JML (joiner mover leaver) automation on the identity provider side, configuring Okta Workflows to push user provisioning into 60+ SaaS applications. That gave me a solid understanding of the SCIM spec from the client's point of view.

This project is the other half: the server that receives those SCIM calls and applies them, which is the side most SaaS products build and maintain themselves. It demonstrates both ends of the handshake.

## ⚙️ What it does

SCIMage is the *service provider* side of SCIM — the endpoint an identity provider provisions into. It implements the core `/Users` resource:

| Method | Endpoint      | Behaviour                                                                       |
|--------|---------------|---------------------------------------------------------------------------------|
| POST   | `/Users`      | Creates a user. `201` with a `Location` header, `409` on a duplicate `userName` |
| GET    | `/Users/{id}` | Fetches one user                                                                |
| GET    | `/Users`      | Lists users, paginated with `startIndex` and `count`                            |
| PUT    | `/Users/{id}` | Replaces a user's attributes                                                    |
| DELETE | `/Users/{id}` | Deactivates a user, preserving the row and its history                          |

Responses use `application/scim+json`, and errors use the SCIM Error schema with the appropriate `scimType`.

`userName` uniqueness is enforced case-insensitively, matching the spec's `caseExact=false` characteristic — so `bjensen` and `BJensen` are correctly treated as the same identity.

## 🏗️ How it's built

```mermaid
flowchart LR
    IDP[Identity provider] -->|SCIM request, Bearer token| AUTH[Auth middleware]
    AUTH --> RL[Rate limiter]
    RL --> ROUTER{Router}
    ROUTER -->|POST /Users| CREATE[Create]
    ROUTER -->|GET /Users, /Users/:id| READ[List / Fetch]
    ROUTER -->|PUT /Users/:id| UPDATE[Replace]
    ROUTER -->|DELETE /Users/:id| DEACTIVATE[Deactivate]
    CREATE --> TX[(Postgres: users + audit_log<br/>one transaction)]
    UPDATE --> TX
    DEACTIVATE --> TX
    READ --> DB[(Postgres: users)]
```

The request path is deliberately plain: auth middleware checks the bearer token, the rate limiter admits the request, the router dispatches to a handler, the handler validates the payload and maps it to a Postgres row. Postgres is the single source of truth. Standard library `net/http` and raw SQL keep the behaviour visible in the code.

## 🔒 Security practices

These are load-bearing, and each one is covered by tests:

- **Constant-time token comparison.** Bearer tokens are compared with `crypto/subtle.ConstantTimeCompare` over SHA-256 digests. Hashing gives both sides a fixed width, keeping the comparison constant-time with respect to the token's length as well as its content.
- **Auth applied by the router itself.** `Routes()` wraps every path in the bearer check, so authentication covers the whole surface structurally rather than per handler.
- **Audit logging in the same transaction as the change.** Create, replace and deactivate each write an entry — actor, action, target user ID, timestamp, before/after state — inside the transaction that makes the change. The entry and the change commit together, so every modification carries a record. Refusals are recorded too, which makes a burst of denied deactivations visible to a reviewer.
- **Schema validation before the database.** Every incoming SCIM payload is checked against the expected shape, with attribute lengths bounded, before it reaches a query.
- **Rate limiting per caller.** A token bucket returns `429` with `Retry-After`, so a runaway sync loop is bounded.
- **Secrets from the environment.** The bearer token and database credentials are read from environment variables at runtime. Git hooks scan staged diffs for credential-shaped content, and CI runs `govulncheck` against dependencies.

### Configuration

| Variable                              | Purpose                                                                                 |
|---------------------------------------|-----------------------------------------------------------------------------------------|
| `SCIM_TOKEN`                          | Bearer token every request presents. Required — the server validates it at startup.     |
| `DATABASE_URL`                        | Postgres connection string. Assembled from `POSTGRES_*` when unset.                     |
| `SCIM_ADDR`                           | Listen address. Defaults to `:8080`.                                                    |
| `SCIM_BASE_URL`                       | External base URL, used for `Location` and `meta.location` when running behind a proxy. |
| `SCIM_RATE_LIMIT` / `SCIM_RATE_BURST` | Token bucket, in requests per second. Defaults to 20/40.                                |

Generate a token with `openssl rand -hex 32`. The server requires at least 16 characters.

### Rotating the token

1. Generate a new token: `openssl rand -hex 32`
2. Set it as `SCIM_TOKEN` and restart the server.
3. Update the token in your identity provider.

Treat `SCIM_TOKEN` as a privileged credential — it authorizes directory changes — and rotate it whenever exposure is suspected. Dual-token rotation, which removes the restart from this sequence, is on the roadmap.

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

The server starts on `:8080`. Migrations run through `golang-migrate`, and `make migrate` uses a host `migrate` binary when one is present and the official container otherwise. See [LOCAL-DEVELOPMENT.md](LOCAL-DEVELOPMENT.md) for prerequisites and the full set of targets.

## 📬 Example request

```bash
set -a; source .env; set +a

curl -X POST http://localhost:8080/Users \
  -H "Authorization: Bearer $SCIM_TOKEN" \
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

Store and handler tests run against a real Postgres instance via `docker-compose`, exercising the actual SQL and constraints. Handlers are driven through `httptest`. Both suites clean up the rows they create.

## 🗺️ Roadmap

Tracked in detail in [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md).

- **Identity provider interoperability** *(next)* — the discovery endpoints (`/ServiceProviderConfig`, `/Schemas`, `/ResourceTypes`), `externalId` storage, `userName` and `externalId` filtering, and `PATCH` operations. This is what an Okta or Entra tenant exercises during setup, so it's built and verified against a live developer tenant.
- **Change delivery** — signed outbound webhooks with retries, and a `UserStore` interface, so provisioned users flow into an application's own schema.
- **Multi-tenancy with issued API tokens** — per-tenant SCIM URLs, tokens stored as hashes and shown once at creation, revocation, and overlap-window rotation that needs no restart.
- **`/Groups`** — group resources and membership.
- **SAGE: SCIM Audit & Governance Engine** — a companion CLI (`cmd/sage`) that reads the audit trail and writes a plain-English summary of what merits a human's attention: bulk deactivations in a short window, off-hours changes, or a caller's volume rising sharply. SAGE is advisory by design: it surfaces signal, while every create, replace and deactivate stays in deterministic Go code. A sage advises; the code decides.
- **Release engineering** — health and readiness endpoints, graceful shutdown, a published container image, tagged releases, and setup guides for Okta and Entra.

## 🧰 Tech

Go with the standard library `net/http`, Postgres 16 via `pgx` with raw SQL, and `golang-migrate` for schema migrations. Few layers between the code and what runs.

## 📄 License

MIT, see [LICENSE](LICENSE).
