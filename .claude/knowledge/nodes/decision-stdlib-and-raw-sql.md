---
id: decision-stdlib-and-raw-sql
kind: decision
title: Standard library HTTP and raw SQL, with a minimal dependency set
summary: net/http with no framework, pgx with hand-written SQL and no ORM, and only pgx plus x/time as external modules, so each behaviour stays visible in code
tags: [decision, stdlib, net/http, pgx, sql, orm, dependencies, why, minimal, framework, golang-migrate]
links: [related:architecture-overview, related:module-store, related:module-scim, related:decision-real-postgres-tests, documented_in:doc-architecture]
sources: [docs/ARCHITECTURE.md:55-56, docs/ARCHITECTURE.md:513-514, scripts/migrate.sh:1-2]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Decision
- HTTP layer uses Go standard library net/http; no web framework → docs/ARCHITECTURE.md:55-56
- Postgres access uses pgx with raw SQL; no ORM → docs/ARCHITECTURE.md:513-514
- Only pgx and golang.org/x/time are kept as external modules → docs/ARCHITECTURE.md:513-514
- Schema changes are applied by golang-migrate through scripts/migrate.sh → scripts/migrate.sh:1-2
## Why
- Explicit SQL and plain routing make behaviour visible; Postgres stays the single source of truth → docs/ARCHITECTURE.md:55-56
- Fewer dependencies means a smaller supply-chain and review surface for a security product → docs/ARCHITECTURE.md:513-514
