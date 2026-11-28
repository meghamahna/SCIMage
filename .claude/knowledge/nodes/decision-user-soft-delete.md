---
id: decision-user-soft-delete
kind: decision
title: Users are soft-deleted (deactivated); groups are hard-deleted
summary: DELETE on a user only sets active to false so history keeps the subject, while group DELETE is a real removal because the Group schema has no active attribute
tags: [decision, soft delete, deactivate, deactivation, delete, users, groups, history, audit, why, active]
links: [related:data-model-core-users-groups, related:module-store, related:decision-audit-in-transaction, documented_in:doc-architecture]
sources: [docs/ARCHITECTURE.md:96-97, internal/store/user.go:385-402, internal/store/group.go:457, internal/scim/handler.go:328-329, internal/scim/handler.go:468-470]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Decision
- Users: DELETE is a soft delete that sets active to false → docs/ARCHITECTURE.md:96, internal/store/user.go:388-402
- Groups: DELETE is a real deletion, since the Group schema has no active attribute to soft-delete into → docs/ARCHITECTURE.md:97, internal/store/group.go:457
## Why
- Keeping the user row lets audit_log entries and history still point at a real subject → docs/ARCHITECTURE.md:96
- Deactivation is idempotent, so a retried delete succeeds → internal/store/user.go:385-387
