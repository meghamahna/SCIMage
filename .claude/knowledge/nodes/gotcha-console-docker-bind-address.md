---
id: gotcha-console-docker-bind-address
kind: gotcha
title: Console bind address differs in Docker
summary: Loopback 127.0.0.1 for CONSOLE_ADDR is right on bare metal but unreachable through Docker port publishing; Docker runs use 0.0.0.0 inside the container, which the Host allowlist still accepts via localhost
tags: [gotcha, docker, CONSOLE_ADDR, bind, loopback, 0.0.0.0, port publish, console, local testing, host allowlist]
links: [related:module-console, related:decision-console-separate-listener, related:decision-console-host-allowlist, related:workflow-docker]
sources: [README.md:218, README.md:222, README.md:231, cmd/server/main.go:41-44, internal/console/host.go:22-27]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Docker deployments bind CONSOLE_ADDR to 0.0.0.0 inside the container, because port publishing forwards to the container's network interface, so a console bound to the container's 127.0.0.1 is unreachable → README.md:231
- The local-testing command sets `-e CONSOLE_ADDR="0.0.0.0:8090"` (line 222) and publishes `-p 127.0.0.1:8090:8090` (line 218), so the host keeps the loopback restriction → README.md:218, README.md:222
- The 127.0.0.1:8090 recommendation applies to bare-metal runs only → cmd/server/main.go:41-44
- The 0.0.0.0 bind is not a Host name; the browser sends localhost or 127.0.0.1, which the Host allowlist always accepts → internal/console/host.go:21, internal/console/host.go:22-27
