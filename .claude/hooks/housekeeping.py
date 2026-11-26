#!/usr/bin/env python3
"""Housekeeping hooks for agent-created files.

  housekeeping.py pre   (PreToolUse Write|NotebookEdit) -> record files an agent is about to create
`stop_reason()` is used by stop_checks.py to ask for cleanup of temporary-looking leftovers.
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "lib"))
sys.path.insert(0, os.path.join(HERE, "..", "tools"))
from common import project_dir, rel_to, run_soft, state_dir  # noqa: E402
import cleanup  # noqa: E402


def pre(event):
    proj = project_dir(event)
    ti = event.get("tool_input") or {}
    path = ti.get("file_path") or ti.get("notebook_path")
    if not path:
        return
    full = path if os.path.isabs(path) else os.path.join(proj, path)
    if os.path.exists(full):
        return
    r = rel_to(proj, path)
    if r.startswith(".."):
        return
    with open(os.path.join(state_dir(proj), "created-files"), "a", encoding="utf-8") as fh:
        fh.write(r + "\n")


_TO_RECORD = []  # reported files to save once the request has actually been sent


def record_reported():
    while _TO_RECORD:
        path, names = _TO_RECORD.pop()
        try:
            with open(path, "w", encoding="utf-8") as fh:
                fh.write("".join(r + "\n" for r in names))
        except OSError:
            pass


def stop_reason(event):
    """The [harness:cleanup] instruction when temporary-looking leftovers exist, else None."""
    if event.get("stop_hook_active"):
        return None
    proj = project_dir(event)
    c = cleanup.candidates(proj, True)
    # Each leftover is raised once: a file the agent chose to keep is not asked about again.
    sfile = os.path.join(state_dir(proj), "cleanup-reported")
    try:
        with open(sfile, encoding="utf-8") as fh:
            reported = {l.strip() for l in fh if l.strip()}
    except OSError:
        reported = set()
    # A file that was removed and later made again is raised again.
    reported = {r for r in reported if os.path.exists(os.path.join(proj, r))}
    temp = [r for r in c["created_temporary"] if r not in reported]
    stray = [r for r in c["untracked_since_session"] if cleanup.looks_temporary(r) and r not in reported]
    _TO_RECORD[:] = [(sfile, sorted(reported | set(temp) | set(stray)))]
    if not temp and not stray:
        return None
    parts = []
    if temp:
        parts.append("python3 .claude/tools/cleanup.py remove " + " ".join(temp[:10]))
    if stray:
        parts.append("python3 .claude/tools/cleanup.py remove --untracked " + " ".join(stray[:10]))
    return ("[harness:cleanup] Files left over from this work look temporary: " + ", ".join((temp + stray)[:10]) +
            ". Keep any that are part of the change (real tests and code stay). Remove the rest, which moves them "
            "to a restorable trash: " + " ; ".join(parts) + ". Next time, run throwaway code inline (`python3 - <<'EOF'`) or save it with "
            "`python3 .claude/tools/save.py scratch <name>`.")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    run_soft({"pre": pre}.get(mode, lambda e: None))
