#!/usr/bin/env bash
# Scans `git diff --cached` for obvious secret patterns. Shared by the
# Claude Code commit hook (.claude/hooks/block-secret-commit.sh) and the
# real git hook (.githooks/pre-commit) so the rules live in one place.
#
# Exit 0 and silent: clean. Exit 1 with a reason on stdout: a secret-shaped
# pattern was found in the staged diff.
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

reason=""
if echo "$diff" | grep -qE -- "$env_file_re" && ! echo "$diff" | grep -qE -- "$env_file_allow_re"; then
  reason="Staging a .env file. Secrets belong in the environment at runtime, never committed."
elif echo "$added_lines" | grep -qE -- "$private_key_re"; then
  reason="Staged diff contains a PEM private key."
elif echo "$added_lines" | grep -qE -- "$aws_key_re"; then
  reason="Staged diff contains what looks like an AWS access key ID."
elif echo "$added_lines_nodoc" | grep -qE -- "$dsn_password_re"; then
  reason="Staged diff contains a database URL with a plaintext password."
elif echo "$added_lines_nodoc" | grep -qE -- "$bearer_re"; then
  reason="Staged diff contains a literal Bearer token."
elif echo "$added_lines_nodoc" | grep -Eiq -- "$assign_secret_re"; then
  reason="Staged diff contains what looks like a hardcoded credential (password/secret/api key)."
fi

if [[ -n "$reason" ]]; then
  echo "$reason"
  exit 1
fi

exit 0
