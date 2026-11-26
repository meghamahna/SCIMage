---
id: gotcha-console-docker-bind-address
kind: gotcha
title: Console bind address differs in Docker
summary: Loopback 127.0.0.1 for CONSOLE_ADDR is right on bare metal but unreachable through Docker port publishing
tags: [gotcha, docker, CONSOLE_ADDR, bind, loopback, 0.0.0.0, port publish, console]
links: [related:module-console, related:decision-console-separate-listener]
sources: [README.md:216-221, cmd/server/main.go:36-39]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Docker deployments must bind CONSOLE_ADDR to 0.0.0.0, because port publishing forwards to the container interface → README.md:216-221
- The 127.0.0.1:8090 default applies to bare-metal runs only → cmd/server/main.go:36-39
