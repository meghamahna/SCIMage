#!/usr/bin/env bash
# Loads .env, then runs the command passed as arguments:
#
#   scripts/with-env.sh go test ./...
#
# An exported shell variable wins over .env, matching docker compose.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

if [[ -f .env ]]; then
  preset=$(export -p)
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
  eval "$preset"
fi

exec "$@"
