#!/usr/bin/env bash
# Scans `git diff --cached` for secret patterns. Shared by the Claude Code
# commit hook (.claude/hooks/block-secret-commit.sh) and the real git hook
# (.githooks/pre-commit).
#
# Exit 0, silent: clean. Exit 1, reason on stdout: a secret-shaped pattern
# was found.
set -euo pipefail

diff=$(git diff --cached 2>/dev/null || true)

[[ -z "$diff" ]] && exit 0

added_lines=$(echo "$diff" | grep -E '^\+' | grep -vE '^\+\+\+' || true)
added_lines_nodoc=$(echo "$diff" | awk '
  /^diff --git/ { skip = ($0 ~ /\.(md|mdx|txt)( |$)/) }
  /^\+/ && $0 !~ /^\+\+\+/ && !skip { print }
')

private_key_re='-----BEGIN ([A-Z]+ )?PRIVATE KEY-----'
aws_key_re='AKIA[0-9A-Z]{16}'
dsn_password_re='postgres(ql)?://[^:/@[:space:]]+:[^@/[:space:]]+@'
bearer_re='Bearer[[:space:]]+[A-Za-z0-9._~+/-]{20,}'
assign_secret_re='(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|bearer[_-]?token|db[_-]?pass)[[:space:]]*[:=]{1,2}[[:space:]]*["'"'"'][^"'"'"'$][^"'"'"']{5,}["'"'"']'
env_file_re='^\+\+\+ b/(.*/)?\.env(\.[A-Za-z0-9_-]+)?$'
env_file_allow_re='\.env\.(example|sample|template)$'

# Placeholder values match as whole values only (no wildcards) — a real
# secret with a placeholder-word prefix still gets caught.
placeholder='changeme|change_me|change-me|placeholder|replaceme|replace_me|replace-me|yourpassword|your_password|your-password|yoursecret|your_secret|your-secret|yourtoken|your_token|your-token|xxxxxxxx|dummy|sample|fakepassword|fake_password|fake-password|fake|example|test|testing|password|secret'
safe_dsn_re="postgres(ql)?://[^:/@[:space:]]+:(${placeholder})@"
safe_bearer_re="Bearer[[:space:]]+(${placeholder})([^A-Za-z0-9._~+/-]|\$)"
safe_assign_secret_re="(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|bearer[_-]?token|db[_-]?pass)[[:space:]]*[:=]{1,2}[[:space:]]*[\"'](${placeholder})[\"']"

reason=""
if echo "$diff" | grep -qE -- "$env_file_re" && ! echo "$diff" | grep -qE -- "$env_file_allow_re"; then
  reason="Staging a .env file. Secrets belong in the environment at runtime, not commits."
elif echo "$added_lines" | grep -qE -- "$private_key_re"; then
  reason="Staged diff contains a PEM private key."
elif echo "$added_lines" | grep -qE -- "$aws_key_re"; then
  reason="Staged diff contains an AWS access key ID."
elif echo "$added_lines_nodoc" | grep -E -- "$dsn_password_re" | grep -qviE -- "$safe_dsn_re"; then
  reason="Staged diff contains a database URL with a plaintext password."
elif unsafe_bearer=$(echo "$added_lines_nodoc" | grep -viE -- "$safe_bearer_re" || true) && [[ -n "$unsafe_bearer" ]] && echo "$unsafe_bearer" | grep -qE -- "$bearer_re"; then
  reason="Staged diff contains a literal Bearer token."
elif echo "$added_lines_nodoc" | grep -Ei -- "$assign_secret_re" | grep -qviE -- "$safe_assign_secret_re"; then
  reason="Staged diff contains a hardcoded credential (password/secret/api key)."
fi

if [[ -n "$reason" ]]; then
  echo "$reason"
  exit 1
fi

exit 0
