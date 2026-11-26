---
id: workflow-precommit-hook
kind: workflow
title: Pre-commit hook and staged secret scan
summary: .githooks/pre-commit, enabled by make hooks-install, blocks commits with staged secrets, unformatted or unvetted Go, or failing tests
tags: [pre-commit, git hook, githooks, hooks-install, secrets, secret scan, gofmt, goimports, go vet, commit gate, no-verify, hooksPath]
links: [related:command-dev-commands, related:workflow-ci, related:gotcha-bare-go-test-skips-postgres]
sources: [.githooks/pre-commit:1-57, scripts/check-staged-secrets.sh:8-86, Makefile:164-168]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Activate once per clone with `make hooks-install`, which sets core.hooksPath to .githooks; git clone does not carry it → Makefile:164-168, .githooks/pre-commit:4
- Runs on every commit from any tool; the .claude hooks only fire for Claude → .githooks/pre-commit:2-3
- First step runs scripts/check-staged-secrets.sh and blocks on any reason → .githooks/pre-commit:11-14
- Secret scan reads only `git diff --cached` → scripts/check-staged-secrets.sh:10
- Blocked: staged .env files except .example, .sample, .template; PEM keys; AWS access key IDs → scripts/check-staged-secrets.sh:27-33,63-72
- Blocked: DSNs with a plaintext password, literal Bearer tokens, hardcoded password/secret/api-key assignments → scripts/check-staged-secrets.sh:73-78
- Placeholder values such as changeme, example and test are allowed as whole values → scripts/check-staged-secrets.sh:35-38
- Docs (.md, .mdx, .txt) and the vendored Swagger UI are skipped for credential checks only; PEM and AWS checks cover every file → scripts/check-staged-secrets.sh:15-25
- Go checks run only when staged .go files exist: gofmt -l, goimports -l if installed, go vet ./..., and `scripts/with-env.sh go test -p 1 ./...` → .githooks/pre-commit:16-45
- If go is missing, the Go checks are skipped with a warning → .githooks/pre-commit:47-49
- Failure message names --no-verify as an emergency bypass; AGENTS.md forbids it → .githooks/pre-commit:52, AGENTS.md:163
