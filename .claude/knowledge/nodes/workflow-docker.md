---
id: workflow-docker
kind: workflow
title: Docker image and compose stack
summary: Multi-stage Dockerfile builds a static nonroot scimage binary; docker-compose runs only Postgres 16 on a configurable host port
tags: [docker, dockerfile, docker compose, compose, container, image, build image, distroless, nonroot, postgres container, port, volume, deploy]
links: [related:command-dev-commands, related:api-env-vars, related:workflow-migrations, related:gotcha-container-log-dir, related:gotcha-console-docker-bind-address]
sources: [Dockerfile:1-28, docker-compose.yml:1-20, scripts/migrate.sh:56-87]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Build stage uses golang:1.26.6 and downloads modules first, so that layer is cached until go.mod or go.sum change → Dockerfile:2-8
- Binary is built with CGO_ENABLED=0 and `-ldflags="-s -w"` from ./cmd/server to /out/scimage → Dockerfile:12-14
- Runtime stage is gcr.io/distroless/static-debian12:nonroot: no shell, no package manager, CA certs included for webhooks and ARIA → Dockerfile:16-19
- The image runs as nonroot with ENTRYPOINT `scimage`, copied to /usr/local/bin → Dockerfile:21-28
- EXPOSE 8080 is documentation only; SCIM_ADDR controls the real bind → Dockerfile:23-25
- No make target builds the image; no compose service runs it → Makefile:12, docker-compose.yml:2-19
- Compose has one service, `postgres` (image postgres:16, restart unless-stopped) → docker-compose.yml:2-4
- Compose takes POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB from the environment with no defaults → docker-compose.yml:6-8
- Host port is `${POSTGRES_PORT:-5432}` mapped to container port 5432 → docker-compose.yml:9-10
- Data lives in the named volume postgres_data → docker-compose.yml:11-12,19-20
- Healthcheck: pg_isready every 5s, 5 retries → docker-compose.yml:13-17
- Migrations run from the host through scripts/migrate.sh; it uses a host migrate binary if present, else the migrate/migrate image on the compose network → scripts/migrate.sh:69-87
