---
id: gotcha-request-body-logging-opt-in
kind: gotcha
title: Request body logging is opt-in and only for authenticated calls
summary: SCIM_LOG_REQUESTS=1 records bodies, query and user agent of authenticated requests only; secret-like values are redacted and oversize or non-JSON bodies are logged by size
tags: [gotcha, logging, request logs, SCIM_LOG_REQUESTS, body, PII, user attributes, redaction, secrets, debug logging]
links: [related:module-logging, related:module-scim, related:component-scim-request-middleware, related:api-env-vars]
sources: [internal/scim/requestlog.go:15-30, internal/scim/requestlog.go:54-87, internal/scim/requestlog.go:98-161, internal/scim/requestlog.go:167-253, internal/scim/handler.go:93-98, docs/CONFIGURATION.md:85-100]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Off unless SCIM_LOG_REQUESTS=1; a warning is logged at startup when on → internal/scim/requestlog.go:54-60
- Body, query and user agent are logged only for authenticated requests; captureBody sits inside requireToken and marks the entry → internal/scim/requestlog.go:98-101, internal/scim/handler.go:98
- Unauthenticated requests get method (cut at 16 bytes), path (cut at 256 bytes) and status only → internal/scim/requestlog.go:27-28, internal/scim/requestlog.go:70-75, internal/scim/requestlog.go:91-96
- The handler reads the real stream: the body is tee'd as it is read, so the count stops where the handler's own limit stops → internal/scim/requestlog.go:102-115
- Decode cap is 64 KiB (maxDecodedBody); logged JSON is cut to 2 KB (maxLoggedBody) → internal/scim/requestlog.go:17-21, internal/scim/requestlog.go:156-159
- Body over the request limit logs body_over_limit with no content; over 64 KiB or not valid JSON logs byte count only → internal/scim/requestlog.go:135-149
- Keys are matched after folding: letters and digits only, lowercased, Unicode case-fold orbit → internal/scim/requestlog.go:205-233
- Secret-like names contain password, passwd, pwd, passphrase, secret, token, apikey, privatekey or credential → internal/scim/requestlog.go:201-203
- PATCH: a secret path redacts every sibling except op and path; a non-string path redacts every sibling except op → internal/scim/requestlog.go:170-190
- Filter text inside [...] in a path is ignored when testing for a secret path → internal/scim/requestlog.go:239-253
- Authorization header is never written to the entry → internal/scim/requestlog.go:70-82
## Gotchas
- Query strings are logged as sent, without redaction; a token in the query is not masked → internal/scim/requestlog.go:79-81
- Entries still carry user attributes; the operator must treat the log directory as sensitive → docs/CONFIGURATION.md:85-100
