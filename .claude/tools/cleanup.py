#!/usr/bin/env python3
"""Garbage cleanup for files agents create. Deletes nothing outright: files move to a trash folder.

Agents may only remove what they made:
  - anything in .claude/scratch/ (the place for throwaway files)
  - files an agent created this session (recorded by the housekeeping hook) that git does not track
  - with --untracked: untracked files that appeared since the session started (e.g. made by a script)
  - build output: a git-ignored folder named in cleanup.build_dirs (dist/, node_modules/, ...) with no tracked files
    Other git-ignored paths (local databases, uploads, local config) are not build output and stay.
Never: tracked files, sensitive files, harness files, or anything outside the project.

Usage:
  python3 .claude/tools/cleanup.py candidates            what the agent may remove, temporary-looking first
  python3 .claude/tools/cleanup.py remove <path>... [--untracked]
  python3 .claude/tools/cleanup.py scratch [--older-than HOURS]
  python3 .claude/tools/cleanup.py restore <path|all>
  python3 .claude/tools/cleanup.py purge                 empty trash batches older than cleanup.trash_days
"""
from __future__ import annotations

import fnmatch
import json
import os
import shutil
import subprocess
import sys
import time
from typing import Dict, List, Optional

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "hooks", "lib"))
from detectors import sensitive_path_reason  # noqa: E402

TEMP_PATTERNS = ["tmp*", "temp*", "scratch*", "debug*", "*.tmp", "*.log", "*.bak", "*.orig", "*.rej", "*~",
                 "out.*", "output.*", "*.out", "test_output*", "repro*", "try_*", "*.pyc", "nohup.out"]
TEMP_DIRS = {"tmp", "temp", ".tmp"}
PROTECTED_PREFIXES = (".claude/settings", ".claude/harness.json", ".claude/hooks/", ".claude/tools/",
                      ".claude/security/", ".claude/agents/", ".claude/skills/", ".claude/knowledge/",
                      ".claude/state/", ".claude/.harness-manifest.json", ".git/")
DEFAULTS = {"trash_days": 7, "scratch_hours": 24, "handoff_days": 7,
            # Folders that hold only output a build or test run can make again.
            "build_dirs": ["node_modules", "dist", "build", ".next", "target", "__pycache__", ".pytest_cache",
                           ".mypy_cache", ".ruff_cache", ".tox", "coverage", "htmlcov"]}


def project_root() -> str:
    return os.environ.get("CLAUDE_PROJECT_DIR") or os.path.abspath(os.path.join(HERE, "..", ".."))


def config(root: str) -> Dict:
    try:
        with open(os.path.join(root, ".claude", "harness.json")) as fh:
            return {**DEFAULTS, **json.load(fh).get("cleanup", {})}
    except (OSError, ValueError):
        return dict(DEFAULTS)


def state(root: str, name: str) -> str:
    d = os.path.join(root, ".claude", "state")
    os.makedirs(d, exist_ok=True)
    return os.path.join(d, name)


def rel(root: str, path: str) -> Optional[str]:
    ap = os.path.realpath(os.path.join(root, os.path.expanduser(path)))
    r = os.path.relpath(ap, os.path.realpath(root)).replace(os.sep, "/")
    return None if r.startswith("..") else r


def git(root: str, *args: str) -> Optional[str]:
    try:
        r = subprocess.run(["git", "-C", root, *args], capture_output=True, text=True, timeout=15)
        return r.stdout if r.returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired):
        return None


def tracked(root: str) -> Optional[set]:
    out = git(root, "ls-files", "-z")
    return None if out is None else set(filter(None, out.split("\0")))


def untracked(root: str) -> set:
    out = git(root, "ls-files", "--others", "--exclude-standard", "-z")
    return set(filter(None, (out or "").split("\0")))


def ignored_build_output(root: str, r: str) -> bool:
    """True when `r` can be made again, so it may go to the trash without the session ledger: git ignores it,
    nothing under it is tracked, and it is (or sits inside) a folder named in cleanup.build_dirs, or it is a
    temporary-looking file written since the session started.

    Being git-ignored is not enough on its own. Local databases, upload folders and local config are ignored
    too, and nothing can rebuild those."""
    full = os.path.join(root, r)
    in_build_dir = bool(set(config(root)["build_dirs"]) & set(r.split("/")))
    fresh_temp = looks_temporary(r) and os.path.isfile(full) and os.path.getmtime(full) >= session_start(root) - 1
    if not (in_build_dir or fresh_temp):
        return False
    if not git(root, "check-ignore", "--", r):
        return False
    return not (git(root, "ls-files", "--", r) or "").strip()


def ledger(root: str) -> List[str]:
    try:
        with open(state(root, "created-files")) as fh:
            return sorted({l.strip() for l in fh if l.strip()})
    except OSError:
        return []


def session_start(root: str) -> float:
    try:
        with open(state(root, "session-start")) as fh:
            return float(fh.read().strip())
    except (OSError, ValueError):
        return time.time()


def looks_temporary(r: str) -> bool:
    name = r.rsplit("/", 1)[-1]
    return any(fnmatch.fnmatch(name, p) for p in TEMP_PATTERNS) or any(part in TEMP_DIRS for part in r.split("/")[:-1])


def refusal(root: str, r: Optional[str], allowed: set, track: Optional[set]) -> Optional[str]:
    if r is None:
        return "outside the project"
    if r.startswith(".claude/scratch/"):
        return None
    if r.startswith(PROTECTED_PREFIXES):
        return "harness file"
    if sensitive_path_reason(r):
        return "sensitive file"
    if track is not None and r in track:
        return "tracked by git (only a person deletes tracked files)"
    if ignored_build_output(root, r):
        return None
    if r not in allowed:
        return "not created by an agent in this session"
    return None


def candidates(root: str, include_untracked: bool = False) -> Dict[str, List[str]]:
    track = tracked(root)
    scratch_dir = os.path.join(root, ".claude", "scratch")
    scratch = []
    for dp, _, fn in os.walk(scratch_dir):
        scratch += [rel(root, os.path.join(dp, f)) for f in fn]
    created = [r for r in ledger(root) if os.path.isfile(os.path.join(root, r)) and
               refusal(root, r, set(ledger(root)), track) is None]
    since = session_start(root)
    new_untracked = sorted(r for r in untracked(root) if r not in created and not r.startswith(".claude/")
                           and os.path.isfile(os.path.join(root, r))
                           and os.path.getmtime(os.path.join(root, r)) >= since - 1
                           and not sensitive_path_reason(r))
    return {"scratch": sorted(filter(None, scratch)),
            "created_temporary": [r for r in created if looks_temporary(r)],
            "created_other": [r for r in created if not looks_temporary(r)],
            "untracked_since_session": new_untracked}


def to_trash(root: str, r: str, batch: str) -> str:
    dest = os.path.join(root, ".claude", "state", "trash", batch, r)
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    shutil.move(os.path.join(root, r), dest)
    parent = os.path.dirname(os.path.join(root, r))
    while os.path.realpath(parent) != os.path.realpath(root):  # drop folders the move left empty
        try:
            os.rmdir(parent)
        except OSError:
            break
        parent = os.path.dirname(parent)
    return dest


def cmd_candidates(root: str, args: List[str]) -> int:
    c = candidates(root, True)
    if "--json" in args:
        print(json.dumps(c, indent=1))
        return 0
    labels = {"created_temporary": "Created by an agent, looks temporary (remove unless it is part of the change)",
              "created_other": "Created by an agent (keep if it is part of the change)",
              "scratch": "Scratch files (always removable)",
              "untracked_since_session": "Untracked since the session started (remove with --untracked if you made them)"}
    shown = False
    for key, label in labels.items():
        if c[key]:
            shown = True
            print(label + ":")
            for r in c[key]:
                print(f"  {r}")
    if not shown:
        print("Nothing to clean up.")
    return 0


def cmd_remove(root: str, args: List[str]) -> int:
    allow_untracked = "--untracked" in args
    paths = [a for a in args if not a.startswith("--")]
    if not paths:
        print("usage: cleanup.py remove <path>... [--untracked]", file=sys.stderr)
        return 2
    c = candidates(root, True)
    allowed = set(c["created_temporary"] + c["created_other"]) | (set(c["untracked_since_session"]) if allow_untracked else set())
    track = tracked(root)
    batch = time.strftime("%Y%m%d-%H%M%S")
    status = 0
    for p in paths:
        r = rel(root, p)
        why = refusal(root, r, allowed, track)
        full = os.path.join(root, r) if r else ""
        if why is None and os.path.isdir(full) and not os.path.islink(full) and not ignored_build_output(root, r):
            why = "a folder that git does not ignore (remove its files one by one)"
        elif why is None and not os.path.lexists(full):
            why = "does not exist"
        if why:
            print(f"refused {p}: {why}")
            status = 1
            continue
        to_trash(root, r, batch)
        print(f"trashed {r}")
    if status == 0:
        print(f"Restore with: python3 .claude/tools/cleanup.py restore <path> (trash batch {batch})")
    return status


def cmd_scratch(root: str, args: List[str]) -> int:
    hours = float(args[args.index("--older-than") + 1]) if "--older-than" in args else 0.0
    cutoff = time.time() - hours * 3600
    base = os.path.join(root, ".claude", "scratch")
    n = 0
    for dp, dirs, fn in os.walk(base, topdown=False):
        for f in fn:
            p = os.path.join(dp, f)
            if os.path.getmtime(p) <= cutoff:
                os.remove(p)
                n += 1
        if dp != base:
            try:
                os.rmdir(dp)
            except OSError:
                pass
    print(f"removed {n} scratch file(s)")
    return 0


def cmd_restore(root: str, args: List[str]) -> int:
    tdir = os.path.join(root, ".claude", "state", "trash")
    if not args or not os.path.isdir(tdir):
        print("nothing to restore" if args else "usage: cleanup.py restore <path|all>")
        return 0 if args else 2
    want = None if args[0] == "all" else rel(root, args[0])
    restored = 0
    for batch in sorted(os.listdir(tdir), reverse=True):
        bdir = os.path.join(tdir, batch)
        for dp, _, fn in os.walk(bdir):
            for f in fn:
                r = os.path.relpath(os.path.join(dp, f), bdir).replace(os.sep, "/")
                if want in (None, r) and not os.path.exists(os.path.join(root, r)):
                    os.makedirs(os.path.dirname(os.path.join(root, r)) or root, exist_ok=True)
                    shutil.move(os.path.join(dp, f), os.path.join(root, r))
                    print(f"restored {r}")
                    restored += 1
    if not restored:
        print("nothing matched in trash")
    return 0


def purge(root: str) -> int:
    """Empty old trash batches, stale scratch files and old handoff notes. Runs automatically at session start."""
    cfg = config(root)
    tdir = os.path.join(root, ".claude", "state", "trash")
    cutoff = time.time() - float(cfg["trash_days"]) * 86400
    n = 0
    if os.path.isdir(tdir):
        for batch in os.listdir(tdir):
            p = os.path.join(tdir, batch)
            if os.path.getmtime(p) < cutoff:
                shutil.rmtree(p, ignore_errors=True)
                n += 1
    for base, max_age in ((os.path.join(root, ".claude", "scratch"), float(cfg["scratch_hours"]) * 3600),
                          (os.path.join(root, ".claude", "knowledge", "handoffs"), float(cfg["handoff_days"]) * 86400)):
        old = time.time() - max_age
        for dp, _, fn in os.walk(base, topdown=False):
            for f in fn:
                fp = os.path.join(dp, f)
                if f != ".gitkeep" and os.path.getmtime(fp) < old:
                    os.remove(fp)
    return n


def main(argv: List[str]) -> int:
    root = project_root()
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__.strip())
        return 0 if argv else 2
    cmd, rest = argv[0], argv[1:]
    if cmd == "purge":
        print(f"purged {purge(root)} trash batch(es)")
        return 0
    fn = {"candidates": cmd_candidates, "remove": cmd_remove, "scratch": cmd_scratch, "restore": cmd_restore}.get(cmd)
    if not fn:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    return fn(root, rest)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
