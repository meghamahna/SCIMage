# SCIM 2.0 Provisioning Server: Project Guide

## What this is
A SCIM 2.0 server in Go implementing the `/Users` resource per RFC
7644, backed by Postgres in Docker. This is a portfolio project built
to demonstrate real backend engineering skill for a Go/identity role.
Correctness, testing, and security practices all matter more than
feature breadth.

Full build plan lives in `docs/IMPLEMENTATION_PLAN.md`. Work through it in
phase order. When a phase is complete, check its boxes off in that
file before moving to the next phase.

## Stack and conventions
- Go, standard library `net/http` for the HTTP layer, no web
  framework
- Postgres via `pgx`, raw SQL, no ORM. Write queries directly in
  `internal/store`
- `golang-migrate` for schema migrations, files live in `/migrations`
- Errors: wrap with `fmt.Errorf("...: %w", err)`, never swallow an
  error silently
- Tests: table-driven where it fits, `httptest` for HTTP handlers,
  real Postgres (via docker-compose) for store integration tests,
  no mocking the database. Run them with `make test`, which loads `.env`
  and passes `-p 1`; both test packages share the `users` table
- Dependencies stay minimal: `pgx` for Postgres and
  `golang.org/x/time/rate` for the token bucket. Reach for the standard
  library first
- Formatting: `gofmt` / `goimports` before every commit

## Project structure
```text
/cmd/server           entrypoint for the SCIM server
/cmd/scimage-admin    tenant and token administration (Phase 10)
/cmd/scimtrace        AI-assisted audit reviewer (Phase 12)
/internal/scim        HTTP handlers, SCIM models, auth, rate limiting
/internal/store       Postgres-backed store and audit log, raw SQL
/migrations           SQL migration files
/scripts              env loading, migrations, secret scanning
/.github/workflows    CI
docker-compose.yml
```

## Security design principles (non-negotiable)
- Bearer token comparison must use `crypto/subtle.ConstantTimeCompare`,
  never `==`
- Every mutating call (create/replace/deactivate) writes an `audit_log`
  row (actor, action, target user id, timestamp, before/after state)
  **inside the transaction that makes the change**, so the entry and the
  change commit together. The store owns that write, which keeps a
  handler from being able to skip it. Refusals are recorded too
- All incoming SCIM payloads are validated against the expected schema
  before touching the database
- Secrets (DB credentials, bearer token) come from environment
  variables only, never hardcoded, never committed
- These rules are enforced mechanically, not just documented: hooks in
  `.claude/hooks/` block reading `.env`/key files, block writing
  hardcoded credentials, and block `git commit` when the staged diff
  looks like it contains a secret. If a hook blocks something that's
  actually fine, fix the hook in `.claude/hooks/` rather than working
  around it.

## Keep the focus on the project, not the harness
The hooks, scripts, and `.claude/` config are scaffolding, not the
work. When one gets in the way, make the smallest fix that unblocks
the task, confirm it didn't weaken the check, and get back to the
phase. If it needs more than that, say so and ask. Raise harness-only
review nits as a note rather than acting on them.

## Comments and docs
Keep them short. Explain *why* where it isn't obvious: a schema
choice, a spec requirement, a non-obvious failure mode. Don't narrate
what the code already says, and don't write a paragraph where a line
does.

## AI usage in this project
The only AI component is `cmd/scimtrace`: **SCIMTrace AI**. It reads
the `audit_log` table and produces a plain-English summary of patterns
worth a human's attention (bulk deactivations, off-hours changes,
unusual call volume from a caller).
The name is deliberate: it traces and reports on activity, it doesn't decide anything.

This is advisory only. **The AI must never be given the ability to
make or influence an authorization or provisioning decision.** If a
future task suggests wiring the LLM output into anything that creates,
updates, or deactivates a user, or into the auth middleware, stop and
flag it rather than implementing it. That would break the core
security design of this project.

## Commit style
Small, focused commits. Message format: `phase-N: short description`,
e.g. `phase-3: add user store create/get/update/deactivate`.

Before proposing any `git commit`, run the `code-reviewer` subagent
(`.claude/agents/code-reviewer.md`) against the staged diff. If it
returns `DO NOT COMMIT`, fix the flagged items first. Don't commit
around them and don't skip the review because the diff "looks small."
This is a review gate, not a formatting pass: `.claude/hooks/` and
`.githooks/pre-commit` already catch secrets/formatting/`go vet`/tests
mechanically; the subagent is for correctness and security-principle
judgment those scripts can't make. `git commit` also always prompts
for your approval regardless; the subagent's job is to make sure
that approval request is a good one.

## When starting a session
1. Read `docs/IMPLEMENTATION_PLAN.md` to see which phase is next
   (unchecked boxes)
2. Confirm `make up` is running before working on anything that
   touches the store
3. Run `make test` before considering a phase done
4. One-time per clone: `make hooks-install`. This activates the real
   git pre-commit hook (secrets scan, `gofmt`, `go vet`, `go test`).
   It sets a local git config, so it doesn't travel with the repo
   automatically and has to be run again on every fresh clone.
