---
id: module-apidocs
kind: module
title: API docs (internal/apidocs)
summary: Embedded OpenAPI 3.0 spec and vendored Swagger UI served unauthenticated at /docs, with no runtime network fetch
tags: [api docs, openapi, swagger, swagger ui, docs, spec, embed, specification, documentation, 413, body limit]
links: [documented_in:doc-readme, related:module-console, related:module-scim, related:api-scim-errors]
sources: [internal/apidocs/embed.go:1-66, internal/apidocs/openapi.yaml:1-30, internal/apidocs/openapi.yaml:107-275, internal/apidocs/openapi.yaml:372-376, cmd/server/main.go:127-132]
updated: 2026-11-26
verified_commit: b8c7070
---
## Facts
- Package serves a hand-written OpenAPI spec and a vendored Swagger UI → internal/apidocs/embed.go:1-11
- Spec file is embedded with go:embed openapi.yaml → internal/apidocs/embed.go:20-21
- Swagger UI assets (index.html, swagger-ui.css, swagger-ui-bundle.js) embedded with go:embed → internal/apidocs/embed.go:23-24
- Spec is OpenAPI 3.0.3, info title "SCIMage — SCIM 2.0 Provisioning API", version 1.0.0 → internal/apidocs/openapi.yaml:1-12
- Spec servers URL is {scheme}://{host}/scim/v2/{tenantID} → internal/apidocs/openapi.yaml:30-31
- Auth scheme is bearerAuth (HTTP bearer) → internal/apidocs/openapi.yaml:44, internal/apidocs/openapi.yaml:322-325
- POST, PUT and PATCH on /Users, /Users/{id}, /Groups and /Groups/{id} each list a 413 response → internal/apidocs/openapi.yaml:107, internal/apidocs/openapi.yaml:146, internal/apidocs/openapi.yaml:165, internal/apidocs/openapi.yaml:223, internal/apidocs/openapi.yaml:256, internal/apidocs/openapi.yaml:275
- The 413 response is the shared components/responses/TooLarge, body over 1 MiB → internal/apidocs/openapi.yaml:372-376
- Handler serves GET /openapi.yaml as application/yaml, max-age 300 → internal/apidocs/embed.go:44-56
- Other files come from the embedded swaggerui tree, max-age 3600 → internal/apidocs/embed.go:58-64
- Mounted in cmd/server with the /docs prefix stripped; bare /docs redirects to /docs/ → cmd/server/main.go:127-132
- Served on the SCIM listener, outside SCIM auth, like the health probes → cmd/server/main.go:120-132
- Swagger UI version recorded in VERSION file: swagger-ui-dist 5.17.14 → internal/apidocs/swaggerui/VERSION:1
## Gotchas
- Mount must strip "/docs" without trailing slash, or the inner mux issues a redirect → internal/apidocs/embed.go:26-33
- Spec is public on purpose; it carries no tenant data, while the endpoints it documents still need a token → internal/apidocs/embed.go:8-11
