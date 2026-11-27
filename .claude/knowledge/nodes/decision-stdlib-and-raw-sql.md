---
id: decision-stdlib-and-raw-sql
kind: decision
title: Standard library HTTP and raw SQL, with a minimal dependency set
summary: net/http with no framework, pgx with hand-written SQL and no ORM, and only pgx plus x/time as external modules, so each behaviour stays visible in code
tags: [decision, stdlib, net/http, pgx, sql, orm, dependencies, why, minimal, framework]
links: [related:architecture-overview, related:module-store, related:module-scim, related:decision-real-postgres-tests]
sources: [docs/ARCHITECTURE.md:44-46, docs/ARCHITECTURE.md:55-56, docs/IMPLEMENTATION_PLAN.md:11-19]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Decision
- HTTP layer uses Go standard library net/http; no web framework → docs/IMPLEMENTATION_PLAN.md:16
- Postgres access uses pgx with raw SQL; no ORM → docs/IMPLEMENTATION_PLAN.md:11-12, docs/IMPLEMENTATION_PLAN.md:18
- Only pgx and golang.org/x/time are kept as external modules → docs/ARCHITECTURE.md:55-56
- Schema changes go through golang-migrate → docs/IMPLEMENTATION_PLAN.md:19
## Why
- Explicit SQL and plain routing make behaviour visible; Postgres stays the single source of truth → docs/ARCHITECTURE.md:44-46
- Fewer dependencies means a smaller supply-chain and review surface for a security product → docs/ARCHITECTURE.md:55-56
