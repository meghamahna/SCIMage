#!/usr/bin/env python3
"""PostToolUse Bash|Grep|WebFetch|mcp__*: defense-in-depth redaction of tool output.

Pre-tool guards are the primary control. If something still slips into a result,
replace each matched value with ***REDACTED:<detector>*** while keeping the output's
exact JSON shape (required for built-in tools to accept the replacement).
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
from common import PREFIX, emit, load_config, scan_local, project_dir, run_soft  # noqa: E402
from detectors import Allowlist, redact, summarize  # noqa: E402


def _walk(obj, fn):
    if isinstance(obj, str):
        return fn(obj)
    if isinstance(obj, dict):
        return {k: _walk(v, fn) for k, v in obj.items()}
    if isinstance(obj, list):
        return [_walk(v, fn) for v in obj]
    return obj


def main(event):
    proj = project_dir(event)
    cfg = load_config(proj)["scan"]
    allow = Allowlist.load(proj)
    seen = []

    def fix(s):
        f = scan_local(s, allow, cfg)
        if f:
            seen.extend(f)
            return redact(s, f)
        return s

    resp = event.get("tool_response")
    if resp is None:
        return
    new = _walk(resp, fix)
    if seen:
        emit({"hookSpecificOutput": {
            "hookEventName": "PostToolUse",
            "updatedToolOutput": new,
            "additionalContext": (f"{PREFIX} Tool output contained {summarize(seen)}; values were redacted. "
                                  "Do not attempt to recover them. If any unredacted value is visible, "
                                  "do not repeat, store, or transmit it."),
        }})


if __name__ == "__main__":
    run_soft(main)
