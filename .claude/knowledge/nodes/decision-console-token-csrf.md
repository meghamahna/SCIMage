---
id: decision-console-token-csrf
kind: decision
title: Console uses bearer/basic tokens and stateless CSRF, not sessions
summary: No login session or cookie; each request presents a console token, and CSRF is a keyed HMAC over an hourly bucket
tags: [decision, session, cookies, csrf, token auth, bearer, basic auth, stateless, why]
links: [decided_by:module-console-auth, related:module-console-auth]
sources: [internal/console/csrf.go:21-31, internal/console/auth.go:38-42, internal/console/server.go:8-12]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Auth uses a token the operator pastes into the browser Basic dialog or sends as Bearer → internal/console/auth.go:38-42
- No cookies: a browser does not attach credentials to a cross-site POST, so CSRF is defence in depth → internal/console/csrf.go:21-28
- CSRF is stateless: an HMAC over a time bucket, verified by any process that holds the key → internal/console/csrf.go:21-27, internal/console/csrf.go:42-53
- Console login tokens are minted only by the CLI, so the console can't mint or revoke its own credential → internal/console/server.go:8-12
