---
id: decision-user-soft-delete
kind: decision
title: Users are soft-deleted (deactivated); groups are hard-deleted
summary: DELETE on a user only sets active to false so history keeps the subject, while group DELETE is a real removal because the Group schema has no active attribute
tags: [decision, soft delete, deactivate, deactivation, delete, users, groups, history, audit, why, active]
links: [related:data-model-core-users-groups, related:module-store, related:decision-audit-in-transaction]
sources: [docs/ARCHITECTURE.md:80-82, internal/store/user.go:375-402]
updated: 2026-11-26
verified_commit: 4185d2b
---
## Decision
- users: deactivation is a soft delete, so history keeps a subject → docs/ARCHITECTURE.md:81
- groups: DELETE is a real deletion, since the Group schema has no active attribute to soft-delete into → docs/ARCHITECTURE.md:82
- Store entry point for user deactivation, which locks the row first → internal/store/user.go:375-402
## Why
- Keeping the user row lets audit_log entries and history still point at a real subject → docs/ARCHITECTURE.md:81
