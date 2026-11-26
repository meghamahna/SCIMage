---
id: decision-scim-strict-filter
kind: decision
title: Refuse unsupported SCIM filters instead of approximating
summary: Only equality on reconciliation keys is accepted; any other filter is a 400 so a client never acts on the wrong set
tags: [decision, filter, why, invalidFilter, safety, reconciliation, provisioning]
links: [documented_in:api-scim-filter, related:module-scim]
sources: [internal/scim/filter.go:11-18, internal/scim/handler.go:144-151]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Decision: implement only `attr eq "value"` on userName or externalId (groups: displayName or externalId) → internal/scim/filter.go:11-14
- Why: IdPs filter to find a user before create or update, and an approximate answer could make a client update or deactivate the wrong person → internal/scim/filter.go:16-18
- Why not unfiltered: an unfiltered list would read as a match to the IdP → internal/scim/handler.go:144-147
- Trade-off: sort, attribute projection and other operators are unsupported and advertised as such in discovery → internal/scim/discovery.go:109-116
