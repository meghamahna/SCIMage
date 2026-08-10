# Local Development Setup

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+
- [Docker](https://www.docker.com/products/docker-desktop/) with
  Compose (Docker Desktop on macOS/Windows, Docker Engine + Compose
  plugin on Linux)

Verify:

```bash
go version
docker --version
docker compose version
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
# start Postgres
make up

# apply schema migrations
make migrate

# run the server
make run
```

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

## View it

The server listens on `:8080`. With `SCIM_TOKEN` set to whatever's in
your `.env` (not yet in `.env.example` — `SCIM_TOKEN` lands in Phase 5):

```bash
curl -X POST http://localhost:8080/Users \
  -H "Authorization: Bearer $SCIM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "userName": "jdoe",
    "name": {"givenName": "Jane", "familyName": "Doe"},
    "emails": [{"value": "jdoe@example.com", "primary": true}],
    "active": true
  }'
```

## Run tests

```bash
make test
```

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
