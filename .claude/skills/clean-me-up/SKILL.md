---
name: clean-me-up
description: Clean up leftovers in one go. Removes temporary files agents created, scratch files, obsolete knowledge-graph nodes, and old trash and hand-off notes. Files tracked by git are never touched, and removed files stay restorable.
disable-model-invocation: true
allowed-tools: Bash(python3 .claude/tools/cleanup.py *), Bash(python3 .claude/tools/kg.py *)
---

# /clean-me-up: clear leftovers

1. **List.** Run `python3 .claude/tools/cleanup.py candidates`.
2. **Remove the leftovers.** Remove scratch files and files that look temporary (debug scripts, sample output, `tmp_*`, logs) with `python3 .claude/tools/cleanup.py remove <path>...`. Add `--untracked` for files a script made this session. Keep anything that is part of real work (source, tests, docs). If you can't tell, list it and ask the user instead of removing it.
3. **Empty old trash.** Run `python3 .claude/tools/cleanup.py purge`. It drops trash, scratch files and hand-off notes past their age limits in `.claude/harness.json`.
4. **Trim the knowledge graph.** Run `python3 .claude/tools/kg.py prune --apply`, then `python3 .claude/tools/kg.py validate`.
5. **Report** as a short list:
   - removed: each path (restorable with `python3 .claude/tools/cleanup.py restore <path>` until the trash is emptied)
   - knowledge graph: the nodes pruned, and the validate result
   - kept: anything you left in place, and why

Never use `rm` or any other delete command (they are blocked), and never touch files git tracks.
