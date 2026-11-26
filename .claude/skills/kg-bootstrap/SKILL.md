---
name: kg-bootstrap
description: Populate the project knowledge graph (.claude/knowledge) from the codebase, using knowledge-keeper agents in directory batches.
argument-hint: "[path-or-area to focus on]"
disable-model-invocation: true
allowed-tools: Bash(python3 .claude/tools/kg.py *), Bash(python3 .claude/tools/stack.py*), Bash(git rev-parse *), Bash(git log *), Bash(ls *)
---

# /kg-bootstrap: populate the knowledge graph

Focus area (optional): `$ARGUMENTS`

You coordinate; **knowledge-keeper** agents do the reading and writing. Keep your own context small.

## Steps
1. **Baseline.** Run:
   ```bash
   python3 .claude/tools/kg.py stats
   python3 .claude/tools/kg.py validate
   python3 .claude/tools/stack.py
   ls
   ```
   Read `.claude/knowledge/INDEX.md` to see what the installer already seeded.
2. **Plan batches.** List the top-level source directories, plus docs/ADRs, CI config and manifests. Make one batch per directory or service, sized so one agent reads at most ~30 files or ~3k lines. If a focus area was given, include only batches under it.
3. **Delegate.** For each batch, spawn a `knowledge-keeper` with this brief:
   - **Goal:** create or update nodes for *major info only*: architecture, module responsibilities, entry points, public APIs, data models, commands, key decisions (and why), gotchas.
   - **Scope:** `<dir>` only. Read entry files first and use ranged reads. No secrets, no PII, env var names only.
   - **Rules:**
     - Node format and linking per the `knowledge-graph` skill.
     - Every fact carries a `→ path:line` pointer.
     - At most 60 body lines per node; prefer updating over duplicating.
     - Save with `save.py node <id>`, which stamps `updated` and `verified_commit`.
   - **Finish with** `python3 .claude/tools/kg.py index && python3 .claude/tools/kg.py validate`, then return the STATUS block, listing node ids created/updated.
   - **Budget:** the context limit applies. On `[harness:context] LIMIT`, write a handoff and return `STATUS: HANDOFF <path>`.

   Run independent batches in parallel (up to 3 at a time). They write different nodes, so collisions are unlikely. `index` rewrites the shared INDEX.md, so after the parallel group finishes, run `kg.py index` once more yourself.
4. **Handoffs.** If any agent returns `STATUS: HANDOFF <path>`, spawn a fresh `knowledge-keeper` with: "Resume from handoff <path>. Read it first, then continue from 'Next first step'."
5. **Cross-cutting pass.** Run one final `knowledge-keeper` that:
   - writes or refreshes `architecture-overview`, linking module nodes with typed edges
   - adds `decision-*` nodes from ADRs or docs
   - adds `glossary-*` for domain terms
6. **Verify.** Run:
   ```bash
   python3 .claude/tools/kg.py index
   python3 .claude/tools/kg.py validate
   python3 .claude/tools/kg.py stats
   ```
   Fix any errors (delegate them).
7. **Report.** Give the node counts by kind, any gaps noticed, and remind the user to commit `.claude/knowledge/` (`INDEX.md`, `graph.json`, `nodes/`).
