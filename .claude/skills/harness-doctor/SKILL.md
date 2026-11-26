---
name: harness-doctor
description: Check the health of the installed Claude harness (dependencies, settings, hooks self-test, knowledge graph) and explain how to fix any problems.
disable-model-invocation: true
allowed-tools: Bash(python3 .claude/tools/doctor.py*)
---

# /harness-doctor: harness health check

1. Run: `python3 .claude/tools/doctor.py`
2. Summarize the result as a short table, `check · status · fix`. Show only failures and warnings in detail.
3. **Common fixes:**
   | Problem | Fix |
   |---|---|
   | Hooks missing from `.claude/settings.json` | Re-run the installer from the harness repo: `python3 ~/mkclaude/install.py DIR="<this project>"` |
   | Hook self-test failed | Re-run the installer (restores unmodified harness files). If a hook file was modified, use `--force` after reviewing the diff. |
   | `CLAUDE_CODE_DISABLE_FILE_MENTIONS` not set | Until it is set, files attached with `@` skip the secret scan. It only works from user/shell settings: re-run the installer with `--user-env`, or add it to `~/.claude/settings.json` `env` yourself. |
   | KG validation errors | Delegate to `knowledge-keeper` with the error list. |
   | Stale KG nodes | Delegate to `knowledge-keeper` with the output of `python3 .claude/tools/kg.py stale`. |
   | Python < 3.9 | Install a newer python3; the hooks require it. |
4. **Never edit** `.claude/settings.json`, `.claude/hooks/**`, `.claude/tools/**` or `.claude/security/**` yourself. They are protected; the user or the installer changes them.
