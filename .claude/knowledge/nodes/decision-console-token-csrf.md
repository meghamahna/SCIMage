---
id: decision-console-token-csrf
kind: decision
title: Console uses bearer/basic tokens and origin-checked, operator-bound CSRF, not sessions
summary: No login session or cookie; each request presents a console token, and each POST passes an origin check plus a keyed HMAC form token bound to the operator's key id
tags: [decision, session, cookies, csrf, token auth, bearer, basic auth, stateless, origin check, why]
links: [decided_by:module-console-auth, related:module-console-auth, related:decision-console-host-allowlist]
sources: [internal/console/csrf.go:18-30, internal/console/csrf.go:44-62, internal/console/auth.go:38-42, internal/console/server.go:8-12, internal/console/server.go:174, internal/console/handlers.go:868-896]
updated: 2026-11-27
verified_commit: 7f7fe4d
---
## Facts
- Auth uses a token the operator pastes into the browser Basic dialog or sends as Bearer → internal/console/auth.go:38-42
- Browsers re-send Basic credentials on cross-site POSTs too, so authentication alone does not show the operator meant the request → internal/console/csrf.go:21-25
- Two checks cover that: an origin check (sameOrigin, run first) and a form token bound to the operator's key id → internal/console/csrf.go:25-30, internal/console/handlers.go:868-896
- Token = HMAC over a fixed 8-byte bucket then the key id, so a token works only for the operator it was rendered for → internal/console/csrf.go:44-62
- CSRF is stateless: any process holding the key can verify it → internal/console/csrf.go:64-94
- Referrer-Policy is same-origin so a same-origin POST sends a real Origin the check can compare → internal/console/server.go:174
- Console login tokens are minted only by the CLI, so the console can't mint or revoke its own credential → internal/console/server.go:8-12
## Why
- Chosen to keep the scheme sessionless; form tokens expire after an hour, which is accepted as the trade-off → internal/console/csrf.go:18-20
