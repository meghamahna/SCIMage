# Security Policy

SCIMage is a SCIM 2.0 provisioning server. It handles user identity data and the
credentials that guard it, so security is treated as a first-class property of
the codebase. This document explains how to report a problem and summarizes the
guarantees the server is built to keep.

## Reporting a vulnerability

Please report suspected vulnerabilities privately, not in a public issue.

- Use GitHub's **[private vulnerability reporting](https://github.com/meghamahna/SCIMage/security/advisories/new)**
  (Security → Advisories → *Report a vulnerability*).
- Include the version or commit, a description of the issue, and the steps or a
  proof of concept that reproduces it.

You can expect an acknowledgement within a few days. Once a fix is available it
will land with a note in [CHANGELOG.md](CHANGELOG.md), and the advisory will
credit the reporter unless you ask otherwise.

Because this is a portfolio project, there is no bug bounty. Reports are still
very welcome.

## Supported versions

The project has not yet cut a `1.0.0` release. Until it does, fixes land on the
default branch, and that is the supported target for a report.

| Version | Supported |
| --- | --- |
| `main` (unreleased) | ✅ |

## Security model

These properties are enforced in code and covered by tests.
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) has the mechanisms in full; the
[threat model](docs/THREAT-MODEL.md) walks the trust boundaries and residual
risks.

- **Bearer tokens are issued and tenant-scoped.** A token
  is `scimage_<keyID>_<secret>`; only `sha256(secret)` is stored. Verification
  looks the row up by key id and compares with `crypto/subtle.ConstantTimeCompare`
  against the stored hash, then confirms the token's tenant matches the tenant in
  the URL. A misconfigured or too-short token fails closed.
- **Authentication is applied by the router itself.** Every path is wrapped in
  the token check, so the whole surface is covered structurally, and an
  unregistered path is rejected the same way as a real one.
- **Tenant isolation is structural.** Every store query is scoped by `tenant_id`,
  including lookups by id, so one tenant's token naming another tenant's resource
  gets a `404`.
- **Every mutation is audited in its own transaction.** The audit row (actor,
  action, resource, target id, timestamp, before/after) commits with the change
  or not at all. Refusals are recorded too. Privileged CLI actions write an
  `admin_audit_log` entry the same way.
- **All payloads are validated against the expected schema before they reach the
  database.**
- **Outbound webhooks are signed** with HMAC-SHA256 over the timestamp, delivery
  id, event type and body. A configured endpoint requires a secret at startup, so
  nothing goes out unsigned, and redirects are never followed.
- **Secrets come only from environment variables** (database credentials, the
  bearer/webhook secrets), never hardcoded, never committed. Repository hooks
  block reading `.env`/key files and block commits whose staged diff looks like a
  secret.

### The AI component is advisory only

The only AI in this project is **ARIA** (`cmd/aria`), which reads the audit log
and narrates already-computed activity signals for a human reviewer. It is
strictly advisory: the model never makes or influences an authorization or
provisioning decision, and it has no path into the auth middleware or any
mutating code. A change that would wire model output into a decision is a design
break. Report it as one.

## Operational expectations

SCIMage terminates its trust at the process boundary and assumes the operator
provides the rest of the deployment envelope:

- **TLS terminates in front of the server.** Tokens and user attributes travel in
  request bodies, so run it behind a TLS-terminating proxy or load balancer. Set
  `SCIM_BASE_URL` so generated `Location` links stay `https`.
- **The admin CLI and database are kept off the public network.** `scimage-admin`
  and ARIA reach Postgres directly; that connection is not meant to be exposed.
- **Rate limiting is per authenticated caller.** A pre-authentication limiter is
  deferred; front the service with one at the proxy if unauthenticated request
  volume is a concern in your environment.
