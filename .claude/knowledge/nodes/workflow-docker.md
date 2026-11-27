---
id: workflow-docker
kind: workflow
title: Docker image and compose stack
summary: Multi-stage Dockerfile builds a static nonroot scimage binary; docker-compose runs only Postgres 16, published on a configurable 127.0.0.1 port; local-testing docker run joins the compose network
tags: [docker, dockerfile, docker compose, compose, container, image, build image, distroless, nonroot, postgres container, port, loopback, 127.0.0.1, POSTGRES_PORT, volume, deploy, local testing, docker run, network, env-file, DATABASE_URL]
links: [related:command-dev-commands, related:api-env-vars, related:workflow-migrations, related:gotcha-container-log-dir, related:gotcha-console-docker-bind-address]
sources: [Dockerfile:1-28, docker-compose.yml:1-20, scripts/migrate.sh:56-87, scripts/with-env.sh:12-17, README.md:210-232]
updated: 2026-11-26
verified_commit: ec35d99
---
## Facts
- Build stage uses golang:1.26.11 and downloads modules first, so that layer is cached until go.mod or go.sum change → Dockerfile:2-8
- Binary is built with CGO_ENABLED=0 and `-ldflags="-s -w"` from ./cmd/server to /out/scimage → Dockerfile:12-14
- Runtime stage is gcr.io/distroless/static-debian12:nonroot: no shell, no package manager, CA certs included for webhooks and ARIA → Dockerfile:16-19
- The image runs as nonroot with ENTRYPOINT `scimage`, copied to /usr/local/bin → Dockerfile:21-28
- EXPOSE 8080 is documentation only; SCIM_ADDR controls the real bind → Dockerfile:23-25
- No make target builds the image; no compose service runs it → Makefile:12, docker-compose.yml:2-19
- Compose has one service, `postgres` (image postgres:16, restart unless-stopped) → docker-compose.yml:2-4
- Compose takes POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB from the environment with no defaults → docker-compose.yml:6-8
- Host port is `${POSTGRES_PORT:-5432}` bound to 127.0.0.1 only, mapped to container port 5432, so the dev DB is not reachable off-host → docker-compose.yml:9-10
- Compose sets no top-level `name:`, so the default network is `<folder>_default` → docker-compose.yml:1-20
- Data lives in the named volume postgres_data → docker-compose.yml:11-12,19-20
- Healthcheck: pg_isready every 5s, 5 retries → docker-compose.yml:13-17
- Migrations run from the host through scripts/migrate.sh; it uses a host migrate binary if present, else the migrate/migrate image on the compose network → scripts/migrate.sh:69-87
- Local testing (README) runs the image on the compose network `scimage_default` with `--env-file .env`; DATABASE_URL is built from POSTGRES_* values with host `postgres`, port 5432 → README.md:215-218
- The block runs in a subshell `( ... )`, so the variables loaded from .env by `set -a; source .env` do not stay in the terminal → README.md:213-214,221
- `-e CONSOLE_ADDR="0.0.0.0:8090"` with `-p 127.0.0.1:8090:8090` keeps the console loopback-only on the host → README.md:215,219,228
- Console token for the container comes from the host: `make console-token LABEL=...`, since the image has no scimage-admin → README.md:230
## Gotchas
- `--env-file` reads values literally (no quote stripping, no inline comments), unlike scripts/with-env.sh, which loads .env by shell source → README.md:210,217, scripts/with-env.sh:14-16
- `scimage_default` is the Compose network, named after the lowercased project folder plus `_default`; a clone in a differently named folder finds its network with `docker network ls` → README.md:216,226
- If the password contains `/`, `?`, `#`, `%` or a space, percent-encode it in place of `$POSTGRES_PASSWORD`, or choose one without those characters → README.md:218,227
- A webhook URL in .env that points at localhost fails from inside the container → README.md:210
- The compose stack is local-dev only; a production database needs its own credentials and network controls → README.md:232
