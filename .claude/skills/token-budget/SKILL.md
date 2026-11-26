---
name: token-budget
description: Token-budget-conscious working style. Look up the knowledge graph before searching, delegate exploration to cheap agents, use ranged reads, avoid re-reading, and keep every agent under its context limit (75% of the window by default). Use for any multi-step task, large codebase exploration, long sessions, or when a [harness:context] warning appears.
---

# Token budget

Context is the scarcest resource. Every agent must finish, or hand off, **before the context limit** (`context.limit_pct` in `.claude/harness.json`).

## Order of lookup (cheapest first)
1. **The knowledge graph.** Read `.claude/knowledge/INDEX.md`, or run `python3 .claude/tools/kg.py query <terms>`. Then open the node and jump to its `path:line` pointers.
2. **Targeted search.** Run Grep with `output_mode: files_with_matches` first, then `content` with `head_limit` and a narrow `glob`/`path`.
3. **Ranged reads.** Read with `offset`/`limit` around the pointer, not the whole file. Read whole files only when they are under ~200 lines or you are editing broadly.
4. **Delegate broad exploration** to `repo-explorer` (haiku), which returns conclusions and `path:line` pointers, not file dumps.

## Rules
- **Never re-read a file you already read** unless it changed since. Keep notes of what you learned instead.
- **Prefer diffs over rewrites.** Use Edit for small changes, and Write only for new files or full rewrites.
- **Summarize tool output.** Don't paste long logs back. Quote only the lines that matter, with pointers.
- **Batch independent tool calls** in one message, and run independent read-only subagents in parallel.
- **Keep messages between agents terse.** Give the result, the evidence pointers and the next step, without restating the task. The final report to the user is different: write it in full sentences for someone who saw none of the work.
- **Pick the right model:**
  - haiku for lookups, formatting and commit messages
  - sonnet for docs and diagrams
  - opus for coding, review and supervision
- **Bound outputs:** use `head -n`, `--max-count`, `| tail -n 50` on noisy commands (tests, builds), plus the test runner's quiet/fail-only flags.
- **Tests:** write the few that matter (test-driven-dev's test budget), run only the affected ones, and read only failures. Never paste a full test log into a reply.
- **Don't load what you don't need.** Skills load their `references/` only when required, and you should load them the same way.

## Context protocol (enforced by `context_guard.py`)
| Signal | Meaning | Do |
|---|---|---|
| `[harness:context] WARN NN%` | warn level reached | Finish the current unit. No large reads; delegate further exploration. |
| `[harness:context] LIMIT NN%` | limit reached | Stop now. Save a handoff with `python3 .claude/tools/save.py handoff <agent>`, then end with `STATUS: HANDOFF <path>`. |

- **Subagents:** the parent respawns a fresh agent of the same type from your handoff file.
- **Main session:** delegate the remainder to a fresh `orchestrator` with the handoff, or ask the user to `/clear` then `/resume-handoff <path>`.

## Self-check before a big step
- Can the KG answer this? Can a haiku agent answer it?
- Do I need the whole file, or 40 lines around a pointer?
- Will this command print thousands of lines? Then bound it.
