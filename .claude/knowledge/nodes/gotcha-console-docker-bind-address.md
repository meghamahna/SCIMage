---
id: gotcha-console-docker-bind-address
kind: gotcha
title: Console bind address differs in Docker
summary: Loopback 127.0.0.1 for CONSOLE_ADDR is right on bare metal but unreachable through Docker port publishing; Docker runs use 0.0.0.0 inside the container
tags: [gotcha, docker, CONSOLE_ADDR, bind, loopback, 0.0.0.0, port publish, console, local testing]
links: [related:module-console, related:decision-console-separate-listener, related:workflow-docker]
sources: [README.md:213-228, cmd/server/main.go:36-39]
updated: 2026-11-26
verified_commit: ec35d99
---
## Facts
- Docker deployments bind CONSOLE_ADDR to 0.0.0.0 inside the container, because port publishing forwards to the container's network interface, so a console bound to the container's 127.0.0.1 is unreachable → README.md:228
- The local-testing command sets `-e CONSOLE_ADDR="0.0.0.0:8090"` (line 219) and publishes `-p 127.0.0.1:8090:8090` (line 215), so the host keeps the loopback restriction → README.md:215,219
- The 127.0.0.1:8090 recommendation applies to bare-metal runs only → cmd/server/main.go:36-39
