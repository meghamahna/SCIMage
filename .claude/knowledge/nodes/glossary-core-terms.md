---
id: glossary-core-terms
kind: glossary
title: Glossary of SCIMage domain terms
summary: One-line definitions of SCIM, tenant, tokens, key id, externalId, soft delete, audit and admin audit logs, outbox, dead-letter, IdP and ARIA, each with its pointer
tags: [glossary, terms, definitions, scim, tenant, token, key id, externalId, soft delete, deactivate, audit, outbox, dead letter, idp, okta, entra, aria, lease, replay]
links: [related:architecture-overview, related:module-scim, related:module-store, related:module-webhook, related:module-aria, related:decision-user-soft-delete, related:decision-credentials-issued-by-cli, related:data-model-audit-log, related:data-model-webhook-outbox]
sources: [docs/IMPLEMENTATION_PLAN.md:5-7, docs/ARCHITECTURE.md:25, docs/ARCHITECTURE.md:74-87, docs/ARCHITECTURE.md:100-103, docs/ARCHITECTURE.md:119-139, docs/ARCHITECTURE.md:238-300, docs/ARCHITECTURE.md:414-427, docs/OKTA.md:1, docs/OKTA.md:61-62, docs/OKTA.md:93-95, docs/MS-ENTRA.md:1, docs/MS-ENTRA.md:48, internal/store/consoletoken.go:18]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Terms
- **SCIM 2.0**: the provisioning protocol (RFC 7644) this server implements for /Users and /Groups → docs/IMPLEMENTATION_PLAN.md:5-7
- **Tenant**: one customer organization; a row in `tenants` and the `tenant_id` every scoped row carries → docs/ARCHITECTURE.md:89-94
- **SCIM token**: tenant-scoped bearer credential `scimage_<keyID>_<secret>` a customer pastes into its IdP → docs/ARCHITECTURE.md:100-101
- **Console token**: system-wide credential `scimage_console_<keyID>_<secret>` for the admin console, with no tenant_id → docs/ARCHITECTURE.md:80, internal/store/consoletoken.go:18
- **Key id**: the non-secret, indexed half of a token used for lookup; only sha256 of the secret is stored → docs/ARCHITECTURE.md:100-103
- **externalId**: the IdP's own id for a user or group, used as a reconciliation key and filterable by equality → docs/OKTA.md:61-62, docs/OKTA.md:93-95, docs/MS-ENTRA.md:48
- **Soft delete / deactivate**: DELETE on a user sets it inactive and keeps the row; groups are hard-deleted → docs/ARCHITECTURE.md:81-82
- **audit_log**: one row per mutating SCIM call (user or group), written in the change's transaction → docs/ARCHITECTURE.md:84, docs/ARCHITECTURE.md:190
- **admin_audit_log**: one row per privileged CLI action (tenant create, token issue/revoke, attribute and replay actions) → docs/ARCHITECTURE.md:85, docs/ARCHITECTURE.md:124-133
- **Outbox**: the `webhook_deliveries` table; each change queues its event inside the same transaction → docs/ARCHITECTURE.md:238-239
- **Lease**: a claimed row's pushed-forward next_attempt_at, which stops other dispatchers taking it during a send → docs/ARCHITECTURE.md:246-268
- **Dead-letter**: terminal status for a delivery that exhausted retries or got a non-retryable 4xx; replay requeues it → docs/ARCHITECTURE.md:282-300
- **IdP**: identity provider that calls SCIM, such as Okta or Microsoft Entra ID; setup guides at docs/OKTA.md:1 and docs/MS-ENTRA.md:1 → docs/ARCHITECTURE.md:25
- **ARIA**: Audit Risk Intelligence Advisor; computes audit signals in Go and narrates them with an LLM, advisory only → docs/ARCHITECTURE.md:414-427
