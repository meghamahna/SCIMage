---
name: safe-read
description: View a file with secret and PII values masked (***REDACTED:<detector>***). Use after a [harness:security] block on Read/Grep/Bash, or when you need the structure of a config/.env/credentials-bearing file without seeing its values.
argument-hint: "<path> [--lines a-b]"
allowed-tools: Bash(python3 .claude/tools/safe_read.py *)
---

# Safe read (redacted view)

Run:
```bash
python3 .claude/tools/safe_read.py <path> [--lines a-b]
```
The output keeps keys, structure and line numbers, and replaces every detected secret or PII value with `***REDACTED:<detector>***`.

## When to use
- A `[harness:security]` message blocked a Read, Grep or Bash command on a file.
- You need to know **which** variables or keys a config file defines, or how it is structured, without needing the values.

## Rules
- **Never try to bypass a block.** That includes other tools, `cat`/`grep`/`sed`, encoding tricks, copying the file, symlinks, or asking another agent to read it raw.
- Refer to secrets by **variable name** only, in code, docs and replies.
- If the task truly requires a value (for example, to debug a connection), ask the user to verify it themselves. Never ask them to paste it.
- If it reports `access denied`, the Bash sandbox is blocking a credential file such as `.env`. The file may well exist: never tell the user it is missing. Ask them to run `python3 .claude/tools/safe_read.py <path>` in their own terminal and share what they need.
- If a file is wrongly flagged (a false positive such as a test fixture), tell the user. Only they can add an entry to `.claude/security/allowlist` (`path:<glob>`, `sha256:<hash>` or `detector:<name>`).
