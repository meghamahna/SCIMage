---
id: decision-scim-strict-filter
kind: decision
title: Refuse unsupported SCIM filters instead of approximating
summary: Only equality on reconciliation keys is accepted; any other filter is a 400 so a client never acts on the wrong set
tags: []
links: [documented_in:api-scim-filter, related:module-scim]
sources: [internal/scim/filter.go:11-18, internal/scim/handler.go:148-155, internal/scim/discovery.go:109-117]
updated: 2026-11-28
verified_commit: 924cf37
---
## Decision
- Implement only `attr eq "value"` on userName or externalId (groups: displayName or externalId) → internal/scim/filter.go:11-14
## Why
- IdPs look a user up before create or update; an approximate answer could make a client change the wrong person → internal/scim/filter.go:14-18
- An unfiltered list would read as a match to the IdP, so it is refused → internal/scim/handler.go:148-151
## Trade-off
- Sort, attribute projection and other operators are unsupported and advertised as such in discovery → internal/scim/discovery.go:115-117
