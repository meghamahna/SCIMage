# SCIM 2.0 Provisioning Server — Project Guide

## What this is
A SCIM 2.0 server in Go implementing the `/Users` resource per RFC
7644, backed by Postgres in Docker. This is a portfolio project built
to demonstrate real backend engineering skill for a Go/identity role —
correctness, testing, and security practices all matter more than
feature breadth.

Full build plan lives in `IMPLEMENTATION_PLAN.md`. Work through it in
phase order. When a phase is complete, check its boxes off in that
file before moving to the next phase.

## Stack and conventions
- Go, standard library `net/http` for the HTTP layer — no web
  framework
- Postgres via `pgx`, raw SQL — no ORM. Write queries directly in
  `internal/store`
- `golang-migrate` for schema migrations, files live in `/migrations`
- Errors: wrap with `fmt.Errorf("...: %w", err)`, never swallow an
  error silently
- Tests: table-driven where it fits, `httptest` for HTTP handlers,
  real Postgres (via docker-compose) for store integration tests —
  no mocking the database
- Formatting: `gofmt` / `goimports` before every commit

## Project structure
```
/cmd/server            entrypoint for the SCIM server
/cmd/sage        entrypoint for the AI-assisted audit reviewer
/internal/scim          HTTP handlers, SCIM request/response models
/internal/store          Postgres-backed user store, raw SQL
/migrations             SQL migration files
docker-compose.yml
```

## Security design principles (non-negotiable)
- Bearer token comparison must use `crypto/subtle.ConstantTimeCompare`,
  never `==`
- Every mutating call (create/update/deactivate) writes a structured
  audit log entry: actor, action, target user id, timestamp,
  before/after state
- All incoming SCIM payloads are validated against the expected schema
  before touching the database
- Secrets (DB credentials, bearer token) come from environment
  variables only — never hardcoded, never committed
- These rules are enforced mechanically, not just documented: hooks in
  `.claude/hooks/` block reading `.env`/key files, block writing
  hardcoded credentials, and block `git commit` when the staged diff
  looks like it contains a secret. If a hook blocks something that's
  actually fine, fix the hook in `.claude/hooks/` rather than working
  around it.

## AI usage in this project
The only AI component is `cmd/sage` — **SAGE: SCIM Audit & Governance
Engine**. It reads the structured audit log and produces a
plain-English summary of patterns worth a human's attention (bulk
deactivations, off-hours changes, unusual call volume from a token).
The name is deliberate: a sage advises, it doesn't decide.

This is advisory only. **The AI must never be given the ability to
make or influence an authorization or provisioning decision.** If a
future task suggests wiring the LLM output into anything that creates,
updates, or deactivates a user, or into the auth middleware, stop and
flag it rather than implementing it — that would break the core
security design of this project.

## Commit style
Small, focused commits. Message format: `phase-N: short description`,
e.g. `phase-3: add user store create/get/update/deactivate`.

Before proposing any `git commit`, run the `code-reviewer` subagent
(`.claude/agents/code-reviewer.md`) against the staged diff. If it
returns `DO NOT COMMIT`, fix the flagged items first — don't commit
around them and don't skip the review because the diff "looks small."
This is a review gate, not a formatting pass: `.claude/hooks/` and
`.githooks/pre-commit` already catch secrets/formatting/`go vet`/tests
mechanically; the subagent is for correctness and security-principle
judgment those scripts can't make. `git commit` also always prompts
for your approval regardless — the subagent's job is to make sure
that approval request is a good one.

## When starting a session
1. Read `IMPLEMENTATION_PLAN.md` to see which phase is next
   (unchecked boxes)
2. Confirm `docker compose up -d` is running before working on
   anything that touches the store
3. Run `go test ./...` before considering a phase done
4. One-time per clone: `git config core.hooksPath .githooks` — this
   activates the real git pre-commit hook (secrets scan, `gofmt`,
   `go vet`, `go test`). It's a local git config, so it doesn't travel
   with the repo automatically.
