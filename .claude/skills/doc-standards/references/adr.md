# ADR template (docs/adr/NNNN-title.md)

```markdown
# 💡 NNNN. <Decision title in imperative form, e.g. "Use Postgres for order storage">

- Status: proposed | accepted | superseded by NNNN | deprecated
- Date: YYYY-MM-DD
- Deciders: <roles or teams>

<!-- toc -->
## 📑 Contents

- [Context](#-context)
- [Decision](#-decision)
- [Options considered](#-options-considered)
- [Consequences](#-consequences)
- [References](#-references)

<!-- /toc -->

## 🧭 Context
<The forces at play: requirements, constraints, current pain. Facts with pointers.>

## ✅ Decision
<What we will do, stated plainly.>

## 🔀 Options considered
| Option | Pros | Cons |
|---|---|---|
| A (chosen) | | |
| B | | |

## 🚦 Consequences
- Positive: …
- Negative / trade-offs: …
- Follow-ups: …

## 🔗 References
- <links, code pointers path:line, related ADRs>
```

Rules:
- ADRs are immutable once accepted. To change a decision, write a new ADR that supersedes the old one.
- Create or update a KG `decision-*` node with `documented_in:doc-adr-NNNN`.
