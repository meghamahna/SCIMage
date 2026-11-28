---
id: gotcha-scim-tenant-path-parsing
kind: gotcha
title: Tenant id is parsed twice and must agree
summary: Auth reads the tenant from the raw escaped path before the mux runs; a mismatch with the mux would authenticate as one tenant and route as another
tags: []
links: [related:component-scim-auth, related:api-scim-routes, related:decision-tenant-scoping]
sources: [internal/scim/auth.go:99-102, internal/scim/auth.go:132-133, internal/scim/auth.go:150-165, internal/scim/handler.go:60-70]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Auth runs before the mux, so r.PathValue("tenantID") is empty there; auth parses the path itself → internal/scim/auth.go:99-102
- tenantIDFromPath takes EscapedPath, cuts the segment after /scim/v2/, and unescapes only that segment → internal/scim/auth.go:150-165
- Must match the mux's own matching, or a caller could authenticate for tenant A and be routed to tenant B → internal/scim/auth.go:132-133
- A malformed or unmatched path yields "", which never equals a real tenant and so fails auth → internal/scim/auth.go:150-165
- Any change to route patterns or the /scim/v2/ prefix must update this parser too → internal/scim/handler.go:65-70, internal/scim/auth.go:153
