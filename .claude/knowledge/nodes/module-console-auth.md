---
id: module-console-auth
kind: module
title: Console auth and CSRF
summary: Console login is a console_tokens credential sent as Bearer or Basic; POSTs pass an origin check, then a stateless per-process HMAC form token bound to the operator's key id
tags: [auth, authentication, login, console token, bearer, basic auth, csrf, origin check, sec-fetch-site, session, hmac, constant time]
links: [depends_on:module-store, related:module-console, decided_by:decision-console-token-csrf]
sources: [internal/console/auth.go:1-126, internal/console/csrf.go:1-128, internal/console/handlers.go:851-870]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- No session store and no cookies. Each request carries a console token → internal/console/auth.go:38-42
- Token accepted as Bearer header, or as HTTP Basic password or username when it starts with the console prefix → internal/console/auth.go:102-121
- Checks: lookup by key id, reject if revoked or expired, then SHA-256 of secret compared with ConstantTimeCompare → internal/console/auth.go:76-97
- Every rejection returns the same 401 with Basic realm "SCIMage Console" so key ids can't be probed → internal/console/auth.go:44-52
- Authenticated identity (KeyID, Label) is placed in request context → internal/console/auth.go:24-36, internal/console/auth.go:63-64
- Protected routes are wrapped by requireConsoleToken; only /console/static/ mounts before auth → internal/console/server.go:114-143
- checkCSRF order: origin check first, then ParseForm, then token check → internal/console/handlers.go:851-870
- Origin check (sameOrigin): Sec-Fetch-Site must be exactly same-origin when present → internal/console/csrf.go:97-98
- Without Sec-Fetch-Site, Origin must parse as scheme://host[:port] and its host must equal r.Host (case-folded); scheme is not compared → internal/console/csrf.go:100-113
- Requests with neither header are accepted; only a non-browser client can send them, and the token still applies → internal/console/csrf.go:100-103
- Refused origin checks are logged with key id, path and clipped headers → internal/console/handlers.go:855-861
- Form token = "<hour-bucket>.<hex HMAC-SHA256>" over the bucket and the operator's console key id, using a random 32-byte per-process key → internal/console/csrf.go:44-62
- An empty key id always fails the token check → internal/console/csrf.go:64-68
- Valid for the current or immediately previous hour bucket, compared in constant time → internal/console/csrf.go:64-94
- Forms send the token in the "csrf" field; refused tokens are logged and return 403 → internal/console/handlers.go:867-870
## Gotchas
- Reverse proxies in front of the console must forward the original Host header, or same-origin POSTs are refused → internal/console/csrf.go:100-113
- CSRF key is random per process, so forms rendered before a restart are rejected → internal/console/csrf.go:35-41
- CSRF window is one hour and the previous bucket is also accepted, so a form stays valid between 1 and 2 hours after rendering → internal/console/csrf.go:18-21, internal/console/csrf.go:64-94
- Console tokens are issued and revoked only via the CLI, never through the console → internal/console/server.go:8-12
