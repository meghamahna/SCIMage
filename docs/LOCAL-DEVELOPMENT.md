# Local Development Setup

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+
- [Docker](https://www.docker.com/products/docker-desktop/) with
  Compose (Docker Desktop on macOS/Windows, Docker Engine + Compose
  plugin on Linux)
- [`jq`](https://jqlang.github.io/jq/) (`brew install jq`) — used by
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
> bash scripts — use WSL2, not native PowerShell/cmd.

## Clone and configure

```bash
git clone https://github.com/meghamahna/SCIMage.git
cd SCIMage

# activate the real git pre-commit hook (secrets scan, gofmt, go vet, go test)
# — this sets local git config, so it has to be run again on every fresh clone
make hooks-install

# copy the env template and fill in real values
# (never commit the result — .env is gitignored)
cp .env.example .env
```

## Run it

```bash
# start Postgres and bring the schema up to date
make up

# run the server
make run
```

`make up` applies migrations for you once Postgres is accepting
connections, so a fresh clone is one command away from a usable
database.

## Stop / restart Postgres

All targets take an optional `SERVICE=<name>` (currently only
`postgres` exists) to scope the action to one service instead of the
whole project — `make` variables are passed as `VAR=value`, not
`--flag=value`.

```bash
make stop                    # stop, keep data — everything, or SERVICE=postgres for just one
make restart                 # restart in place, keep data — same SERVICE= scoping
make down                    # stop + remove containers/network (always whole-project)
make reset                   # full reset: wipes the data volume, re-initializes from .env
make ps                      # what's running
make logs                    # follow logs — SERVICE=postgres to scope to one
```

## Migrations

Schema migrations live in [`migrations/`](../migrations/) and are managed
with [golang-migrate](https://github.com/golang-migrate/migrate).

```bash
make migrate                 # apply everything pending (also run by make up)
make migrate-down            # roll back the most recent migration
make migrate-version         # which version the database is on
```

[`scripts/migrate.sh`](../scripts/migrate.sh)
uses a host binary if you have one and otherwise runs the official
`migrate/migrate` image on the compose network. Either way the
connection string is assembled at runtime from `.env` and passed straight
to the CLI.

To add a migration, create the next numbered pair by hand:

```text
migrations/000002_<description>.up.sql
migrations/000002_<description>.down.sql
```

Write the `down` half alongside it, and test the round trip
(`make migrate && make migrate-down && make migrate`) before committing, so
the migration is safe to reverse in a deployment.

## View it

The server listens on `:8080` (override with `SCIM_ADDR`). There's no token
to put in `.env` — create a tenant and issue it a token first:

```bash
set -a; source .env; set +a

TENANT_ID=$(go run ./cmd/scimage-admin tenant create -name "Local dev" | awk '/^Created tenant/{print $3}')
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

Structured JSON goes to stdout and to `logs/scimage-<date>.log`. To see what an
identity provider actually sends — the fastest way to diagnose an integration:

```bash
SCIM_LOG_REQUESTS=1 make run

# in another terminal
tail -f logs/scimage-$(date -u +%F).log | jq 'select(.msg == "request")'
```

Those entries include request bodies and therefore user attributes, so the
directory is `0700`, files are `0600`, and `logs/` is gitignored.

## Audit trail

Every create, replace and deactivate writes a row to `audit_log` in the same
transaction as the change:

```bash
docker compose exec postgres psql -U scimage -d scimage -c \
  "SELECT at, actor_token, action, result, target_id FROM audit_log ORDER BY at DESC LIMIT 10;"
```

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
`POSTGRES_PASSWORD` in `.env` — Postgres applies that variable when it
initializes a *fresh* data volume, so an existing container keeps the
password it was created with. Two ways forward:

- sync the running instance to match your new `.env` value:

  ```bash
  set -a; source .env; set +a
  docker compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "ALTER USER $POSTGRES_USER WITH PASSWORD '$POSTGRES_PASSWORD';"
  ```

- or wipe and re-initialize (see "full reset" above) if there's no
  data you need to keep.
