---
name: handoff
description: Write a context handoff note now (goal, done, remaining, pointers, decisions, next step) so a fresh agent or session can continue the work.
argument-hint: "[optional one-line task description]"
disable-model-invocation: true
allowed-tools: Bash(python3 .claude/tools/save.py handoff *)
---

# /handoff: write a context handoff note

The same procedure applies, and **must be followed**, whenever `[harness:context] LIMIT` appears.

## Steps
1. Use your agent name (`main` for the main session).
2. Save the note with `python3 .claude/tools/save.py handoff <agent> <<'EOF'` ... `EOF`. It writes `.claude/knowledge/handoffs/<agent>-<timestamp>.md` and prints the path. Claude Code blocks Write/Edit inside `.claude/`, so always use this command. Use exactly this structure:

```markdown
---
agent: <agent name or main>
created: <ISO timestamp>
context_pct: <last [harness:context] % if known, else unknown>
task: <one line; $ARGUMENTS if given>
---
## Goal
<What "done" means, including acceptance criteria.>

## Done
- [x] <completed step> → path:line

## Remaining
- [ ] <next steps, in order, each small enough for one agent>

## Key pointers
- path:line: why it matters; KG ids: <ids>

## Decisions made
- <decision>: <why> (so the successor doesn't relitigate it)

## Open questions
- <questions for the user or orchestrator>

## Next first step
<The single concrete action the successor should take first.>
```

3. **Rules:**
   - Keep it under ~80 lines. Use pointers, not pasted code, and no secrets or PII.
   - Include uncommitted-change context. List the modified files from `git status --short`.
   - Promote anything that is *major* project knowledge (not task state) into a KG node via knowledge-keeper, or note it under "Remaining".
4. **End your turn with the return block:**
```
STATUS: HANDOFF <path printed by save.py>
SUMMARY: <1-3 lines, outcome first; name anything unverified>
NEXT: spawn a fresh <same agent type> → "Resume from handoff <path>"
```
   In the main session, also tell the user they can run `/clear` and then `/resume-handoff <path>`.
