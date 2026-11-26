#!/usr/bin/env python3
"""Save the files agents write inside .claude/: knowledge-graph nodes, handoff notes and scratch files.

Claude Code asks for approval before any Write/Edit under .claude/, so agents save these files through
this tool instead. Content comes from stdin and gets the same secret/PII scan as the write guard.

Usage:
  python3 .claude/tools/save.py node <id> <<'EOF'        -> .claude/knowledge/nodes/<id>.md (stamps updated + verified_commit)
  python3 .claude/tools/save.py handoff <agent> <<'EOF'  -> .claude/knowledge/handoffs/<agent>-<timestamp>.md (prints the path)
  python3 .claude/tools/save.py scratch <name> <<'EOF'   -> .claude/scratch/<name> (prints the path)
"""
from __future__ import annotations

import argparse
import os
import re
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "hooks", "lib"))
sys.path.insert(0, HERE)
from common import load_config, scan_local  # noqa: E402
from detectors import Allowlist, summarize  # noqa: E402
import kg as kgmod  # noqa: E402

NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")


def fail(msg: str, code: int = 2) -> int:
    print(msg, file=sys.stderr)
    return code


def node_text(root: str, nid: str, text: str):
    """Return (file text, error). The node id must match the frontmatter; dates are stamped here."""
    if not kgmod.ID_RE.match(nid):
        return None, "id must be lowercase kebab-case: <kind>-<slug>"
    if not text.startswith("---"):
        return None, "a node starts with frontmatter (id, kind, title, summary, tags, links, sources); see the knowledge-graph skill"
    n = kgmod.parse_node_text(text)
    meta = n["meta"]
    if meta.setdefault("id", nid) != nid:
        return None, f"frontmatter id '{meta['id']}' does not match '{nid}'"
    missing = [f for f in ("kind", "summary") if not meta.get(f)]
    if missing:
        return None, f"frontmatter is missing {', '.join(missing)}"
    meta["updated"] = kgmod.today()
    meta["verified_commit"] = kgmod.head_commit(root)
    return kgmod.dump_node(meta, n["body"]), None


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="Save a KG node, handoff note or scratch file from stdin")
    ap.add_argument("kind", choices=["node", "handoff", "scratch"])
    ap.add_argument("name", help="node id, agent name, or scratch file name")
    ap.add_argument("--root", help="project root (default: auto)")
    a = ap.parse_args(argv)
    root = kgmod.project_root(a.root)
    if not NAME_RE.match(a.name):
        return fail(f"invalid name '{a.name}': use letters, digits, '.', '_' or '-' (no paths)")
    text = sys.stdin.read()
    if not text.strip():
        return fail("nothing on stdin; pass the content with a heredoc: <<'EOF' ... EOF")

    cfg = load_config(root)["scan"]
    # The same scan as the write guard: one contributor address in a handoff note passes, a bulk export does not.
    findings = scan_local(text, Allowlist.load(root), cfg)
    if findings:
        return fail(f"[harness:security] save blocked: content contains {summarize(findings)}. "
                    "Refer to secrets by env var name and use fake fixtures.", 1)

    if a.kind == "node":
        text, err = node_text(root, a.name, text)
        if err:
            return fail(err)
        path = os.path.join(root, ".claude", "knowledge", "nodes", f"{a.name}.md")
    elif a.kind == "handoff":
        path = os.path.join(root, ".claude", "knowledge", "handoffs", f"{a.name}-{time.strftime('%Y%m%d-%H%M%S')}.md")
    else:
        path = os.path.join(root, ".claude", "scratch", a.name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text if text.endswith("\n") else text + "\n")
    print(os.path.relpath(path, root))
    return 0


if __name__ == "__main__":
    sys.exit(main())
