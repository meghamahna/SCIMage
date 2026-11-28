---
id: decision-console-host-allowlist
kind: decision
title: Console answers only to allowlisted Host names
summary: Requests whose Host is not loopback, the CONSOLE_ADDR host or a CONSOLE_ALLOWED_HOSTS entry get 421 before auth, which blocks DNS rebinding; the port is ignored and bad entries fail startup
tags: [decision, console, host header, allowlist, CONSOLE_ALLOWED_HOSTS, CONSOLE_ADDR, DNS rebinding, 421, misdirected, host check, startup validation, why]
links: [decided_by:module-console, related:module-console, related:module-console-auth, related:gotcha-console-docker-bind-address, related:api-env-vars]
sources: [internal/console/host.go:20-27, internal/console/host.go:46-63, internal/console/host.go:79-126, internal/console/host.go:156-172, internal/console/server.go:81-86, internal/console/server.go:121-124, internal/console/server.go:153, docs/CONFIGURATION.md:251-270]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Always allowed: localhost, 127.0.0.1 and ::1 → internal/console/host.go:21
- CONSOLE_ADDR's host is allowed unless it is a wildcard (0.0.0.0, ::) → internal/console/host.go:22-27
- CONSOLE_ALLOWED_HOSTS adds names, comma separated; each entry is parsed by hostEntry → internal/console/host.go:28-41
- Entries are an IP literal or a DNS name, with an optional numeric port that is dropped → internal/console/host.go:46-63
- Wildcard IPs and unspecified addresses are rejected in CONSOLE_ALLOWED_HOSTS → internal/console/host.go:54-58
- DNS names must match the label grammar (letters, digits, hyphens) → internal/console/host.go:46-63, internal/console/host.go:131-150
- CONSOLE_ADDR's host skips that grammar check, so Docker-style names with underscores still work → internal/console/host.go:22-27
- IP literals compare in canonical form, so [0:0:0:0:0:0:0:1] equals ::1; names are lower-cased with the root dot dropped → internal/console/host.go:79-126
- A malformed or refused entry fails NewServer, which stops the console and therefore startup → internal/console/server.go:81-86
- The check runs before auth; a refused Host gets 421 Misdirected Request and never reaches the token lookup → internal/console/host.go:164-172, internal/console/server.go:121-124
- It wraps the whole root handler, so static assets are host-checked too → internal/console/server.go:153
- The port is not compared; the name alone is what rebinding cannot forge → internal/console/host.go:156-163
## Why
- A page on attacker.example re-pointed at 127.0.0.1 still sends Host: attacker.example, so the console refuses it even though the browser treats the response as same-origin → internal/console/host.go:156-163
## Gotchas
- A 0.0.0.0 CONSOLE_ADDR is a bind address, never a Host; browsers send localhost or 127.0.0.1, which are always allowed → internal/console/host.go:22-27, internal/console/host.go:54-58
- A reverse proxy or hostname other than the loopback names needs its name listed in CONSOLE_ALLOWED_HOSTS → docs/CONFIGURATION.md:222
