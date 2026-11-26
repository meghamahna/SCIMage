#!/usr/bin/env python3
"""PostToolUse Write: when an agent writes a whole Markdown doc, check it against the doc format
(linked TOC, one emoji per heading) and tell the agent what to fix. Edits to existing docs are left alone."""
import fnmatch
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "lib"))
sys.path.insert(0, os.path.join(HERE, "..", "tools"))
from common import add_context, project_dir, rel_to, run_soft  # noqa: E402
import doc_format  # noqa: E402

EXEMPT = [".claude/*", ".github/*", "CHANGELOG*.md", "**/CHANGELOG*.md"]


def main(event):
    if event.get("tool_name") != "Write":
        return
    proj = project_dir(event)
    path = (event.get("tool_input") or {}).get("file_path") or ""
    rel = rel_to(proj, path)
    if not rel.endswith(".md") or rel.startswith("..") or any(fnmatch.fnmatch(rel, g) for g in EXEMPT):
        return
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return
    errors, _ = doc_format.check(rel, text)
    if not errors:
        return
    shown = "; ".join(e.split(": ", 1)[-1] for e in errors[:5]) + (f" (+{len(errors) - 5} more)" if len(errors) > 5 else "")
    add_context("PostToolUse", (
        f"[harness:docs] {rel} does not follow the doc format (doc-standards skill): {shown}. Fix it now: start every "
        "heading with one emoji (`python3 .claude/tools/doc_format.py palette`), then run "
        f"`python3 .claude/tools/doc_format.py toc {rel}` and `python3 .claude/tools/doc_format.py check {rel}`."))


if __name__ == "__main__":
    run_soft(main)
