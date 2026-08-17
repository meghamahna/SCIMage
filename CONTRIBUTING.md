# Contributing to SCIMage

Thanks for your interest. SCIMage is a focused SCIM 2.0 server, and the bar for a
change is correctness, tests, and keeping the security properties intact, more
than feature breadth. This guide covers how to get set up and what a good change
looks like.

## Getting set up

You need Go (see [`go.mod`](go.mod) for the version) and Docker for the Postgres
the integration tests run against.

```bash
git clone https://github.com/meghamahna/SCIMage
cd SCIMage
cp .env.example .env      # fill in the blanks; never commit .env
make hooks-install        # once per clone: activates the pre-commit hook
make up                   # starts Postgres and applies migrations
make test                 # runs the full suite against that Postgres
```

`make hooks-install` sets a local git config, so it doesn't travel with the repo
and has to be run again on every fresh clone. The hook runs a secret scan,
`gofmt`, `go vet` and `go test` before a commit is allowed.

[docs/LOCAL-DEVELOPMENT.md](docs/LOCAL-DEVELOPMENT.md) lists every `make` target;
[docs/CONFIGURATION.md](docs/CONFIGURATION.md) documents every environment
variable.

## Conventions

The full house style lives in [.claude/CLAUDE.md](.claude/CLAUDE.md). The parts
that matter most:

- **Standard library first.** `net/http` for HTTP, no web framework. `pgx` with
  raw SQL for Postgres, no ORM. Queries are written directly in
  `internal/store`. Dependencies stay minimal.
- **Migrations** are `golang-migrate` files in [`migrations/`](migrations/), added
  in pairs (`.up.sql` / `.down.sql`) with the next sequence number.
- **Errors** are wrapped with `fmt.Errorf("...: %w", err)`, never swallowed.
- **Tests** are table-driven where it fits, use `httptest` for handlers, and run
  against real Postgres for store integration tests.
  Every query is scoped by `tenant_id`, which is what lets the suites run
  concurrently without racing on shared tables.
- **Formatting** is `gofmt` / `goimports` before every commit. The hook enforces
  it.
- **Comments** explain *why* where it isn't obvious: a spec requirement, a schema
  choice, a non-obvious failure mode. They don't narrate what the code already
  says.

## The security principles are non-negotiable

Some invariants are load-bearing and enforced mechanically. A change must not
weaken them, and the hooks will fight you if it tries:

- Bearer-token comparison uses `crypto/subtle.ConstantTimeCompare`, never `==`.
- Every mutating call writes an `audit_log` row **inside the transaction that
  makes the change**. The store owns that write so a handler can't skip it.
- All incoming payloads are validated against the expected schema before they
  touch the database.
- Secrets come from environment variables only, never hardcoded, never
  committed.
- **ARIA stays advisory.** The LLM must never be given the ability to make or
  influence an authorization or provisioning decision. A change that wires model
  output into anything that creates, updates or deactivates a user, or into the
  auth middleware, breaks the core design. Flag it rather than build it.

If a hook blocks something that is genuinely fine, fix the hook in
[.claude/hooks/](.claude/hooks/) rather than working around it, and say so in the
PR.

## Submitting a change

1. Branch off `main`.
2. Keep commits small and focused. Message format is `phase-N: short description`
   for phased work, or a plain imperative summary otherwise.
3. Run `make test` and make sure it's green before you push.
4. Open a PR that says what changed and why. If it touches a security property,
   call that out explicitly.
5. Add or update tests alongside the change: a bug fix comes with the test that
   would have caught it.

Security-sensitive reports should follow [SECURITY.md](SECURITY.md) rather than a
public PR or issue.
