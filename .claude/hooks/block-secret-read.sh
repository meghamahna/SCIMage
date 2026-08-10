#!/usr/bin/env bash
# PreToolUse / Read — blocks reading secret-shaped files.
set -euo pipefail

input=$(cat)
path=$(jq -r '.tool_input.file_path // empty' <<<"$input")

[[ -z "$path" ]] && { echo '{"continue": true}'; exit 0; }

allow_regex='\.env\.(example|sample|template)$'
deny_regex='(^|/)\.env(\.[A-Za-z0-9_-]+)?$|\.pem$|\.key$|(^|/)id_(rsa|ed25519|ecdsa)$|(^|/)id_(rsa|ed25519|ecdsa)\.pub$|credentials\.json$|(^|/)secrets?\.ya?ml$|\.pgpass$|(^|/)\.netrc$|(^|/)\.aws/credentials$'

if [[ "$path" =~ $deny_regex ]] && ! [[ "$path" =~ $allow_regex ]]; then
  jq -n --arg reason "'$path' is a secrets file. Secrets come from environment variables at runtime — ask the user for the value directly." \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
fi

echo '{"continue": true}'
