---
id: module-apidocs
kind: module
title: API docs (internal/apidocs)
summary: Embedded hand-written OpenAPI 3.0.3 spec and vendored Swagger UI served at /docs on the SCIM listener; the spec declares 413, 429, 409 and 500 responses and is never parsed by a test
tags: [api docs, openapi, swagger, swagger ui, /docs, spec, yaml, embed, documentation, 429, 409, 500, Retry-After, TooLarge, scim]
links: [documented_in:doc-readme, related:module-console, related:module-scim, related:api-scim-errors, related:component-scim-ratelimit, documented_in:doc-architecture]
sources: [internal/apidocs/embed.go:1-66, internal/apidocs/openapi.yaml:1-45, internal/apidocs/openapi.yaml:55-110, internal/apidocs/openapi.yaml:372-433, cmd/server/main.go:127-132, internal/apidocs/swaggerui/VERSION:1, docs/ARCHITECTURE.md:77-82]
updated: 2026-11-28
verified_commit: 4322bf2
---
## Facts
- Package serves a hand-written OpenAPI spec and a vendored Swagger UI → internal/apidocs/embed.go:1-11
- Spec file is embedded with go:embed openapi.yaml → internal/apidocs/embed.go:20-21
- Swagger UI assets (index.html, swagger-ui.css, swagger-ui-bundle.js) embedded with go:embed → internal/apidocs/embed.go:23-24
- Spec is OpenAPI 3.0.3, info title "SCIMage: SCIM 2.0 Provisioning API", version 1.0.0 → internal/apidocs/openapi.yaml:1-12
- Spec servers URL is {scheme}://{host}/scim/v2/{tenantID} → internal/apidocs/openapi.yaml:31
- Auth scheme is bearerAuth (HTTP bearer), applied globally → internal/apidocs/openapi.yaml:43-44, internal/apidocs/openapi.yaml:372-376
- POST, PUT and PATCH on /Users, /Users/{id}, /Groups and /Groups/{id} each list a 413 response → internal/apidocs/openapi.yaml:109, 152, 178, 242, 284, 310
- Every operation lists 429 (TooManyRequests), including the discovery endpoints → internal/apidocs/openapi.yaml:82, 110, 127, 153, 179, 192, 216, 243, 260, 285, 311, 324, 339, 353, 367
- 429 component: scimType tooMany, and a Retry-After header that is always 1 → internal/apidocs/openapi.yaml:420-428
- 409 is declared on create, replace and PATCH for users and groups → internal/apidocs/openapi.yaml:104, 147, 173, 237, 279, 305
- Every operation except ServiceProviderConfig and ResourceTypes references the shared 500 ServerError response → internal/apidocs/openapi.yaml:83, 111, 128, 154, 180, 193, 217, 244, 261, 286, 312, 325, 368
- ServerError body is generic; the cause is logged, never returned → internal/apidocs/openapi.yaml:429-433
- The 413 response is the shared components/responses/TooLarge, body over 1 MiB → internal/apidocs/openapi.yaml:415-419
- Handler serves GET /openapi.yaml as application/yaml, max-age 300 → internal/apidocs/embed.go:44-56
- Other files come from the embedded swaggerui tree, max-age 3600 → internal/apidocs/embed.go:58-64
- Mounted in cmd/server with the /docs prefix stripped; bare /docs redirects to /docs/ → cmd/server/main.go:127-132
- Served on the SCIM listener, outside SCIM auth, like the health probes → cmd/server/main.go:120-132, docs/ARCHITECTURE.md:77-82
- Swagger UI version recorded in VERSION file: swagger-ui-dist 5.17.14 → internal/apidocs/swaggerui/VERSION:1
## Gotchas
- Mount must strip "/docs" without trailing slash, or the inner mux issues a redirect → internal/apidocs/embed.go:26-33
- Spec is public on purpose; it carries no tenant data, while the endpoints it documents still need a token → internal/apidocs/embed.go:8-11
- No Go test parses openapi.yaml; the file is served as raw bytes, so a YAML or schema error shows only when Swagger UI loads it → internal/apidocs/embed.go:44-56
