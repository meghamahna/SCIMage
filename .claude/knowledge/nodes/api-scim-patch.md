---
id: api-scim-patch
kind: api
title: SCIM PATCH support
summary: PATCH for Users (core attributes plus registered extensions) and Groups (members add/replace/remove); each PATCH runs under the resource's row lock; op names are case-insensitive; no bulk
tags: [patch, PATCH, RFC 7644 3.5.2, add, replace, remove, members, emails, operations, partial update, row lock, serialize, concurrency, scim]
links: [implements:module-scim, related:module-store, related:decision-audit-in-transaction, related:api-scim-routes, related:api-scim-errors, related:component-scim-extended-attributes, related:model-scim-resources]
sources: [internal/scim/patch.go:1-413, internal/scim/handler.go:246-318, internal/scim/handler.go:422-457, internal/store/user.go:290-319, internal/store/group.go:359-391, docs/ARCHITECTURE.md:156-162]
updated: 2026-11-26
verified_commit: 4185d2b
---
## Facts
- Request body is PatchOp: schemas plus Operations (op, path, value); value shape depends on path → internal/scim/patch.go:16-30
- Op names are matched case-insensitively (add, replace, remove); anything else is an error → internal/scim/patch.go:213-222
- Empty Operations is an error "Operations is required" (groups); the user path reaches the same check → internal/scim/patch.go:206-208, internal/scim/handler.go:276-279
- User PATCH: core and extended ops are applied to a copy of the locked user, validated, then written by PatchUser → internal/scim/handler.go:272-301
- Supported user paths: name.givenName, name.familyName, userName, externalId, active, emails → internal/scim/patch.go:68-153
- Pathless value objects are accepted for user attributes → internal/scim/patch.go:68-87
- remove clears a supported attribute (clearPath) → internal/scim/patch.go:155-181
- Extended attributes in one PATCH are split out first; core ops run if present, or if there are no extended ops → internal/scim/handler.go:263-285
- Group PATCH: add appends members, replace overwrites, remove drops members[value eq "id"] or clears all → internal/scim/patch.go:210-229, internal/scim/patch.go:327-348
- Member value extraction keeps the member id case-sensitive → internal/scim/patch.go:285-299
- Group member folding is linear and keeps member order (memberList tracks adds and removes) → internal/scim/patch.go:357-412
- Group PATCH is applied to a copy of the locked group, members included → internal/store/group.go:371-376
- PATCH is serialized per resource: the store locks the row, runs the patch callback and writes, in one transaction → internal/store/user.go:290-319, internal/store/group.go:359-391
- Response is the updated resource with 200 → internal/scim/handler.go:317, internal/scim/handler.go:456
- Bulk is not implemented (advertised false) → internal/scim/discovery.go:109
## Gotchas
- The patch callback runs inside the transaction: no I/O, and it must not modify its argument → internal/store/user.go:287-288
- PATCH on a missing id returns 404 from the lock step with no refusal row; this may change in a later item → internal/store/user.go:297-303, internal/store/group.go:366-369
- No ETag or If-Match support; concurrency is handled by the row lock, not by version checks → docs/ARCHITECTURE.md:156-162
