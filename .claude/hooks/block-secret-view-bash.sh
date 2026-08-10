#!/usr/bin/env bash
# PreToolUse / Bash — refuse shell commands that dump secret files or the full environment.
set -euo pipefail

input=$(cat)
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")

[[ -z "$cmd" ]] && { echo '{"continue": true}'; exit 0; }

view_bin='(cat|less|more|head|tail|bat|strings|xxd|hexdump|vim|vi|nvim|nano|pico|emacs|code|open|pbcopy)'
secret_file='(^|[[:space:]/])(\.env(\.[A-Za-z0-9_-]+)?|[^[:space:]]*\.pem|[^[:space:]]*\.key|id_(rsa|ed25519|ecdsa)(\.pub)?|credentials\.json|secrets?\.ya?ml|\.pgpass|\.netrc)([[:space:]]|$)'
allow_file='\.env\.(example|sample|template)'

blocked_reason=""

if echo "$cmd" | grep -qiE "^[[:space:]]*${view_bin}\b" && echo "$cmd" | grep -qiE "$secret_file" && ! echo "$cmd" | grep -qiE "$allow_file"; then
  blocked_reason="This command appears to read a secrets file directly. Secrets must never be loaded into the assistant's context — they come from environment variables at runtime only."
elif echo "$cmd" | grep -qiE '(^|[|;&]) *(printenv|env)[[:space:]]*($|[|>]|;|&&|\|\|)'; then
  blocked_reason="This command dumps the process environment, which may contain secrets (DB credentials, bearer token). Ask the user directly instead of reading env values."
elif echo "$cmd" | grep -qiE '\bset\b[[:space:]]*\|[[:space:]]*grep'; then
  blocked_reason="This command greps shell-exported variables, which may expose secrets. Ask the user directly instead."
fi

if [[ -n "$blocked_reason" ]]; then
  jq -n --arg reason "$blocked_reason" \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
fi

echo '{"continue": true}'
