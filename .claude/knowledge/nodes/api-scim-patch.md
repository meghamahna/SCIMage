---
id: api-scim-patch
kind: api
title: SCIM PATCH support
summary: PATCH for Users (core attributes plus registered extensions) and Groups (members add/replace/remove); each PATCH runs under the resource's row lock; op names are case-insensitive; no bulk
tags: [patch, PATCH, RFC 7644 3.5.2, add, replace, remove, members, emails, operations, partial update, row lock, serialize, concurrency, scim]
links: [implements:module-scim, related:module-store, related:decision-audit-in-transaction, related:api-scim-routes, related:api-scim-errors, related:component-scim-extended-attributes, related:model-scim-resources]
sources: [internal/scim/patch.go:14-413, internal/scim/handler.go:254-328, internal/scim/handler.go:433-466, internal/store/user.go:288-328, internal/store/group.go:362-402, docs/ARCHITECTURE.md:155-161]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Body is PatchOp (schemas, Operations of op/path/value); the value shape depends on the path → internal/scim/patch.go:19-30
- Op names are case-insensitive (add, replace, remove); anything else is an error → internal/scim/patch.go:50-54, internal/scim/patch.go:213-222
- Empty Operations is an error, "Operations is required" → internal/scim/patch.go:42-44, internal/scim/patch.go:206-208
- User PATCH splits off extended ops first; core ops run if present, or if there are no extended ops → internal/scim/handler.go:269-291
- User PATCH applies to a copy of the locked user, validates the result, then calls PatchUser → internal/scim/handler.go:279-307
- Supported user paths: name.givenName, name.familyName, userName, externalId, active, emails → internal/scim/patch.go:88-153
- Pathless value objects are accepted for user attributes → internal/scim/patch.go:68-87
- remove clears a supported attribute → internal/scim/patch.go:155-181
- Group ops: add appends, replace overwrites, remove drops members[value eq "id"], clears externalId, or clears all members → internal/scim/patch.go:232-249, internal/scim/patch.go:327-347
- Member value accepts a bare id, one member object, or an array; ids keep their case → internal/scim/patch.go:299-326
- Group member edits fold through memberList in op order and keep member order → internal/scim/patch.go:357-412
- Group PATCH works on a copy of the locked group, members included → internal/store/group.go:381-386
- Each PATCH is one transaction: lock the row, run the patch callback, write, commit → internal/store/user.go:297-328, internal/store/group.go:366-402
- Success returns 200 with the updated resource → internal/scim/handler.go:325, internal/scim/handler.go:465
- Bulk is not implemented (advertised as unsupported) → internal/scim/discovery.go:109
## Gotchas
- The patch callback runs inside the transaction: no I/O, and it must not modify its argument → internal/store/user.go:293-294, internal/store/group.go:364-365
- PATCH on a missing id is refused and recorded as a refusal, then returns not found → internal/store/user.go:304-310, internal/store/group.go:373-378
- No ETag or If-Match; concurrency comes from the row lock, not version checks → docs/ARCHITECTURE.md:155-161
