# API reference template

Prefer generating the reference from the source of truth (OpenAPI, GraphQL schema, typed docstrings) and hand-write only the guides around it.

```markdown
# 📡 <Service> API reference

<One sentence: what this API is for and who calls it.>

<!-- toc -->
## 📑 Contents

- [<METHOD> /path/{param}](#--pathparam)
  - [Path / query parameters](#-path--query-parameters)
  - [Request body](#-request-body)
  - [Responses](#-responses)
  - [Example](#-example)
  - [Errors](#-errors)

<!-- /toc -->

## 📡 <METHOD> /path/{param}

<One sentence: what it does.>

**Auth:** <scheme and required scope/role>
**Idempotent:** yes/no (<how: Idempotency-Key header>)
**Rate limit:** <n/min>

### 🧾 Path / query parameters
| Name | Type | Required | Description |
|---|---|---|---|

### 📦 Request body
    {
      "field": "string"
    }

### ✅ Responses
| Status | Meaning | Body |
|---|---|---|
| 200 | OK | `Order` |
| 400 | Validation error | `Error` |
| 403 | Not owner of resource | `Error` |

### 📖 Example
    curl -X POST "$API_BASE/orders" \
      -H "Authorization: Bearer $API_TOKEN" \
      -H "Content-Type: application/json" \
      -d '{"sku": "ABC-1", "qty": 2}'

### ❌ Errors
<Error shape and codes, with how to recover.>
```

Rules:
- In examples, use env var **names** (`$API_TOKEN`) and never token values.
- Document authorization failures, not just the happy path.
- Note breaking changes and the deprecation timeline.
