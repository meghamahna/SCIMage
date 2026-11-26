#!/usr/bin/env python3
"""PostToolUse Agent|Task: when a subagent returns `STATUS: HANDOFF <path>`, tell the parent to respawn."""
import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
from common import add_context, project_dir, run_soft, state_dir  # noqa: E402

STATUS_RE = re.compile(r"STATUS:\s*HANDOFF\s+(\S+)")


def main(event):
    text = json.dumps(event.get("tool_response"), ensure_ascii=False) if not isinstance(
        event.get("tool_response"), str) else event["tool_response"]
    m = STATUS_RE.search(text or "")
    if not m:
        return
    path = m.group(1).strip("`'\".,")
    ti = event.get("tool_input") or {}
    agent = ti.get("subagent_type") or "general-purpose"
    proj = project_dir(event)
    cfile = os.path.join(state_dir(proj), "handoff-chain.json")
    try:
        with open(cfile) as fh:
            chain = json.load(fh)
    except (OSError, ValueError):
        chain = {}
    chain[agent] = chain.get(agent, 0) + 1
    with open(cfile, "w") as fh:
        json.dump(chain, fh)
    if path == "pending-parent-write":
        step = ("The subagent could not write files: first save the handoff content from its SUMMARY with "
                f"`python3 .claude/tools/save.py handoff {agent} <<'EOF'` ... `EOF`, then ")
        path = "<the path it prints>"
    else:
        step = ""
    warn = (" This agent type has handed off "
            f"{chain[agent]} times; consider splitting the task smaller." if chain[agent] >= 3 else "")
    add_context("PostToolUse", (
        f"[harness:handoff] `{agent}` hit its context budget and handed off ({path}). {step}spawn a FRESH "
        f"`{agent}` subagent with prompt: \"Resume from handoff {path}. Read it first, then only the KG "
        f"nodes/pointers it lists. Continue from 'Next first step'.\" Do not continue the work yourself.{warn}"))


if __name__ == "__main__":
    run_soft(main)
