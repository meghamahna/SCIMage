# Mermaid templates (shared palette)

Every template uses the same `%%{init}%%` block and `classDef` palette.

**How the palette survives both themes:**
- Node fills are light tints with dark text, so they stay readable on light **and** dark page backgrounds.
- Edges and borders use mid-tone slate, which has enough contrast on both.

| Role | classDef | Fill | Stroke | Use for |
|---|---|---|---|---|
| svc | `svc` | `#DBEAFE` | `#2563EB` | services, modules, functions |
| data | `data` | `#DCFCE7` | `#16A34A` | databases, queues, caches, files |
| ext | `ext` | `#F1F5F9` | `#64748B` | third-party / external systems (dashed) |
| user | `user` | `#FEF3C7` | `#D97706` | people, actors, clients |
| accent | `accent` | `#FCE7F3` | `#DB2777` | the one thing the reader should notice |
| err | `err` | `#FEE2E2` | `#DC2626` | error, deny and failure paths |

Copy the init line and classDefs **verbatim** and change only the nodes and edges. The `flowchart` settings in the init line (straight edges, 48px node spacing, 64px rank spacing, 16px padding) make every flowchart lay out the same way; don't change them per diagram.

## Flowchart (decisions / processes)

```mermaid
%%{init: {'theme':'base','flowchart':{'curve':'linear','nodeSpacing':48,'rankSpacing':64,'padding':16},'themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','primaryColor':'#DBEAFE','primaryTextColor':'#0F172A','primaryBorderColor':'#2563EB','lineColor':'#64748B','secondaryColor':'#DCFCE7','tertiaryColor':'#F1F5F9','clusterBkg':'#F8FAFC','clusterBorder':'#94A3B8','edgeLabelBackground':'#FFFFFF'}}}%%
flowchart TD
  accTitle: Request validation flow
  accDescr: Incoming requests are validated, then either rejected or processed and stored.
  start([Request received]):::user --> validate{Valid payload?}:::svc
  validate -- no --> reject[Return 400]:::accent
  validate -- yes --> process[Process order]:::svc
  process --> store[(Orders DB)]:::data
  classDef svc fill:#DBEAFE,stroke:#2563EB,stroke-width:1.5px,color:#0F172A
  classDef data fill:#DCFCE7,stroke:#16A34A,stroke-width:1.5px,color:#0F172A
  classDef ext fill:#F1F5F9,stroke:#64748B,stroke-width:1.5px,stroke-dasharray:4 3,color:#0F172A
  classDef user fill:#FEF3C7,stroke:#D97706,stroke-width:1.5px,color:#0F172A
  classDef accent fill:#FCE7F3,stroke:#DB2777,stroke-width:2px,color:#0F172A
  classDef err fill:#FEE2E2,stroke:#DC2626,stroke-width:1.5px,color:#0F172A
```

## C4-style container view (systems / architecture)

Built with a flowchart and subgraphs, so it renders everywhere and shares the palette. Native `C4Container` ignores classDef.

```mermaid
%%{init: {'theme':'base','flowchart':{'curve':'linear','nodeSpacing':48,'rankSpacing':64,'padding':16},'themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','primaryColor':'#DBEAFE','primaryTextColor':'#0F172A','primaryBorderColor':'#2563EB','lineColor':'#64748B','clusterBkg':'#F8FAFC','clusterBorder':'#94A3B8','edgeLabelBackground':'#FFFFFF'}}}%%
flowchart LR
  accTitle: Container view
  accDescr: A user calls the web app, which uses the API; the API reads and writes Postgres and calls a payment provider.
  user["Customer<br/><small>person</small>"]:::user
  subgraph sys["Shop platform"]
    web["Web app<br/><small>React SPA</small>"]:::svc
    api["API<br/><small>Python / FastAPI</small>"]:::svc
    db[("Postgres<br/><small>orders, users</small>")]:::data
  end
  pay["Payment provider<br/><small>external</small>"]:::ext
  user -- uses --> web
  web -- "JSON/HTTPS" --> api
  api -- "reads/writes" --> db
  api -- charges --> pay
  classDef svc fill:#DBEAFE,stroke:#2563EB,stroke-width:1.5px,color:#0F172A
  classDef data fill:#DCFCE7,stroke:#16A34A,stroke-width:1.5px,color:#0F172A
  classDef ext fill:#F1F5F9,stroke:#64748B,stroke-width:1.5px,stroke-dasharray:4 3,color:#0F172A
  classDef user fill:#FEF3C7,stroke:#D97706,stroke-width:1.5px,color:#0F172A
  classDef accent fill:#FCE7F3,stroke:#DB2777,stroke-width:2px,color:#0F172A
```

## Sequence (interactions over time)

Sequence diagrams ignore classDef. The init block sets actor and note colors from the same palette.

```mermaid
%%{init: {'theme':'base','themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','actorBkg':'#DBEAFE','actorBorder':'#2563EB','actorTextColor':'#0F172A','actorLineColor':'#94A3B8','signalColor':'#64748B','signalTextColor':'#0F172A','noteBkgColor':'#FEF3C7','noteBorderColor':'#D97706','noteTextColor':'#0F172A','activationBkgColor':'#DCFCE7','activationBorderColor':'#16A34A','labelBoxBkgColor':'#F1F5F9','labelBoxBorderColor':'#64748B'}}}%%
sequenceDiagram
  accTitle: Login sequence
  accDescr: The client posts credentials, the API verifies them against the user store and returns a token.
  autonumber
  participant C as Client
  participant A as API
  participant U as User store
  C->>+A: POST /login
  A->>+U: find user by email
  U-->>-A: user record
  Note over A: verify password hash
  A-->>-C: 200 + access token
```

## ER (data model)

```mermaid
%%{init: {'theme':'base','themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','primaryColor':'#DCFCE7','primaryTextColor':'#0F172A','primaryBorderColor':'#16A34A','lineColor':'#64748B','tertiaryColor':'#F8FAFC'}}}%%
erDiagram
  accTitle: Orders data model
  accDescr: A user places many orders; each order has many line items referencing products.
  USER ||--o{ ORDER : places
  ORDER ||--|{ LINE_ITEM : contains
  PRODUCT ||--o{ LINE_ITEM : "appears in"
  USER {
    uuid id PK
    string email UK
  }
  ORDER {
    uuid id PK
    uuid user_id FK
    string status
  }
```

## State (lifecycle)

```mermaid
%%{init: {'theme':'base','themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','primaryColor':'#DBEAFE','primaryTextColor':'#0F172A','primaryBorderColor':'#2563EB','lineColor':'#64748B'}}}%%
stateDiagram-v2
  accTitle: Order lifecycle
  accDescr: Orders move from pending to paid to shipped, and can be cancelled before shipping.
  [*] --> Pending
  Pending --> Paid: payment ok
  Pending --> Cancelled: timeout
  Paid --> Shipped: dispatched
  Paid --> Cancelled: refund
  Shipped --> [*]
  Cancelled --> [*]
  classDef accent fill:#FCE7F3,stroke:#DB2777,stroke-width:2px,color:#0F172A
  class Cancelled accent
```

## Checklist before handing off
- [ ] At most 12 nodes; one direction; few crossings.
- [ ] Palette copied verbatim; roles used for meaning, with a legend if more than 2 roles.
- [ ] `accTitle` + `accDescr` present.
- [ ] Every node and edge traces to a real `path:line` or system.
- [ ] `python3 .claude/tools/svg_lint.py <file>` shows no ERROR.

## Dark theme

Use this only when `diagrams.theme` in `.claude/harness.json` is `dark`. Replace the `themeVariables` and the classDefs; keep everything else, including the `flowchart` layout settings.

```text
'themeVariables':{'fontFamily':'ui-sans-serif, system-ui, sans-serif','fontSize':'14px','background':'#0B1220','primaryColor':'#1E3A8A','primaryTextColor':'#E2E8F0','primaryBorderColor':'#60A5FA','lineColor':'#94A3B8','secondaryColor':'#14532D','tertiaryColor':'#1E293B','clusterBkg':'#0F172A','clusterBorder':'#475569','edgeLabelBackground':'#0B1220'}

classDef svc fill:#1E3A8A,stroke:#60A5FA,stroke-width:1.5px,color:#E2E8F0
classDef data fill:#14532D,stroke:#4ADE80,stroke-width:1.5px,color:#E2E8F0
classDef ext fill:#1E293B,stroke:#94A3B8,stroke-width:1.5px,stroke-dasharray:4 3,color:#E2E8F0
classDef user fill:#78350F,stroke:#FBBF24,stroke-width:1.5px,color:#E2E8F0
classDef accent fill:#831843,stroke:#F472B6,stroke-width:2px,color:#E2E8F0
classDef err fill:#450A0A,stroke:#F87171,stroke-width:1.5px,color:#E2E8F0
```
