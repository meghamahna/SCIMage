---
id: doc-readme
kind: doc
title: README.md
summary: Section map of README.md, including the Docker deploy and local-testing blocks, the audit refusal note and the endpoint conflict rules
tags: [docs, readme, sections, docker, deploy, local testing, endpoints, externalId, audit, denied, change delivery, getting started, migrate]
links: [related:architecture-overview, related:workflow-docker, related:api-scim-errors, related:gotcha-console-docker-bind-address, related:module-apidocs, documented_in:doc-architecture]
sources: [README.md:5-276]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Tagline: SCIM 2.0 provisioning server with signed change delivery and an AI-advisory audit trail → README.md:5
- Why SCIMage, including the audit promise → README.md:38-45
- Refused mutations are recorded as `denied` audit entries → README.md:54
- The problem it solves → README.md:47-56
- What it does: endpoint table; duplicate userName and externalId answer 409 → README.md:58-75
- Discovery endpoints and the unauthenticated /healthz and /readyz probes → README.md:77-79
- Swagger UI at /docs and the admin console at /console → README.md:81
- userName is case-insensitive; externalId unique per tenant, 256-byte limit → README.md:87-89
- Validated against Microsoft's Entra SCIM validator; extended attributes → README.md:91
- How it's built: one Postgres database, one transaction per mutation → README.md:93-101
- Change delivery (SCIM_WEBHOOK_URL enables it) → README.md:103-107
- Security practices and the threat model link → README.md:109-113
- ARIA: the AI advisory audit reviewer → README.md:117-130
- Getting started: local development path and console or CLI provisioning → README.md:132-198
- Deploy with Docker: production run and migrations against another database → README.md:200-233
- Local testing: docker run on the compose network with --env-file .env → README.md:235-257
- Running tests (make up, then make test) → README.md:261-270
- Roadmap → README.md:272-276
- Documentation index → README.md:278
