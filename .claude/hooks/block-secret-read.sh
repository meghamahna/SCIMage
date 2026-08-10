#!/usr/bin/env bash
# PreToolUse / Read — refuse to load secret-shaped files into the model's context.
set -euo pipefail

input=$(cat)
path=$(jq -r '.tool_input.file_path // empty' <<<"$input")

[[ -z "$path" ]] && { echo '{"continue": true}'; exit 0; }

allow_regex='\.env\.(example|sample|template)$'
deny_regex='(^|/)\.env(\.[A-Za-z0-9_-]+)?$|\.pem$|\.key$|(^|/)id_(rsa|ed25519|ecdsa)$|(^|/)id_(rsa|ed25519|ecdsa)\.pub$|credentials\.json$|(^|/)secrets?\.ya?ml$|\.pgpass$|(^|/)\.netrc$|(^|/)\.aws/credentials$'

if [[ "$path" =~ $deny_regex ]] && ! [[ "$path" =~ $allow_regex ]]; then
  jq -n --arg reason "Blocked by project policy: '$path' looks like a secrets file. This project requires secrets to come from environment variables at runtime, never read or persisted by an assistant. If you need a value, ask the user to supply it or set it directly." \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
fi

echo '{"continue": true}'
