#!/usr/bin/env bash
# Runs golang-migrate against the Postgres started by docker-compose.
# Invoked by `make migrate`, `make migrate-down`, `make migrate-version`.
#
#   scripts/migrate.sh [migrate args...]     (defaults to: up)
#
# The connection string is never written to disk: it is assembled here at
# runtime from the environment (.env, which is gitignored). It is still passed
# in argv, so it is visible in `ps` while the command runs — acceptable for a
# local dev database, and golang-migrate's CLI offers no env-var alternative
# to -database.
#
# If a `migrate` binary is on PATH it is used directly; otherwise the official
# migrate/migrate image is run on the compose network, so a fresh clone needs
# nothing beyond Docker.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

MIGRATE_IMAGE=${MIGRATE_IMAGE:-migrate/migrate:v4.18.1}
service=postgres

if [[ -f .env ]]; then
  # An already-exported shell variable wins over .env, matching how docker
  # compose resolves the same names — otherwise `POSTGRES_DB=x make up` would
  # start one database and migrate a different one.
  preset=$(export -p)
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
  eval "$preset"
fi

for var in POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB; do
  if [[ -z "${!var:-}" ]]; then
    echo "migrate: $var is not set — copy .env.example to .env and fill it in" >&2
    exit 1
  fi
done

if ! command -v jq >/dev/null 2>&1; then
  echo "migrate: jq is required (brew install jq)" >&2
  exit 1
fi

# Percent-encode the credentials so a password containing URL-reserved
# characters (@ : / ?) doesn't silently corrupt the DSN.
urlenc() { jq -rn --arg s "$1" '$s|@uri'; }
user_enc=$(urlenc "$POSTGRES_USER")
pass_enc=$(urlenc "$POSTGRES_PASSWORD")
db_enc=$(urlenc "$POSTGRES_DB")

args=("$@")
[[ ${#args[@]} -eq 0 ]] && args=(up)

# `docker compose up -d` returns as soon as the container starts, which is
# before Postgres accepts connections — wait it out so this is safe to chain
# straight off `make up`.
wait_for_postgres() {
  local cid=$1 i
  for ((i = 0; i < 60; i++)); do
    if docker exec "$cid" pg_isready -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "migrate: Postgres did not become ready within 60s — check 'make logs'" >&2
  return 1
}

if ! command -v docker >/dev/null 2>&1; then
  echo "migrate: docker is required — see LOCAL-DEVELOPMENT.md for prerequisites" >&2
  exit 1
fi

cid=$(docker compose ps -q "$service" 2>/dev/null || true)
if [[ -z "$cid" ]]; then
  echo "migrate: the '$service' container isn't running — run 'make up' first" >&2
  exit 1
fi

wait_for_postgres "$cid"

if command -v migrate >/dev/null 2>&1; then
  dsn="postgres://${user_enc}:${pass_enc}@localhost:${POSTGRES_PORT:-5432}/${db_enc}?sslmode=disable"
  exec migrate -path migrations -database "$dsn" "${args[@]}"
fi

# No host binary — run the CLI in a container attached to the same network as
# Postgres, where the service name resolves and the port is always 5432
# regardless of what POSTGRES_PORT publishes on the host.
network=$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{"\n"}}{{end}}' "$cid" | head -n1)
if [[ -z "$network" ]]; then
  echo "migrate: could not determine the compose network for '$service'" >&2
  exit 1
fi

dsn="postgres://${user_enc}:${pass_enc}@${service}:5432/${db_enc}?sslmode=disable"
exec docker run --rm \
  --network "$network" \
  --volume "$repo_root/migrations:/migrations:ro" \
  "$MIGRATE_IMAGE" \
  -path=/migrations -database "$dsn" "${args[@]}"
