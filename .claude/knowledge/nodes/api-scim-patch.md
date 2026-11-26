---
id: api-scim-patch
kind: api
title: SCIM PATCH support
summary: PATCH for Users (core attributes plus registered extensions) and Groups (members add/replace/remove); op names are case-insensitive; no bulk
tags: [patch, PATCH, RFC 7644 3.5.2, add, replace, remove, members, emails, operations, partial update, scim]
links: [implements:module-scim, related:api-scim-routes, related:api-scim-errors, related:component-scim-extended-attributes, related:model-scim-resources]
sources: [internal/scim/patch.go:1-372, internal/scim/handler.go:225-306, internal/scim/handler.go:409-452]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Request body is PatchOp: schemas plus Operations (op, path, value); value shape depends on path → internal/scim/patch.go:15-29
- Op names are matched case-insensitively (add, replace, remove); anything else is an error → internal/scim/patch.go:40-62
- Empty Operations list is an error "Operations is required" → internal/scim/patch.go:41-43
- User PATCH is applied to the current resource and the whole result is persisted → internal/scim/patch.go:31-39, internal/scim/handler.go:225-306
- Supported user paths: name.givenName, name.familyName, userName, externalId, active, emails (any emails[...] filter form targets the one stored email) → internal/scim/patch.go:34, internal/scim/patch.go:67-152
- Pathless value objects are accepted for user attributes → internal/scim/patch.go:67-86
- remove clears a supported attribute (clearPath) → internal/scim/patch.go:154-180
- Group PATCH: add appends members, replace overwrites, remove takes members[value eq "id"] or clears all → internal/scim/patch.go:204-227, internal/scim/patch.go:194-199
- Member value extraction keeps member id case-sensitive → internal/scim/patch.go:194-202, internal/scim/patch.go:356-362
- Extended attributes in the same PATCH are split out first; core ops run only if present → internal/scim/handler.go:250-266
- PATCH response returns the updated resource with 200 → internal/scim/handler.go:300-306
- Bulk is not implemented (advertised false) → internal/scim/discovery.go:109
