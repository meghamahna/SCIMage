---
id: glossary-core-terms
kind: glossary
title: Glossary of SCIMage domain terms
summary: One-line definitions of SCIM, tenant, tokens, key id, externalId, soft delete, audit and admin audit logs, outbox, dead-letter, IdP and ARIA, each with its pointer
tags: [glossary, terms, definitions, scim, tenant, token, key id, externalId, soft delete, deactivate, audit, outbox, dead letter, idp, okta, entra, aria, lease, replay]
links: [related:architecture-overview, related:module-scim, related:module-store, related:module-webhook, related:module-aria, related:decision-user-soft-delete, related:decision-credentials-issued-by-cli, related:data-model-audit-log, related:data-model-webhook-outbox]
sources: [docs/IMPLEMENTATION_PLAN.md:5-7, docs/ARCHITECTURE.md:31, docs/ARCHITECTURE.md:94-100, docs/ARCHITECTURE.md:110-129, docs/ARCHITECTURE.md:145-152, docs/ARCHITECTURE.md:259-262, docs/ARCHITECTURE.md:287-289, docs/ARCHITECTURE.md:305-318, docs/ARCHITECTURE.md:511-521, docs/OKTA.md:1, docs/OKTA.md:65-67, docs/MS-ENTRA.md:3, internal/store/consoletoken.go:18]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Terms
- **SCIM 2.0**: the provisioning protocol (RFC 7644) this server implements for /Users and /Groups → docs/IMPLEMENTATION_PLAN.md:5-7
- **Tenant**: one customer organization; a row in `tenants` and the `tenant_id` every scoped row carries → docs/ARCHITECTURE.md:110-115
- **SCIM token**: tenant-scoped bearer credential `scimage_<keyID>_<secret>` a customer pastes into its IdP → docs/ARCHITECTURE.md:121-129
- **Console token**: system-wide credential for the admin console, stored in `console_tokens` with no tenant_id → docs/ARCHITECTURE.md:95, internal/store/consoletoken.go:18
- **Key id**: the non-secret, indexed half of a token used for lookup; only sha256 of the secret is stored → docs/ARCHITECTURE.md:121-125
- **externalId**: the IdP's own id for a user or group; a duplicate on create is a 409 → docs/OKTA.md:65-67
- **Soft delete / deactivate**: DELETE on a user sets it inactive and keeps the row; groups are hard-deleted → docs/ARCHITECTURE.md:96-97
- **audit_log**: one row per mutating SCIM call (user or group), written in the change's transaction → docs/ARCHITECTURE.md:99
- **admin_audit_log**: one row per privileged CLI action (tenant create, token issue/revoke) → docs/ARCHITECTURE.md:100, docs/ARCHITECTURE.md:145-152
- **Outbox**: the `webhook_deliveries` table; each change queues its event inside the same transaction → docs/ARCHITECTURE.md:102, docs/ARCHITECTURE.md:259-262
- **Lease**: a claimed row's pushed-forward next_attempt_at, which stops other dispatchers taking it during a send → docs/ARCHITECTURE.md:287-289
- **Dead-letter**: terminal status for a delivery that exhausted retries or got a non-retryable 4xx; replay requeues it → docs/ARCHITECTURE.md:305-318
- **IdP**: identity provider that calls SCIM, such as Okta or Microsoft Entra ID; setup guides at docs/OKTA.md:1 and docs/MS-ENTRA.md:3 → docs/ARCHITECTURE.md:31
- **ARIA**: Audit Risk Intelligence Advisor; computes audit signals in Go and narrates them with an LLM, advisory only → docs/ARCHITECTURE.md:511-521
