---
id: decision-scim-uniform-401
kind: decision
title: Every auth failure returns the same 401
summary: Missing, unknown, revoked, expired, wrong-secret and wrong-tenant tokens all get one generic 401 so callers cannot probe tenants or key ids; the reason is logged server-side
tags: [decision, 401, auth, uniform, probing, tenants, key id, why, logging]
links: [documented_in:component-scim-auth, related:module-scim, related:api-scim-errors]
sources: [internal/scim/auth.go:29-44, internal/scim/auth.go:55-62, internal/scim/auth.go:98-136]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Decision
- One generic 401 for every rejection reason → internal/scim/auth.go:61-62
- Why: distinguishing reasons would let a caller learn which tenants or key ids exist → internal/scim/auth.go:36-40
- Unauthenticated requests for unknown paths get 401, not 404, for the same reason → internal/scim/auth.go:41-44
- The reason is still logged server-side at Warn, so the flat 401 does not hide attacks or outages → internal/scim/auth.go:55-60
