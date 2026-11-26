# Knowledge graph examples

## Good node: decision
```markdown
---
id: decision-postgres-over-mongo
kind: decision
title: Postgres over MongoDB
summary: Relational store chosen for transactional order/invoice integrity
tags: [database, postgres, adr]
links: [documented_in:doc-adr-0002, related:data-model-order]
sources: [docs/adr/0002-database.md:1-60, src/db/session.py:1-30]
updated: 2026-10-24
verified_commit: 3f9c2ab
---
## Decision
- Postgres 16 with SQLAlchemy 2.x → src/db/session.py:5-18
## Why
- Orders and invoices need multi-row transactions → docs/adr/0002-database.md:12-25
## Consequences
- Migrations via Alembic; never edit applied migrations → migrations/README.md:1-20
```

## Good node: command
```markdown
---
id: command-test
kind: command
title: Run tests
summary: Unit tests with pytest; integration tests need docker compose up
tags: [test, pytest, ci]
links: [related:workflow-ci]
sources: [pyproject.toml:40-62, .github/workflows/ci.yml:20-48]
updated: 2026-10-24
verified_commit: 3f9c2ab
---
## Facts
- `uv run pytest -q` runs unit tests → pyproject.toml:40-52
- Integration tests are marked `@pytest.mark.integration` and skipped by default → pyproject.toml:55-62
```

## Bad node content (don't write)
- "Currently refactoring the auth module; half done." This is task state; it belongs in a handoff.
- "DB password is ..." This is a secret, and the guard will block it anyway.
- A 200-line dump of a file. Use pointers, not copies.
- "utils.py has helper functions." This says nothing useful. Name the non-obvious helpers that matter and why.

## Typical queries
```bash
python3 .claude/tools/kg.py query auth token refresh
python3 .claude/tools/kg.py neighbors module-auth-jwt --depth 2
python3 .claude/tools/kg.py stale
python3 .claude/tools/kg.py stats
```

## Bootstrapping order (for /kg-bootstrap)
1. One `architecture-overview` node.
2. One `command-*` node per workflow (build, test, lint, run, deploy).
3. `entrypoint-*` nodes.
4. `module-*` nodes, one per top-level package or service.
5. `api-*` and `data-model-*` nodes.
6. `decision-*` nodes, from ADRs or commit history.
7. `gotcha-*` nodes.
