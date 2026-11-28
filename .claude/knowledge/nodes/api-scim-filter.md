---
id: api-scim-filter
kind: api
title: SCIM filter support
summary: Only `attribute eq "value"` on userName or externalId (users) and displayName or externalId (groups); everything else is a 400 invalidFilter
tags: [filter, query, eq, userName, externalId, displayName, invalidFilter, RFC 7644 3.4.2.2, reconciliation, lookup]
links: [implements:module-scim, related:decision-scim-strict-filter, related:api-scim-routes, related:api-scim-errors]
sources: [internal/scim/filter.go:11-85, internal/scim/handler.go:148-160, internal/scim/discovery.go:112]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Single accepted shape: `attr eq "value"`, one anchored regex, whitespace tolerant → internal/scim/filter.go:19
- Users: userName and externalId; attribute name matched case-insensitively → internal/scim/filter.go:23-26
- Groups: displayName and externalId, parsed by parseGroupFilter → internal/scim/filter.go:58-63
- Empty or blank filter lists everything → internal/scim/filter.go:30-34
- Compound or other operator expressions fail the anchored regex → 400 invalidFilter → internal/scim/filter.go:36-39, internal/scim/handler.go:152-155
- Unknown attribute → 400 invalidFilter naming the supported attributes → internal/scim/filter.go:41-44
- Empty quoted value is rejected → internal/scim/filter.go:46-49
- Discovery advertises filter support with MaxResults equal to store.MaxPageSize → internal/scim/discovery.go:112
- The parsed filter goes to the store, which does the matching; this package does no in-memory filtering → internal/scim/handler.go:160
