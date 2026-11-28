---
id: api-scim-errors
kind: api
title: SCIM error responses
summary: Shape of SCIM error bodies, scimType values in use, and which status each failure maps to
tags: [errors, scimType, error response, 400, 401, 404, 405, 409, 413, 429, 500, invalidValue, invalidPath, invalidFilter, invalidSyntax, uniqueness, mutability, tooMany, externalId, duplicate, 256]
links: [implements:module-scim, related:api-scim-routes, related:module-apidocs, related:gotcha-scim-idp-quirks, related:gotcha-refused-mutation-audit-best-effort]
sources: [internal/scim/handler.go:101-131, internal/scim/handler.go:181-216, internal/scim/handler.go:232-246, internal/scim/handler.go:290-300, internal/scim/handler.go:576-612, internal/scim/handler.go:642-696, internal/scim/handler.go:707-790, internal/scim/models.go:14-17, internal/scim/models.go:120, internal/scim/mapper.go:14, internal/scim/mapper.go:87, internal/scim/ratelimit.go:103-104, internal/scim/auth.go:61-62]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Every error uses the SCIM Error schema with a string status, optional scimType and detail; content type application/scim+json → internal/scim/models.go:14-17, internal/scim/models.go:120, internal/scim/handler.go:774-782
- Body over the 1 MiB limit → 413; malformed JSON → 400 invalidSyntax → internal/scim/handler.go:577-586 (user), internal/scim/handler.go:645-653 (PATCH), internal/scim/handler.go:662-670 (group)
- User schema or field validation failure → 400 invalidValue (schemas must hold the User URN, userName required, fields over 256 bytes) → internal/scim/handler.go:596, internal/scim/handler.go:610-636
- Group displayName or externalId over 256 bytes → 400 invalidValue → internal/scim/handler.go:674, internal/scim/handler.go:681-695
- Duplicate userName → 409 uniqueness → internal/scim/handler.go:119-120 (create), internal/scim/handler.go:211-212 (PUT), internal/scim/handler.go:315-316 (PATCH)
- Duplicate externalId, users → 409 uniqueness "externalId is already in use" → internal/scim/handler.go:121-122 (create), internal/scim/handler.go:213-214 (PUT), internal/scim/handler.go:317-318 (PATCH)
- Duplicate externalId, groups → 409 uniqueness, same detail → internal/scim/handler.go:711-712
- Duplicate displayName → 409 uniqueness; unknown or foreign group member → 400 invalidValue → internal/scim/handler.go:709-714
- externalId length is checked on the trimmed value, in bytes → internal/scim/handler.go:630-634, internal/scim/handler.go:693-694
- Stored externalId is trimmed, empty becomes NULL → internal/scim/mapper.go:14, internal/scim/mapper.go:87
- PUT id mismatch → 400 mutability → internal/scim/handler.go:193 (user), internal/scim/handler.go:414 (group)
- PATCH refusals use scimType invalidPath for unsupported paths, otherwise invalidValue → internal/scim/handler.go:232-246
- PATCH re-validates the patched resource after operations apply, so a PATCH can return the same 400 as a create → internal/scim/handler.go:290-300 (user), internal/scim/handler.go:448-452 (group)
- Unsupported or malformed filter → 400 invalidFilter → internal/scim/handler.go:154, internal/scim/handler.go:376
- Rate limited → 429 tooMany with Retry-After: 1 → internal/scim/ratelimit.go:103-104
- Missing or bad bearer token → 401 with WWW-Authenticate: Bearer → internal/scim/auth.go:61-62
- Unknown user → 404 → internal/scim/handler.go:137-138, internal/scim/handler.go:783-785
- Unknown group → 404 → internal/scim/handler.go:720-722
- Unknown path → 404 "unknown resource"; wrong verb → 405 → internal/scim/handler.go:758-764
- Any unexpected error → 500 "internal server error"; cause is logged only → internal/scim/handler.go:788-790
