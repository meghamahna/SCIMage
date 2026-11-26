---
name: knowledge-graph
description: Query and maintain the project's index-based knowledge graph (.claude/knowledge). INDEX.md points to nodes, and nodes point to exact path:line locations. Use before searching the codebase, when answering "where/how is X", after significant code changes, when a [harness:kg] message appears, or when writing/updating knowledge nodes.
allowed-tools: Bash(python3 .claude/tools/kg.py *)
---

# Knowledge graph

An index-based memory of the project's **major** facts: INDEX → node → exact `path:line`.

## Lookup (always try this first)
1. Read `.claude/knowledge/INDEX.md` (at most 200 lines, grouped by kind, with keyword lines), or run
   `python3 .claude/tools/kg.py query <terms> [--limit N]`.
2. `python3 .claude/tools/kg.py show <id>` opens a node. `neighbors <id> --depth 2` shows related nodes.
3. Jump to the node's `path:line` pointers with ranged Reads.
4. If the KG has no answer, search normally, then report `KG: gap <topic>` in your return block.

## Node format (`.claude/knowledge/nodes/<id>.md`)
```markdown
---
id: module-billing
kind: module
title: Billing
summary: Invoice generation and payment capture via provider SDK
tags: [billing, invoice, payment, stripe]
links: [depends_on:data-model-invoice, calls:module-payments-client, decided_by:decision-idempotent-charges]
sources: [src/billing/service.py:1-140, src/billing/routes.py:10-58]
updated: 2026-10-24
verified_commit: 3f9c2ab
---
## Facts
- Invoices are created on order close, not on payment → src/billing/service.py:42-67
## Gotchas
- Charges use idempotency keys derived from invoice id → src/billing/service.py:98-104
```

- **id:** `<kind>-<slug>`, and the file name is `<id>.md`.
- **Frontmatter:** one `key: value` per line, inline lists, no nesting.
- **Kinds:** `architecture, module, entrypoint, api, data-model, workflow, command, decision, gotcha, doc, glossary`.
- **Link types:** `depends_on, implements, calls, documented_in, decided_by, related` (a bare id means `related`).
- **Body:** at most 60 lines, and every fact ends with `→ path:line` or `→ path:start-end`.

## What counts as "major" (only write these)
- **Include:** architecture and boundaries; module responsibilities; entry points; public APIs and contracts; data models and schemas; build/test/run commands; key decisions and **why**; non-obvious gotchas.
- **Exclude:** task progress, TODOs, temporary debugging notes, anything derivable in one grep, secrets, PII, and values of env vars (names only).

## Update protocol
Edits are recorded in `.claude/state/kg-pending` and batched (`knowledge.pending_trigger_count`, 5 files by default). When the Stop hook says `[harness:kg] ...`, wait for every other agent to finish, then delegate to **knowledge-keeper**, which runs:
```bash
python3 .claude/tools/kg.py pending                 # changed files + nodes that reference them
python3 .claude/tools/kg.py prune --apply           # remove nodes whose sources were all deleted
python3 .claude/tools/save.py node <id> <<'EOF'   # whole node: frontmatter + facts
---
id: <id>
...
EOF
python3 .claude/tools/kg.py index && python3 .claude/tools/kg.py validate
python3 .claude/tools/kg.py pending --clear
```
- Save nodes only with `save.py`: Claude Code blocks Write/Edit inside `.claude/`. It also scans for secrets/PII and stamps `updated` and `verified_commit`.
- Update existing nodes rather than creating near-duplicates. Fix stale pointers whenever you touch a node.
- **Keep it lean:** delete facts that no longer hold, and `kg.py remove <id>` nodes that describe something gone or duplicated. A smaller graph costs less context.
- `python3 .claude/tools/kg.py stale` lists nodes whose sources changed since they were verified.
- **Never hand-edit `INDEX.md` or `graph.json`.** They are generated.

More examples: [references/examples.md](references/examples.md).
