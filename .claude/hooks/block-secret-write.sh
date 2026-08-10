#!/usr/bin/env bash
# PreToolUse / Write, Edit — refuse to persist secret-shaped files or hardcoded credentials.
set -euo pipefail

input=$(cat)
tool=$(jq -r '.tool_name' <<<"$input")
path=$(jq -r '.tool_input.file_path // empty' <<<"$input")

deny() {
  jq -n --arg reason "$1" \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $reason}}'
  exit 0
}

if [[ -n "$path" ]]; then
  allow_path='\.env\.(example|sample|template)$'
  deny_path='(^|/)\.env(\.[A-Za-z0-9_-]+)?$|\.pem$|\.key$|(^|/)id_(rsa|ed25519|ecdsa)$|credentials\.json$|(^|/)secrets?\.ya?ml$|\.pgpass$|(^|/)\.netrc$'
  if [[ "$path" =~ $deny_path ]] && ! [[ "$path" =~ $allow_path ]]; then
    deny "Blocked by project policy: writing to '$path' would create/modify a secrets file. Secrets in this project come from environment variables set at runtime only — never files."
  fi
fi

if [[ "$tool" == "Write" ]]; then
  content=$(jq -r '.tool_input.content // empty' <<<"$input")
elif [[ "$tool" == "Edit" ]]; then
  content=$(jq -r '.tool_input.new_string // empty' <<<"$input")
else
  content=""
fi

[[ -z "$content" ]] && { echo '{"continue": true}'; exit 0; }

is_test_file=false
[[ "$path" =~ (_test\.go|/testdata/) ]] && is_test_file=true
is_doc_file=false
[[ "$path" =~ \.(md|mdx|txt)$ ]] && is_doc_file=true

private_key_re='-----BEGIN ([A-Z]+ )?PRIVATE KEY-----'
aws_key_re='AKIA[0-9A-Z]{16}'
dsn_password_re='postgres(ql)?://[^:/@[:space:]]+:[^@/[:space:]]+@'
bearer_re='Bearer[[:space:]]+[A-Za-z0-9._~+/-]{20,}'
assign_secret_re='(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|bearer[_-]?token|db[_-]?pass)[[:space:]]*[:=]{1,2}[[:space:]]*["'"'"'][^"'"'"'$][^"'"'"']{5,}["'"'"']'

if echo "$content" | grep -qE -- "$private_key_re"; then
  deny "Blocked by project policy: this change embeds a PEM private key literal in a tracked file."
fi
if echo "$content" | grep -qE -- "$aws_key_re"; then
  deny "Blocked by project policy: this change embeds what looks like an AWS access key ID in a tracked file."
fi
if [[ "$is_doc_file" == false ]]; then
  if echo "$content" | grep -qE -- "$dsn_password_re"; then
    deny "Blocked by project policy: this change embeds a database connection string with a plaintext password. Use DATABASE_URL from the environment instead."
  fi
  if echo "$content" | grep -qE -- "$bearer_re"; then
    deny "Blocked by project policy: this change embeds a literal Bearer token. The SCIM bearer token must come from an environment variable, never hardcoded."
  fi
  if [[ "$is_test_file" == false ]] && echo "$content" | grep -Eiq -- "$assign_secret_re"; then
    deny "Blocked by project policy: this change hardcodes what looks like a credential (password/secret/api key) as a literal string. Read it from an environment variable instead."
  fi
fi

echo '{"continue": true}'
