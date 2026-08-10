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

Schema migrations live in [`migrations/`](migrations/) and are managed
with [golang-migrate](https://github.com/golang-migrate/migrate).

```bash
make migrate                 # apply everything pending (also run by make up)
make migrate-down            # roll back the most recent migration
make migrate-version         # which version the database is on
```

You don't need `migrate` installed. [`scripts/migrate.sh`](scripts/migrate.sh)
uses a host binary if you have one and otherwise runs the official
`migrate/migrate` image on the compose network. Either way the
connection string is assembled at runtime from `.env` — it's never
written to disk.

To add a migration, create the next numbered pair by hand:

```text
migrations/000002_<description>.up.sql
migrations/000002_<description>.down.sql
```

Always write the `down` half, and test the round trip
(`make migrate && make migrate-down && make migrate`) before committing —
a migration you can't reverse is a migration you can't safely deploy.

## View it

The server listens on `:8080` (override with `SCIM_ADDR`). Every request
needs the bearer token from your `.env` — the server won't start without
`SCIM_TOKEN` set. Audit logging is still Phase 7, so mutations aren't
recorded yet.

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

curl -H "Authorization: Bearer $SCIM_TOKEN" \
  "http://localhost:8080/Users?startIndex=1&count=10"
```

Set `SCIM_BASE_URL` if the server sits behind a proxy — `Location` and
`meta.location` are derived from the `Host` header otherwise, which is
client-controlled and reports `http` behind TLS termination.

## Run tests

```bash
make up      # the store tests need a running Postgres
make test
```

The store tests run against the real database, not a mock, and clean up
after themselves. They skip when no database is configured, so a plain
`go test ./...` still works.

## Troubleshooting

**`password authentication failed for user "scimage"`** after editing
`POSTGRES_PASSWORD` in `.env` — Postgres only applies that variable
when it initializes a *fresh* data volume. Editing `.env` after the
container already exists doesn't change the live database user's
password. Either:

- sync the running instance to match your new `.env` value:

  ```bash
  set -a; source .env; set +a
  docker compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "ALTER USER $POSTGRES_USER WITH PASSWORD '$POSTGRES_PASSWORD';"
  ```

- or wipe and re-initialize (see "full reset" above) if there's no
  data you need to keep.
