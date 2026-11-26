---
id: gotcha-scim-idp-quirks
kind: gotcha
title: Identity provider payload quirks the parser accepts
summary: Entra sends string booleans and capitalised ops; unknown attributes are dropped, not rejected; these leniencies are deliberate
tags: [gotcha, Entra, Okta, identity provider, IdP, string boolean, primary, PATCH op case, unknown fields, interop]
links: [related:model-scim-resources, related:api-scim-patch, related:api-scim-errors]
sources: [internal/scim/models.go:20-47, internal/scim/patch.go:44-49, internal/scim/handler.go:536-540, internal/scim/handler.go:186-190]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Bool accepts "true" as a string because Entra sends emails[].primary that way; a plain bool would 400 every Entra create → internal/scim/models.go:20-26, internal/scim/models.go:72
- PATCH op names are matched case-insensitively ("Replace" from Entra) → internal/scim/patch.go:44-49
- Unknown top-level attributes (externalId, groups, enterprise extension) are silently dropped, not rejected → internal/scim/handler.go:536-540
- Exception: registered extended attributes are captured, see component-scim-extended-attributes → internal/scim/extended.go:42-45
- Group Member.display is accepted on input and never returned → internal/scim/models.go:97-108
- Body id that conflicts with the path id is refused with 400 mutability, not ignored → internal/scim/handler.go:186-190
