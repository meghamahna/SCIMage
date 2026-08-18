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
# The credential/DSN/bearer checks run against first-party source only. Docs
# (.md/.mdx/.txt) are skipped because they quote example values, and the
# vendored Swagger UI bundle (docs/swaggerui/, a pinned third-party minified
# library — provenance in docs/swaggerui/VERSION) is skipped because its
# minified code contains field names like clientSecret:"..." that match the
# credential regex with no real secret behind them. First-party code is still
# fully scanned; the PEM/AWS-key checks below run against every file regardless.
added_lines_nodoc=$(echo "$diff" | awk '
  /^diff --git/ { skip = ($0 ~ /\.(md|mdx|txt)( |$)/ || $0 ~ /docs\/swaggerui\//) }
  /^\+/ && $0 !~ /^\+\+\+/ && !skip { print }
')

private_key_re='-----BEGIN ([A-Z]+ )?PRIVATE KEY-----'
aws_key_re='AKIA[0-9A-Z]{16}'
dsn_password_re='postgres(ql)?://[^:/@[:space:]]+:[^@/[:space:]]+@'
bearer_re='Bearer[[:space:]]+[A-Za-z0-9._~+/-]{20,}'
assign_secret_re='(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|bearer[_-]?token|db[_-]?pass)[[:space:]]*[:=]{1,2}[[:space:]]*["'"'"'][^"'"'"'$][^"'"'"']{5,}["'"'"']'
env_file_path_re='(^|/)\.env(\..+)?$'
env_file_allow_re='\.env\.(example|sample|template)$'

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

# Herestrings, never `echo | grep -q`. `grep -q` exits at its first match and
# SIGPIPEs the upstream echo, so under `pipefail` the pipeline returns 141 —
# which reads as "no match". That fails open for the secret checks and, inside
# the negated allow-check below, fails closed on a legitimate .env.example.
# Whether it happens depends on the diff outgrowing the pipe buffer, so it looks
# intermittent. A herestring is a file descriptor, not a pipe, so there is no
# reader to disappear.
#
# Where a filter chain is needed, the result is captured and then tested rather
# than ending in `grep -q`, for the same reason.
# Env files are judged one path at a time. A diff-wide allow-check would let a
# real .env through whenever .env.example happened to be staged in the same
# commit — which is exactly what `git add -A` after `cp .env.example .env` does.
staged_env_files=$(grep -E '^\+\+\+ b/' <<<"$diff" | sed 's|^+++ b/||' | grep -E -- "$env_file_path_re" || true)
unsafe_env_files=$(grep -vE -- "$env_file_allow_re" <<<"$staged_env_files" || true)

reason=""
if [[ -n "$unsafe_env_files" ]]; then
  reason="Staging a .env file ($(tr '\n' ' ' <<<"$unsafe_env_files")). Secrets belong in the environment at runtime, not commits."
elif grep -qE -- "$private_key_re" <<<"$added_lines"; then
  reason="Staged diff contains a PEM private key."
elif grep -qE -- "$aws_key_re" <<<"$added_lines"; then
  reason="Staged diff contains an AWS access key ID."
elif unsafe_dsn=$(grep -oE -- "$dsn_password_re" <<<"$added_lines_nodoc" | grep -viE -- "$safe_dsn_re" | grep -viE -- "$safe_dsn_var_re") && [[ -n "$unsafe_dsn" ]]; then
  reason="Staged diff contains a database URL with a plaintext password."
elif unsafe_bearer=$(grep -viE -- "$safe_bearer_re" <<<"$added_lines_nodoc" | grep -E -- "$bearer_re") && [[ -n "$unsafe_bearer" ]]; then
  reason="Staged diff contains a literal Bearer token."
elif unsafe_assign=$(grep -Ei -- "$assign_secret_re" <<<"$added_lines_nodoc" | grep -viE -- "$safe_assign_secret_re") && [[ -n "$unsafe_assign" ]]; then
  reason="Staged diff contains a hardcoded credential (password/secret/api key)."
fi

if [[ -n "$reason" ]]; then
  echo "$reason"
  exit 1
fi

exit 0
