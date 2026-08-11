---
name: phase-check
description: Verify a SCIM server implementation phase is actually done — tests pass, formatting is clean, and the phase's security principles are satisfied — before checking it off in docs/IMPLEMENTATION_PLAN.md. Use when the user says a phase is done, asks to check off docs/IMPLEMENTATION_PLAN.md boxes, or asks what's left before committing.
---

# Phase completion check

This project's discipline (per CLAUDE.md) is: don't check a box in
`docs/IMPLEMENTATION_PLAN.md` until it's actually true. This skill is the
gate — run every applicable step below before ticking anything off or
suggesting a `phase-N:` commit.

## Steps

1. **Locate the phase.** Read `docs/IMPLEMENTATION_PLAN.md`. Find the
   lowest-numbered phase with any unchecked `- [ ]` box — that's the
   one under review. List its specific checklist items.

2. **Tests.** Run `make test` — it loads `.env` and passes `-p 1`, both
   of which the suite needs. A bare `go test ./...` skips every
   integration test and still exits 0, so it proves nothing. Confirm
   Postgres is up first (`docker compose ps`) — integration tests must
   hit the real database per CLAUDE.md, never a mock. A failing or
   skipped test means the phase is not done.

3. **Formatting.** Run `gofmt -l .` and, if installed, `goimports -l .`.
   Both must print nothing. If either lists files, run `gofmt -w` /
   `goimports -w` on them before continuing — do not check off the
   phase with unformatted files.

4. **Security principles relevant to this phase** (CLAUDE.md,
   "Security design principles" — non-negotiable, don't skip based on
   phase number alone; re-check if the diff touches these areas):
   - Auth/token comparison code uses `crypto/subtle.ConstantTimeCompare`,
     never `==`. Grep for the token comparison and confirm.
   - Every create/replace/deactivate path writes an `audit_log` row
     (actor, action, target user id, timestamp, before/after state)
     inside the same transaction as the change, so the two commit
     together. The store owns that write — confirm a new mutating path
     goes through it rather than committing and recording separately.
   - Incoming SCIM payloads are validated against the expected schema
     before any DB call in that code path.
   - No secret is hardcoded or read from a file — only `os.Getenv` /
     env vars. (The write/commit hooks in `.claude/hooks/` also
     enforce this mechanically, but check the diff by eye too.)

5. **Vulnerability scan** (once Phase 7 lands, or anytime `go.sum`
   changed): `govulncheck ./...` if installed.

6. **Code review.** Run the `code-reviewer` subagent against the
   diff for this phase. A `DO NOT COMMIT` verdict means the phase
   isn't done, full stop — go back and fix what it flagged.

7. **Report, don't rubber-stamp.** If every applicable step above
   passed, check off the completed boxes in `docs/IMPLEMENTATION_PLAN.md`
   and propose a commit message in the `phase-N: short description`
   format. If anything failed, list exactly what's missing and do NOT
   check any boxes — a half-passing phase stays unchecked.
