#!/usr/bin/env bash
# PreToolUse / Bash, filtered to `git commit` — scans the staged diff for secrets.
set -euo pipefail

cat >/dev/null # drain stdin; settings.json already matched the command

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
repo_root=$(cd -- "$script_dir/../.." &>/dev/null && pwd)

reason=$("$repo_root/scripts/check-staged-secrets.sh" 2>/dev/null) && ok=true || ok=false

if [[ "$ok" == false ]]; then
  jq -n --arg reason "$reason Unstage it (git restore --staged <file>), use an environment variable, or add it to .gitignore." \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
fi

echo '{"continue": true}'
