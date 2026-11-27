---
id: model-scim-resources
kind: data-model
title: SCIM resource models and store mapping
summary: Go structs for SCIM User, Group, Member, ListResponse, Error and the lenient Bool, plus mapping to store.User and store.Group
tags: [model, data model, scim user, scim group, schema, mapper, json, Bool, ListResponse, RFC 7643, store mapping]
links: [implements:module-scim, depends_on:api-scim-errors, related:gotcha-scim-idp-quirks, related:component-scim-extended-attributes]
sources: [internal/scim/models.go:10-125, internal/scim/mapper.go:11-116]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Schema URNs for User, Group, ListResponse and Error; responses use application/scim+json → internal/scim/models.go:10-17
- User struct: schemas, id (omitted when empty), externalId, userName, name, emails, active, meta → internal/scim/models.go:50-64
- Name carries givenName and familyName; Email carries value and primary → internal/scim/models.go:66-74
- Meta carries resourceType, created, lastModified and location; location is built per tenant → internal/scim/models.go:76-80, internal/scim/mapper.go:35-40
- Group struct: schemas, id, externalId, displayName, members, meta; no active attribute → internal/scim/models.go:86-95
- Member: value, $ref, display; display is never populated on output → internal/scim/models.go:99-108
- ListResponse uses capitalised "Resources" → internal/scim/models.go:111-117
- Error: string status, optional scimType and detail → internal/scim/models.go:120-124
- Bool accepts JSON true or "true" and always marshals as a boolean → internal/scim/models.go:20-47
- Store mapping: trimmed userName and externalId, active (true when absent), first email stored, givenName/familyName → internal/scim/mapper.go:11-26
- Only one email is stored: the primary, or the first if none is primary → internal/scim/mapper.go:62-72
- Group store mapping → internal/scim/mapper.go:84-116
- Unknown top-level fields are dropped on decode, except registered extended attributes → internal/scim/handler.go:573-575
