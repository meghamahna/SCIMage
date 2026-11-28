---
id: module-console-auth
kind: module
title: Console auth and CSRF
summary: Console login is a console_tokens credential sent as Bearer or Basic; the Host check runs first, then POSTs pass an origin check and a stateless per-process HMAC form token bound to the operator's key id
tags: [auth, authentication, login, console token, bearer, basic auth, csrf, origin check, sec-fetch-site, session, hmac, constant time, host check]
links: [depends_on:module-store, related:module-console, decided_by:decision-console-token-csrf, decided_by:decision-console-host-allowlist, documented_in:doc-architecture]
sources: [internal/console/auth.go:1-126, internal/console/csrf.go:1-128, internal/console/handlers.go:868-896, internal/console/server.go:121-124, docs/ARCHITECTURE.md:475-489]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- No session store and no cookies. Each request carries a console token → internal/console/auth.go:38-42
- Token accepted as Bearer header, or as HTTP Basic password or username when it starts with the console prefix → internal/console/auth.go:102-121
- Checks: lookup by key id, reject if revoked or expired, then SHA-256 of secret compared with ConstantTimeCompare → internal/console/auth.go:76-97
- Every rejection returns the same 401 with Basic realm "SCIMage Console" so key ids can't be probed → internal/console/auth.go:44-52
- Authenticated identity (KeyID, Label) is placed in request context → internal/console/auth.go:24-36, internal/console/auth.go:63-64
- The Host allowlist runs before this middleware, so a refused Host never reaches the token lookup → internal/console/server.go:121-124, internal/console/host.go:164-172
- Protected routes are wrapped by requireConsoleToken; only /console/static/ mounts before auth → internal/console/server.go:148-151
- Browsers re-send Basic credentials on every console request, so a valid login alone does not prove a POST was intended → docs/ARCHITECTURE.md:480-481
- checkCSRF order: origin check first, then the body cap and ParseForm, then the token check → internal/console/handlers.go:868-896
- Origin check (sameOrigin): Sec-Fetch-Site must be exactly same-origin when present → internal/console/csrf.go:97-98
- Without Sec-Fetch-Site, Origin must parse as scheme://host[:port] and its host must equal r.Host (case-folded); scheme is not compared → internal/console/csrf.go:100-113
- Requests with neither header are accepted; only a non-browser client can send them, and the token still applies → internal/console/csrf.go:100-103, docs/ARCHITECTURE.md:483-484
- Refused origin checks are logged with key id, path and clipped headers → internal/console/handlers.go:872-878
- Form token = "<hour-bucket>.<hex HMAC-SHA256>" over the bucket and the operator's console key id, using a random 32-byte per-process key → internal/console/csrf.go:44-62
- An empty key id always fails the token check → internal/console/csrf.go:64-68
- Valid for the current or immediately previous hour bucket, compared in constant time → internal/console/csrf.go:64-94, docs/ARCHITECTURE.md:486-487
- Forms send the token in the "csrf" field; refused tokens are logged and return 403 → internal/console/handlers.go:890-894
- POST bodies are capped at 64 KiB before parsing, so an oversize form never reaches the token check → internal/console/handlers.go:880-886
## Gotchas
- Reverse proxies in front of the console must forward the original Host header, or same-origin POSTs are refused → internal/console/csrf.go:100-113
- CSRF key is random per process and does not survive a restart, so forms rendered before a restart are rejected → internal/console/csrf.go:35-41, docs/ARCHITECTURE.md:485-489
- CSRF window is one hour and the previous bucket is also accepted, so a form stays valid between 1 and 2 hours after rendering → internal/console/csrf.go:18-21, internal/console/csrf.go:64-94, docs/ARCHITECTURE.md:486-487
- Console tokens are issued and revoked only via the CLI, never through the console → internal/console/server.go:8-12, docs/ARCHITECTURE.md:475-478
