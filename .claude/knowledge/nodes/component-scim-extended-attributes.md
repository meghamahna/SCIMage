---
id: component-scim-extended-attributes
kind: module
title: SCIM extensible attributes (opt-in)
summary: Off by default; when SCIM_EXTENDED_ATTRIBUTES=1 and a tenant registers names, unknown top-level user attributes are stored in a JSONB blob and merged back on reads
tags: [extended attributes, extensible, JSONB, custom attributes, registry, SCIM_EXTENDED_ATTRIBUTES, feature flag, enterprise extension]
links: [implements:module-scim, depends_on:module-store, related:api-scim-patch, related:model-scim-resources, related:gotcha-scim-idp-quirks]
sources: [internal/scim/extended.go:1-228, internal/scim/handler.go:30-53, internal/scim/attributestore.go:1-18]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Feature is inert unless SCIM_EXTENDED_ATTRIBUTES=1 is set and the tenant has registered a name → internal/scim/extended.go:12-16, internal/scim/handler.go:51
- Flag is held on the Handler; with it off the registry is never consulted and users serialise as before → internal/scim/handler.go:30-33, internal/scim/extended.go:23-25
- Registry is a lookup from lowercased name to canonical name; matching is case-insensitive → internal/scim/extended.go:35-39
- Create captures registered keys from the raw body into the blob; no registered keys means a NULL column → internal/scim/extended.go:42-45, internal/scim/handler.go:101-115
- Reads merge the blob back into the user JSON → internal/scim/extended.go:75-80
- PATCH on a registered name is split off and applied to the blob; other paths stay on the core path → internal/scim/extended.go:146-150, internal/scim/handler.go:250-266
- Registry access goes through AttributeStore.ListAttributes → internal/scim/attributestore.go:14-16
