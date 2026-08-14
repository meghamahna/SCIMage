# Local Development Setup

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+
- [Docker](https://www.docker.com/products/docker-desktop/) with
  Compose (Docker Desktop on macOS/Windows, Docker Engine + Compose
  plugin on Linux)
- [`jq`](https://jqlang.github.io/jq/) (`brew install jq`), used by
  the migration and secret-scanning scripts

Verify:

```bash
go version
docker --version
docker compose version
jq --version
docker info >/dev/null 2>&1 && echo "daemon: RUNNING" || echo "daemon: NOT RUNNING"
```

> **Windows:** the `Makefile` targets and `.githooks/pre-commit` are
> bash scripts; use WSL2, not native PowerShell/cmd.

## Clone and configure

```bash
git clone https://github.com/meghamahna/SCIMage.git
cd SCIMage
make hooks-install
cp .env.example .env
```

`.env` is gitignored; fill in real values before running anything else.
`make hooks-install` sets a local git config, so it has to be run again on
every fresh clone.

## Make commands

Run `make` with no arguments to see this same list generated from the
Makefile itself. `SERVICE=` and other variables are passed as `make up
SERVICE=postgres`, not `--service=postgres`.

| Command | What it does | Comments |
| --- | --- | --- |
| `make help` | Shows the command list | Default target; bare `make` runs this |
| `make up` | Starts Postgres and applies migrations | `SERVICE=postgres` to scope to one service |
| `make down` | Stops and removes all containers/network | Always whole-project; Compose doesn't support scoping `down` |
| `make stop` | Stops service(s) without removing them | `SERVICE=postgres` to scope to one |
| `make restart` | Restarts service(s) in place, keeping data | `SERVICE=postgres` to scope to one |
| `make reset` | Wipes the data volume and re-initializes from `.env` | Destructive, always whole-project; use when you want a clean slate |
| `make ps` | Shows what's running | |
| `make logs` | Follows container logs | `SERVICE=postgres` to scope to one |
| `make migrate` | Applies every pending migration | `make up` already runs this for you; use this to re-run after adding a migration file |
| `make migrate-down` | Rolls back the most recent migration | |
| `make migrate-version` | Shows which migration version the database is on | |
| `make test` | Runs the full test suite against a real Postgres | Run `make up` first |
| `make run` | Runs the SCIM server | Listens on `:8080` by default (`SCIM_ADDR` to override) |
| `make fmt` | Formats every Go file | `gofmt` + `goimports` |
| `make tenant NAME="Acme Corp"` | Creates a tenant | Prints the tenant id and its SCIM base URL |
| `make tenant-list` | Lists every tenant | |
| `make token TENANT=<id> LABEL="Okta prod"` | Issues a token for a tenant | Optional `EXPIRES=90d`; token is shown once |
| `make token-list TENANT=<id>` | Lists a tenant's tokens | Never shows the secret, only metadata |
| `make token-revoke KEY=<keyID>` | Revokes a token immediately | Irreversible; idempotent on an already-revoked key |
| `make attr-register TENANT=<id> NAME=displayName` | Registers an extra attribute a tenant should capture | Optional `TYPE=string`; server captures it only with `SCIM_EXTENDED_ATTRIBUTES=1` |
| `make attr-list TENANT=<id>` | Lists a tenant's registered extra attributes | |
| `make attr-unregister TENANT=<id> NAME=displayName` | Removes a registered attribute | Stops future capture; doesn't touch stored values |
| `make audit-list [TENANT=<id>]` | Reads the admin-audit trail | Every tenant created, token issued or revoked, attribute registered; omit `TENANT` for every tenant |
| `make aria [TENANT=<id>] [SINCE=24h]` | Runs ARIA, the advisory audit reviewer | Optional `TZ=` for the off-hours check; needs the `ARIA_LLM_*` variables only when a window has findings |
| `make hooks-install` | Activates the real git pre-commit hook | One-time per clone; doesn't travel with the repo |

## Supported attributes

What `POST`/`PUT`/`PATCH /Users` actually accept and return. The `/Schemas`
endpoint always reflects this table exactly, since both come from the same
code (`internal/scim/models.go`, `internal/scim/discovery.go`).

| Attribute | Notes |
| --- | --- |
| `id` | Server-assigned, read-only |
| `externalId` | The identity provider's own key, for reconciliation |
| `userName` | Required; unique per tenant, case-insensitively |
| `name.givenName`, `name.familyName` | |
| `emails[].value`, `emails[].primary` | Multiple accepted on input; only the primary (or first) is stored and returned |
| `active` | Defaults to `true`; a `PATCH replace` on this is how identity providers deprovision |
| `meta.resourceType`, `.created`, `.lastModified`, `.location` | Server-managed |

Not modeled: `displayName`, `title`, `preferredLanguage`, `name.formatted`,
`name.middleName`, and typed multi-valued emails. A deliberate scope cut,
not an oversight; see the [README](../README.md) for the Entra validator
results this shows up in.

What `POST`/`PUT`/`PATCH /Groups` accepts and returns, same source-of-truth
reasoning:

| Attribute | Notes |
| --- | --- |
| `id` | Server-assigned, read-only |
| `externalId` | The identity provider's own key, for reconciliation |
| `displayName` | Required; unique per tenant, case-insensitively |
| `members[].value`, `members[].$ref` | User ids and a link back to `/Users/{id}`; `members[].display` is accepted but never populated |
| `meta.resourceType`, `.created`, `.lastModified`, `.location` | Server-managed |

`DELETE /Groups/{id}` is a real deletion, not the soft delete `DELETE
/Users/{id}` does — the Group schema has no `active` attribute to
deactivate into.

## Migrations

Schema migrations live in [`migrations/`](../migrations/) and are managed
with [golang-migrate](https://github.com/golang-migrate/migrate). Use
`make migrate` / `make migrate-down` / `make migrate-version` from the table
above; [`scripts/migrate.sh`](../scripts/migrate.sh) waits for Postgres to be
ready first, then uses a host `migrate` binary if you have one and otherwise
runs the official `migrate/migrate` image on the compose network, so no local
install is required either way.

To add a migration, create the next numbered pair by hand (check
`migrations/` for the current highest number — this example is illustrative,
not the literal next one):

```text
migrations/000010_<description>.up.sql
migrations/000010_<description>.down.sql
```

Write the `down` half alongside it, and test the round trip
(`make migrate && make migrate-down && make migrate`) before committing, so
the migration is safe to reverse in a deployment.

## View it

The server listens on `:8080` (override with `SCIM_ADDR`). There's no token
to put in `.env`. Create a tenant and issue it a token first:

```bash
set -a; source .env; set +a

TENANT_ID=$(go run ./cmd/scimage-admin tenant create -name "Local dev" | awk '/^TENANT ID/{print $NF}')
TOKEN=$(go run ./cmd/scimage-admin token issue -tenant "$TENANT_ID" -label "local curl" | tail -1)

curl -X POST "http://localhost:8080/scim/v2/$TENANT_ID/Users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/scim+json" \
  -d '{
    "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
    "userName": "jdoe",
    "name": {"givenName": "Jane", "familyName": "Doe"},
    "emails": [{"value": "jdoe@example.com", "primary": true}]
  }'

curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/scim/v2/$TENANT_ID/Users?startIndex=1&count=10"
```

Set `SCIM_BASE_URL` when running behind a proxy, so `Location` and
`meta.location` use your external URL and scheme. Left unset, they derive
from the request's `Host` header, which suits local development.

## Logs

Structured JSON goes to stdout and to `logs/scimage-<date>.log`. Seeing what an
identity provider actually sends is the fastest way to diagnose an integration:

```bash
SCIM_LOG_REQUESTS=1 make run

# in another terminal
tail -f logs/scimage-$(date -u +%F).log | jq 'select(.msg == "request")'
```

Those entries include request bodies and therefore user attributes, so the
directory is `0700`, files are `0600`, and `logs/` is gitignored.

## Audit trail

Every create, replace, deactivate and delete — for both `/Users` and
`/Groups` — writes a row to `audit_log` in the same transaction as the
change; every tenant/token admin action writes a row to `admin_audit_log`
the same way (see the table above for reading it back via `scimage-admin
audit list`). To query `audit_log` directly:

```bash
docker compose exec postgres psql -U scimage -d scimage -c \
  "SELECT at, resource_type, actor_token, action, result, target_id FROM audit_log ORDER BY at DESC LIMIT 10;"
```

## Audit review (ARIA)

ARIA reads that audit trail and prints a short, plain-English briefing of
activity worth a human's glance: deactivations clustered in a short window,
changes landing off-hours, and callers spiking in volume or denials.
Deterministic Go computes the signals, and an LLM only phrases them. The
briefing goes to you, and the code keeps it there, clear of the store and the
auth path.

**One-time setup.** Point ARIA at any OpenAI-compatible chat-completions
endpoint by adding three variables to `.env`. With a Claude API key:

```bash
ARIA_LLM_BASE_URL=https://api.anthropic.com/v1
ARIA_LLM_API_KEY=sk-ant-...
ARIA_LLM_MODEL=claude-sonnet-4-5
```

OpenAI, OpenRouter, and a local Ollama or vLLM work the same way: set the base
URL, key, and model to match the provider you run.

**Run a review:**

```bash
make aria                                  # last 24h, every tenant
make aria TENANT=<tenantID> SINCE=7d       # one tenant, last 7 days
make aria SINCE=48h TZ=America/Vancouver   # custom window and off-hours timezone
```

`SINCE` accepts a day count (`7d`) or any Go duration (`24h`, `90m`), and
defaults to `24h`. A quiet window prints a deterministic "nothing tripped the
thresholds" line and skips the model, so a clean review runs even with no key
configured. ARIA calls the LLM only when a window has something to summarize.
[Configuration](CONFIGURATION.md#audit-review-aria) lists every variable and
flag.

## Run tests

```bash
make up      # the integration tests use a real Postgres
make test
```

The store tests run against the real database, not a mock, and clean up
after themselves. They skip when no database is configured, so a plain
`go test ./...` still works.

## Troubleshooting

**`password authentication failed for user "scimage"`** after editing
`POSTGRES_PASSWORD` in `.env`. Postgres applies that variable when it
initializes a *fresh* data volume, so an existing container keeps the
password it was created with. Two ways forward:

- sync the running instance to match your new `.env` value:

  ```bash
  set -a; source .env; set +a
  docker compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "ALTER USER $POSTGRES_USER WITH PASSWORD '$POSTGRES_PASSWORD';"
  ```

- or wipe and re-initialize with `make reset` if there's no data you need to
  keep.
