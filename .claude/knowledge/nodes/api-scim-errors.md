---
id: api-scim-errors
kind: api
title: SCIM error responses
summary: Shape of SCIM error bodies, scimType values in use, and which status each failure maps to
tags: [errors, scimType, error response, 400, 401, 404, 409, 413, 429, 500, invalidValue, invalidPath, invalidFilter, uniqueness, mutability, tooMany]
links: [implements:module-scim, related:api-scim-routes, related:gotcha-scim-idp-quirks]
sources: [internal/scim/handler.go:541-605, internal/scim/handler.go:648-660, internal/scim/handler.go:699-732, internal/scim/models.go:119-124]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Every error uses the SCIM Error schema: string status, optional scimType, detail; content type application/scim+json → internal/scim/models.go:119-124, internal/scim/handler.go:707-721
- Body over 1 MiB → 413; malformed JSON → 400 invalidSyntax → internal/scim/handler.go:541-553
- Schema or field validation failure → 400 invalidValue (schemas must hold the User URN, userName required, fields over 256 chars) → internal/scim/handler.go:575-601
- Duplicate userName → 409 uniqueness → internal/scim/handler.go:117-119, internal/scim/handler.go:296-298
- Duplicate displayName → 409 uniqueness; unknown or foreign group member → 400 invalidValue → internal/scim/handler.go:652-655
- PUT id mismatch → 400 mutability → internal/scim/handler.go:188
- User PATCH: unsupported path → 400 invalidPath, other failures → 400 invalidValue → internal/scim/handler.go:269-273
- Group PATCH uses the same invalidPath/invalidValue split → internal/scim/handler.go:428-432
- Unsupported or malformed filter → 400 invalidFilter → internal/scim/handler.go:146-151, internal/scim/handler.go:350-355
- Rate limited → 429 tooMany with Retry-After: 1 → internal/scim/ratelimit.go:80-83
- Missing or bad bearer token → 401 "invalid or missing bearer token" with WWW-Authenticate: Bearer → internal/scim/auth.go:49-52
- Unknown user or group → 404 → internal/scim/handler.go:133-135, internal/scim/handler.go:724-727
- Unknown path → 404 "unknown resource"; wrong verb → 405 → internal/scim/handler.go:699-705
- Any unexpected error → 500 "internal server error"; cause is logged only → internal/scim/handler.go:729-732
