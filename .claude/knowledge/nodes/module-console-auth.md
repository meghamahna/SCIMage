---
id: module-console-auth
kind: module
title: Console auth and CSRF
summary: Console login is a console_tokens credential sent as Bearer or Basic; CSRF is a stateless per-process HMAC over an hourly bucket
tags: [auth, authentication, login, console token, bearer, basic auth, csrf, session, hmac, constant time]
links: [depends_on:module-store, related:module-console, decided_by:decision-console-token-csrf]
sources: [internal/console/auth.go:1-126, internal/console/csrf.go:1-80, internal/console/handlers.go:851-863]
updated: 2026-11-26
verified_commit: a99d9f9
---
## Facts
- No session store and no cookies. Each request carries a console token → internal/console/auth.go:38-42
- Token accepted as Bearer header, or as HTTP Basic password or username when it starts with the console prefix → internal/console/auth.go:102-121
- Console prefix constant mirrors the store's, duplicated because the store's is unexported → internal/console/auth.go:123-126
- Checks: lookup by key id, reject if revoked or expired, then SHA-256 of secret compared with ConstantTimeCompare → internal/console/auth.go:76-97
- Every rejection returns the same 401 with Basic realm "SCIMage Console" so key ids can't be probed → internal/console/auth.go:44-52
- Store lookup errors other than not-found are logged; the caller still sees 401 → internal/console/auth.go:76-85
- last_used is touched best-effort on success; a failure logs and does not reject → internal/console/auth.go:56-64
- Authenticated identity (KeyID, Label) is placed in request context → internal/console/auth.go:24-36, internal/console/auth.go:63-64
- Protected routes are wrapped by requireConsoleToken; only /console/static/ mounts before auth → internal/console/server.go:137-142
- CSRF token = "<hour-bucket>.<hex HMAC-SHA256>" over a random 32-byte per-process key → internal/console/csrf.go:16-53
- Validity: current or immediately previous hour bucket, compared in constant time → internal/console/csrf.go:55-76
- Forms send the token in the "csrf" field, checked by checkCSRF (403 on failure) → internal/console/handlers.go:851-863
- Console tokens are issued and revoked only via the CLI, never through the console → internal/console/server.go:8-12
## Gotchas
- CSRF key is random per process, so forms rendered before a restart are rejected → internal/console/csrf.go:23-26
- CSRF window is one hour and the previous bucket is also accepted, so a form stays valid between 1 and 2 hours after rendering, then must reload → internal/console/csrf.go:16-19, internal/console/csrf.go:68-71
