#!/usr/bin/env bash
# Runs golang-migrate against the compose Postgres. Use via `make migrate`.
#
#   scripts/with-env.sh scripts/migrate.sh [args...]   (defaults to: up)
#
# Expects POSTGRES_* in the environment; with-env.sh is the one place .env gets
# loaded. The DSN is assembled at runtime, never written to disk — argv is
# visible in `ps`, which is acceptable for a local dev database and is all
# golang-migrate's CLI offers.
#
# Falls back to the migrate/migrate image when no host binary is installed, so
# a fresh clone needs nothing beyond Docker.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

MIGRATE_IMAGE=${MIGRATE_IMAGE:-migrate/migrate:v4.18.1}
service=postgres

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

# Percent-encode so a password with URL-reserved characters can't corrupt the DSN.
urlenc() { jq -rn --arg s "$1" '$s|@uri'; }
user_enc=$(urlenc "$POSTGRES_USER")
pass_enc=$(urlenc "$POSTGRES_PASSWORD")
db_enc=$(urlenc "$POSTGRES_DB")

args=("$@")
[[ ${#args[@]} -eq 0 ]] && args=(up)

# `docker compose up -d` returns before Postgres accepts connections, so this
# is safe to chain straight off `make up`.
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

# On the compose network the service name resolves and the port is always 5432,
# whatever POSTGRES_PORT publishes on the host.
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
