---
id: doc-contributing
kind: doc
title: CONTRIBUTING.md
summary: Contributor guide sections: setup commands, house conventions, non-negotiable security principles and how to submit a change
tags: [docs, contributing, contributor guide, setup, onboarding, PR, pull request, conventions, submitting a change, security principles]
links: [related:architecture-overview, related:workflow-precommit-hook, related:command-dev-commands, related:api-env-vars]
sources: [CONTRIBUTING.md:1-94]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Contributor guide intro: correctness, tests and security properties over feature breadth → CONTRIBUTING.md:3-6
- Getting set up: Go and Docker, then copy .env.example, make hooks-install, make up, make test → CONTRIBUTING.md:18-30
- make hooks-install is per clone and runs secret scan, gofmt, go vet and go test on commit → CONTRIBUTING.md:32-34
- Target list is in docs/LOCAL-DEVELOPMENT.md; env vars are in docs/CONFIGURATION.md → CONTRIBUTING.md:36-38
- Conventions: standard library first, pgx raw SQL in internal/store, golang-migrate pairs in migrations/ → CONTRIBUTING.md:40-53
- Formatting is gofmt and goimports via make fmt → CONTRIBUTING.md:55-56
- Non-negotiable security principles (constant-time token compare, audit_log in the change's transaction, payload validation, env-only secrets, ARIA advisory only) → CONTRIBUTING.md:61-76
- Hooks are fixed in .claude/hooks/ rather than worked around → CONTRIBUTING.md:78-80
- Submitting a change: branch off main, small commits, make test green, tests with the change → CONTRIBUTING.md:82-91
- Security-sensitive reports follow SECURITY.md, not a public PR or issue → CONTRIBUTING.md:93-94
