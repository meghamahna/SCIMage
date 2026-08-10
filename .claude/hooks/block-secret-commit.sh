#!/usr/bin/env bash
# PreToolUse / Bash, filtered to `git commit` — scan the staged diff for obvious secret patterns.
set -euo pipefail

cat >/dev/null # drain stdin, we don't need the payload beyond the "if" match already done by settings.json

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
repo_root=$(cd -- "$script_dir/../.." &>/dev/null && pwd)

reason=$("$repo_root/scripts/check-staged-secrets.sh" 2>/dev/null) && ok=true || ok=false

if [[ "$ok" == false ]]; then
  jq -n --arg reason "$reason Unstage it (git restore --staged <file>) and use an environment variable instead, or add the file to .gitignore." \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
fi

echo '{"continue": true}'
