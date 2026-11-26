---
id: api-scim-filter
kind: api
title: SCIM filter support
summary: Only `attribute eq "value"` on userName/externalId (users) or displayName/externalId (groups); everything else is a 400 invalidFilter
tags: [filter, query, eq, userName, externalId, displayName, invalidFilter, RFC 7644 3.4.2.2, reconciliation, lookup]
links: [implements:module-scim, related:decision-scim-strict-filter, related:api-scim-routes, related:api-scim-errors]
sources: [internal/scim/filter.go:17-85, internal/scim/handler.go:144-160]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Single accepted shape: `attr eq "value"`, one anchored regex, whitespace tolerant → internal/scim/filter.go:17-19
- Users: userName and externalId; attribute name matched case-insensitively → internal/scim/filter.go:22-26
- Groups: displayName and externalId → internal/scim/filter.go:58-61
- Empty or blank filter lists everything → internal/scim/filter.go:31-34
- Anchored regex means compound or other operator expressions do not match → 400 invalidFilter → internal/scim/filter.go:36-39, internal/scim/handler.go:146-151
- Unknown attribute → 400 invalidFilter naming the supported attributes → internal/scim/filter.go:41-44
- Empty quoted value is rejected → internal/scim/filter.go:46-49
- Discovery advertises filter support with MaxResults equal to store.MaxPageSize → internal/scim/discovery.go:112
- Parsed filter is passed to the store, which does the matching; this package does no filtering in memory → internal/scim/handler.go:156
