---
id: decision-real-postgres-tests
kind: decision
title: Tests run against real Postgres, never a mock
summary: Store and handler integration tests hit the compose or CI Postgres; isolation comes from per-test tenants, not from mocks or serial packages
tags: [decision, testing, real database, no mocks, postgres, tenant isolation, integration test, ci, make test, -p 1]
links: [related:workflow-ci, related:command-dev-commands, related:gotcha-bare-go-test-skips-postgres, related:data-model-tenants-tokens]
sources: [Makefile:86-93, .github/workflows/ci.yml:12-14, .github/workflows/ci.yml:65-67, .githooks/pre-commit:40-41]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Decision: the database is never mocked; tests need the compose or CI Postgres → Makefile:86-88, .github/workflows/ci.yml:12-14
- Why: row-count assertions must hit real constraints and queries, not a fake → Makefile:86-88
- Each store test gets its own tenant, and scim shares one tenant per file, so both packages can run against one Postgres → Makefile:89-93
- Why -p 1 anyway: both packages write the users table, so packages run one at a time in CI and pre-commit → .github/workflows/ci.yml:65-67, .githooks/pre-commit:40-42
