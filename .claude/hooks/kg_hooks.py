#!/usr/bin/env python3
"""Knowledge-graph upkeep hooks.

  kg_hooks.py track  (PostToolUse Write|Edit|MultiEdit|NotebookEdit) -> record edited paths
  kg_hooks.py stop   (Stop)  -> once a batch of edits is pending (or nodes are obsolete), block once
                                and ask the main agent to delegate a KG update to knowledge-keeper.
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "lib"))
sys.path.insert(0, os.path.join(HERE, "..", "tools"))
from common import emit, load_config, project_dir, rel_to, run_soft, state_dir  # noqa: E402
import kg as kgmod  # noqa: E402


def track(event):
    proj = project_dir(event)
    ti = event.get("tool_input") or {}
    path = ti.get("file_path") or ti.get("notebook_path")
    if not path:
        return
    rel = rel_to(proj, path)
    if rel.startswith("..") or rel.startswith(".claude/"):
        return
    with open(os.path.join(state_dir(proj), "kg-pending"), "a", encoding="utf-8") as fh:
        fh.write(rel + "\n")


def stop(event):
    reason = stop_reason(event)
    if reason:
        emit({"decision": "block", "reason": reason})
        record_reported()


_TO_RECORD = []  # state to save once the request has actually been sent


def _save_reported(path, asked_at, asked_obsolete):
    try:
        with open(path, "w") as fh:
            json.dump({"pending": asked_at, "obsolete": sorted(asked_obsolete)}, fh)
    except OSError:
        pass


def record_reported():
    """Called after the Stop message was emitted, so a request that never reached the agent is asked again."""
    while _TO_RECORD:
        _save_reported(*_TO_RECORD.pop())


def _load_reported(path):
    try:
        with open(path) as fh:
            st = json.load(fh)
        return int(st.get("pending") or 0), set(st.get("obsolete") or [])
    except (OSError, ValueError, TypeError, AttributeError):
        return 0, set()


def stop_reason(event):
    """The [harness:kg] instruction when pending edits matter, else None.

    Edits are batched: a turn is held back only when `pending_trigger_count` edited files are waiting,
    or when a node's sources were deleted. Each batch is asked about once: if the agent leaves it, the
    next request waits for another full batch or a newly obsolete node.
    """
    if event.get("stop_hook_active"):
        return None  # already continued once for this stop; never loop
    proj = project_dir(event)
    cfg = load_config(proj)["knowledge"]
    trigger = max(1, int(cfg["pending_trigger_count"]))
    kg = kgmod.KG(proj)
    pending = [p for p in kgmod.read_pending(kg) if not kgmod.is_ignored(kg, p)]
    obsolete = kgmod.obsolete_nodes(kg)
    sfile = os.path.join(state_dir(proj), "kg-reported.json")
    asked_at, asked_obsolete = _load_reported(sfile)
    if len(pending) < asked_at:  # the queue was cleared since the last request
        asked_at = 0
    asked_obsolete &= set(obsolete)  # pruned nodes drop out
    new_obsolete = [o for o in obsolete if o not in asked_obsolete]
    due = new_obsolete or len(pending) >= asked_at + trigger
    if not due:
        _save_reported(sfile, asked_at, asked_obsolete)
        return None
    _TO_RECORD.append((sfile, len(pending), set(obsolete)))
    parts = []
    if pending:
        parts.append("Changed: " + ", ".join(pending[:12]) + (f" (+{len(pending) - 12} more)" if len(pending) > 12 else ""))
    if obsolete:
        parts.append("Obsolete nodes (sources deleted): " + ", ".join(obsolete[:12]))
    return (
        "[harness:kg] Knowledge graph update needed before finishing. " + ". ".join(parts) + ". "
        "Run it last: if other agents (including background ones) are still working, wait for all of them to "
        "return first, and never start it in parallel with another agent. Then delegate to the `knowledge-keeper` "
        "subagent with: \"Update the KG for pending changes "
        "(run `python3 .claude/tools/kg.py pending` and `kg.py prune`). Record only major info (architecture, modules, "
        "entry points, APIs, data models, commands, decisions, gotchas) with path:line pointers, remove what is "
        "obsolete, then run `kg.py index && kg.py validate` and `kg.py pending --clear`.\" If nothing major changed "
        "and nothing is obsolete, just run `python3 .claude/tools/kg.py pending --clear`. Then finish your reply with a "
        "1-3 line recap of the outcome (not the full answer again) and one line on the KG update.")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    run_soft({"track": track, "stop": stop}.get(mode, lambda e: None))
