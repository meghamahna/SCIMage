---
name: resume-handoff
description: Resume work from a context handoff note in .claude/knowledge/handoffs/.
argument-hint: "<path to handoff file> (omit for the most recent)"
disable-model-invocation: true
allowed-tools: Bash(ls -t .claude/knowledge/handoffs*), Bash(python3 .claude/tools/kg.py *)
---

# /resume-handoff: continue from a handoff note

Handoff file: `$ARGUMENTS`

## Steps
1. If no path was given, pick the newest file: `ls -t .claude/knowledge/handoffs/*.md | head -1`.
2. Read the handoff file **first and in full**.
3. Load **only** what it points to:
   - its listed KG ids, via `python3 .claude/tools/kg.py show <id>`
   - its `path:line` pointers, via ranged Reads

   Don't re-explore the codebase. The previous agent already paid that cost.
4. **Respect "Decisions made".** Don't relitigate them unless they are clearly wrong; if one is, say so explicitly.
5. Surface "Open questions" to the user (or the orchestrator) before work that depends on them.
6. Start from **"Next first step"**. Work through "Remaining", delegating per `AGENTS.md`, and run independent steps in parallel.
7. When you finish:
   - leave the handoff file as it is (it is cleaned up automatically); name it in your SUMMARY as completed, or as handed off again with the new path
   - return the standard STATUS block

The context limit still applies. If you hit `[harness:context] LIMIT`, save a new handoff with `python3 .claude/tools/save.py handoff <agent>` and chain it.
