---
id: decision-scim-uniform-401
kind: decision
title: Every auth failure returns the same 401
summary: Missing, unknown, revoked, expired, wrong-secret and wrong-tenant tokens all get one generic 401 so callers cannot probe tenants or key ids
tags: [decision, 401, auth, enumeration, uniform error, security, why, tenant]
links: [documented_in:component-scim-auth, related:module-scim, related:api-scim-errors]
sources: [internal/scim/auth.go:29-52, internal/scim/auth.go:84-110]
updated: 2026-11-26
verified_commit: a99d9f9
---
## Facts
- Decision: one generic 401 for every rejection reason → internal/scim/auth.go:32-44, internal/scim/auth.go:49-52
- Why: distinguishing reasons would let a caller learn which tenants or key ids exist → internal/scim/auth.go:36-40
- Same reasoning as the single-token design it replaced; the order of checks keeps the secret compare last among lookups → internal/scim/auth.go:68-72
- Unauthenticated requests for unknown paths get 401, not 404, for the same reason → internal/scim/auth.go:41-44
- Operational failures are still logged server-side, so the flat 401 does not hide outages → internal/scim/auth.go:85-93
