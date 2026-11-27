---
id: workflow-docker
kind: workflow
title: Docker image and compose stack
summary: Multi-stage Dockerfile builds a static nonroot scimage binary; docker-compose runs only Postgres 16, published on a configurable 127.0.0.1 port; local-testing docker run joins the compose network
tags: [docker, dockerfile, docker compose, compose, container, image, build image, distroless, nonroot, postgres container, port, loopback, 127.0.0.1, POSTGRES_PORT, volume, deploy, local testing, docker run, network, env-file, DATABASE_URL]
links: [related:command-dev-commands, related:api-env-vars, related:workflow-migrations, related:gotcha-console-docker-bind-address, related:doc-readme]
sources: [Dockerfile:1-28, docker-compose.yml:1-20, Makefile:32-56, scripts/migrate.sh:56-87, scripts/with-env.sh:12-21, README.md:212-235]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Build stage uses golang:1.26.11 and downloads modules first, so that layer is cached until go.mod or go.sum change → Dockerfile:2-8
- Binary is built with CGO_ENABLED=0 and `-ldflags="-s -w"` from ./cmd/server to /out/scimage → Dockerfile:12-14
- Runtime stage is gcr.io/distroless/static-debian12:nonroot: no shell, no package manager, CA certs included for webhooks and ARIA → Dockerfile:16-19
- The image runs as nonroot with ENTRYPOINT `scimage`, copied to /usr/local/bin → Dockerfile:21-28
- EXPOSE 8080 is documentation only; SCIM_ADDR controls the real bind → Dockerfile:23-25
- No Makefile target builds the image; the Make targets wrap `docker compose` for Postgres → Makefile:32-56
- Compose has one service, `postgres` (image postgres:16, restart unless-stopped) → docker-compose.yml:2-4
- Compose takes POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB from the environment with no defaults → docker-compose.yml:6-8
- Host port is `${POSTGRES_PORT:-5432}` bound to 127.0.0.1 only, mapped to container port 5432, so the dev DB is not reachable off-host → docker-compose.yml:9-10
- Compose sets no top-level `name:`, so the default network is `<folder>_default` → docker-compose.yml:1-20
- Data lives in the named volume postgres_data → docker-compose.yml:11-12, docker-compose.yml:19-20
- Healthcheck: pg_isready every 5s, 5 retries → docker-compose.yml:13-17
- Migrations run from the host through scripts/migrate.sh; it uses a host migrate binary if present, else the migrate/migrate image on the compose network → scripts/migrate.sh:69-87
- The migrate step finds the compose network from the Postgres container → scripts/migrate.sh:74-76
- Local testing (README) runs the image on the compose network `scimage_default` with `--env-file .env` → README.md:219-220
- DATABASE_URL is built from POSTGRES_* values with host `postgres`, port 5432, exported inside the subshell and passed by name with a bare `-e DATABASE_URL`, which keeps the password out of the `docker run` command line → README.md:217, README.md:221, README.md:228
- The block runs in a subshell `( ... )`, so the variables loaded from .env by `set -a; source .env` do not stay in the terminal → README.md:215-216, README.md:227
- `-e CONSOLE_ADDR="0.0.0.0:8090"` with `-p 127.0.0.1:8090:8090` keeps the console loopback-only on the host → README.md:218, README.md:222, README.md:231
- Console token for the container comes from the host: `make console-token LABEL=...`, since the image has no scimage-admin → README.md:233
- The compose stack is local-dev only and publishes Postgres on 127.0.0.1 → README.md:235
## Gotchas
- `--env-file` reads values literally (no quote stripping, no inline comments), unlike scripts/with-env.sh, which loads .env by shell source → README.md:212, scripts/with-env.sh:14-18
- `scimage_default` is the Compose network, named after the lowercased project folder plus `_default`; a clone in a differently named folder finds its network with `docker network ls` → README.md:229
- The URL works as written when the password uses only letters, digits, `-`, `.` and `_`; any other character must be percent-encoded in place of `$POSTGRES_PASSWORD` → README.md:230
- The compose stack is local-dev only; processes on the host and containers on the Compose network can reach the database, so its password is the only access control → README.md:235
- On Linux, a port published on 127.0.0.1 is closed to other machines on Docker Engine 28.3.3 or later, for both Postgres and the console port → README.md:235
