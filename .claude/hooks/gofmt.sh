#!/usr/bin/env bash
# PostToolUse / Write, Edit — auto-format Go files that were just written.
set -euo pipefail

input=$(cat)
path=$(jq -r '.tool_response.filePath // .tool_input.file_path // empty' <<<"$input")

[[ "$path" != *.go ]] && exit 0
[[ ! -f "$path" ]] && exit 0

if command -v gofmt >/dev/null 2>&1; then
  gofmt -w "$path" 2>/dev/null || true
fi
if command -v goimports >/dev/null 2>&1; then
  goimports -w "$path" 2>/dev/null || true
fi

exit 0
