---
id: gotcha-scim-tenant-path-parsing
kind: gotcha
title: Tenant id is parsed twice and must agree
summary: Auth reads the tenant from the raw escaped path before the mux runs; a mismatch with the mux would authenticate as one tenant and route as another
tags: [gotcha, tenant, path, escaping, PathValue, percent-encoding, security, mux, tenantID]
links: [related:component-scim-auth, related:module-scim, related:api-scim-routes]
sources: [internal/scim/auth.go:73-78, internal/scim/auth.go:118-139, internal/scim/handler.go:58-62]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Auth runs before the mux, so r.PathValue("tenantID") is empty there; auth parses the path itself → internal/scim/auth.go:73-78
- tenantIDFromPath takes EscapedPath, cuts the segment after /scim/v2/, and unescapes only that segment → internal/scim/auth.go:126-139
- Must match the mux's own matching, or a caller could authenticate for tenant A and be routed to tenant B → internal/scim/auth.go:118-125
- A malformed or unmatched path yields "", which never equals a real tenant and so fails auth → internal/scim/auth.go:128-135
- Any change to route patterns or the /scim/v2/ prefix must update this parser too → internal/scim/handler.go:65-70, internal/scim/auth.go:127
