---
id: doc-readme
kind: doc
title: README.md
summary: Section map of README.md, including the Docker deploy and local-testing blocks, the audit refusal note and the endpoint conflict rules
tags: [docs, readme, sections, docker, deploy, local testing, endpoints, externalId, audit, denied, change delivery, getting started]
links: [related:architecture-overview, related:workflow-docker, related:api-scim-errors, related:gotcha-console-docker-bind-address]
sources: [README.md:5-272]
updated: 2026-11-26
verified_commit: b8c7070
---
## Facts
- Tagline: SCIM 2.0 provisioning server with signed change delivery and an AI-advisory audit trail → README.md:5
- Why SCIMage, including the audit promise → README.md:33-41
- Refused mutations are recorded as `denied` audit entries → README.md:49
- The problem it solves → README.md:42
- What it does: endpoint table; duplicate userName and externalId answer 409 → README.md:53-65
- Admin console and Swagger UI at /docs → README.md:76
- externalId uniqueness per tenant and the 256-byte limit → README.md:84
- How it's built → README.md:88
- Change delivery (SCIM_WEBHOOK_URL enables it) → README.md:96-100
- Security practices and the threat model link → README.md:102-108
- ARIA: the AI advisory audit reviewer → README.md:110
- Getting started (local development path, console token) → README.md:125-185
- Deploy with Docker (image build, production run, local testing) → README.md:189-235
- Local testing: docker run on the compose network with --env-file .env → README.md:212-235
- Running tests (make up, then make test) → README.md:237-246
- Roadmap → README.md:248
- Documentation → README.md:254
- Tech → README.md:266
- License → README.md:270
