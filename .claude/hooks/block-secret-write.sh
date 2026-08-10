#!/usr/bin/env bash
# PreToolUse / Write, Edit — blocks secret files and hardcoded credentials.
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
  deny_path='(^|/)\.env(\..+)?$|\.pem$|\.key$|(^|/)id_(rsa|ed25519|ecdsa)$|credentials\.json$|(^|/)secrets?\.ya?ml$|\.pgpass$|(^|/)\.netrc$'
  if [[ "$path" =~ $deny_path ]] && ! [[ "$path" =~ $allow_path ]]; then
    deny "Secrets belong in environment variables, not files. '$path' is a secrets file."
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

# Placeholder values match as whole values only (no wildcards) — a real
# secret with a placeholder-word prefix still gets caught.
placeholder='changeme|change_me|change-me|placeholder|replaceme|replace_me|replace-me|yourpassword|your_password|your-password|yoursecret|your_secret|your-secret|yourtoken|your_token|your-token|xxxxxxxx|dummy|sample|fakepassword|fake_password|fake-password|fake|example|test|testing|password|secret'
safe_dsn_re="postgres(ql)?://[^:/@[:space:]]+:(${placeholder})@"
# A password segment that is a variable reference ($VAR / ${VAR} / $(VAR)) is
# interpolated at runtime, not a committed credential — same reasoning as the
# leading [^"'$] in assign_secret_re.
# The WHOLE password segment must be the reference — a literal that merely
# contains a '$' (Xk7$mQ9zLp) is still a hardcoded credential. Only the
# delimited forms count: a braceless $ecretSauce9 is indistinguishable from a
# real password that happens to start with '$'.
safe_dsn_var_re='postgres(ql)?://[^:/@[:space:]]+:(\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$\([A-Za-z_][A-Za-z0-9_]*\))@'
safe_bearer_re="Bearer[[:space:]]+(${placeholder})([^A-Za-z0-9._~+/-]|\$)"
safe_assign_secret_re="(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|bearer[_-]?token|db[_-]?pass)[[:space:]]*[:=]{1,2}[[:space:]]*[\"'](${placeholder})[\"']"

# Herestrings, never `echo | grep -q`: grep -q exits at its first match and
# SIGPIPEs the upstream echo, so under `pipefail` the pipeline returns 141 and
# the check silently passes. It only shows up once the content outgrows the pipe
# buffer, which makes it look intermittent.
if grep -qE -- "$private_key_re" <<<"$content"; then
  deny "This embeds a PEM private key literal."
fi
if grep -qE -- "$aws_key_re" <<<"$content"; then
  deny "This embeds an AWS access key ID."
fi
if [[ "$is_doc_file" == false ]]; then
  # -o so each DSN is judged on its own, not whichever one shares its line.
  if unsafe_dsn=$(grep -oE -- "$dsn_password_re" <<<"$content" | grep -viE -- "$safe_dsn_re" | grep -viE -- "$safe_dsn_var_re") && [[ -n "$unsafe_dsn" ]]; then
    deny "This embeds a database URL with a plaintext password. Use DATABASE_URL from the environment."
  fi
  if unsafe_bearer=$(grep -viE -- "$safe_bearer_re" <<<"$content" | grep -E -- "$bearer_re") && [[ -n "$unsafe_bearer" ]]; then
    deny "This embeds a literal Bearer token. Read the SCIM token from an environment variable."
  fi
  if [[ "$is_test_file" == false ]]; then
    if unsafe_assign=$(grep -Ei -- "$assign_secret_re" <<<"$content" | grep -viE -- "$safe_assign_secret_re") && [[ -n "$unsafe_assign" ]]; then
      deny "This hardcodes a credential literal. Read it from an environment variable."
    fi
  fi
fi

echo '{"continue": true}'
