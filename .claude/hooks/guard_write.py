#!/usr/bin/env python3
"""PreToolUse Write|Edit|MultiEdit|NotebookEdit: no secrets/PII into files; harness config is read-only."""
import fnmatch
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
try:
    from common import UNBLOCK_HINT, ask, deny, load_config, scan_local, project_dir, rel_to, run_guard  # noqa: E402
    from detectors import Allowlist, line_of, sensitive_path_reason, summarize  # noqa: E402
except Exception:  # noqa: BLE001 - a broken install must not let the call through
    import json
    _MSG = "[harness:security] guard could not load (harness files damaged); blocked to be safe. Re-run install.py."
    sys.stdout.write(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny",
                                           "permissionDecisionReason": _MSG}}))
    sys.exit(0)

PROTECTED = [
    ".claude/settings.json", ".claude/settings.local.json", ".claude/harness.json",
    ".claude/.harness-manifest.json", ".claude/hooks/*", ".claude/tools/*", ".claude/security/*",
    ".claude/state/*", ".claude/knowledge/INDEX.md", ".claude/knowledge/graph.json",
    ".git/*", "*/.git/*",  # hooks and config in here run on the user's next git command
]
# Files that run on their own, or that Claude is told to follow in every later session. A poisoned web page or
# package README that talks an agent into changing one keeps its hold after the session ends, so the user confirms.
AUTORUN = [
    ".mcp.json", ".claude/agents/*", ".claude/skills/*", ".claude/commands/*",
    "CLAUDE.md", "*/CLAUDE.md", "CLAUDE.local.md", "*/CLAUDE.local.md", "AGENTS.md", "*/AGENTS.md",
    ".github/workflows/*", ".husky/*", ".vscode/tasks.json",
]


def _match(rel, globs):
    rel = rel.lower()  # macOS and Windows ignore case
    for g in globs:
        if fnmatch.fnmatch(rel, g.lower()) or fnmatch.fnmatch(rel, g.lower().rstrip("*") + "**"):
            return g
    return None


def protected_reason(rel):
    return _match(rel, PROTECTED)


def new_text(tool, ti):
    if tool == "Write":
        return ti.get("content") or ""
    if tool == "Edit":
        return ti.get("new_string") or ""
    if tool == "MultiEdit":
        return "\n".join(e.get("new_string") or "" for e in ti.get("edits") or [])
    if tool == "NotebookEdit":
        return ti.get("new_source") or ""
    return ""


def main(event):
    proj = project_dir(event)
    tool = event.get("tool_name", "")
    ti = event.get("tool_input") or {}
    path = ti.get("file_path") or ti.get("notebook_path") or ""
    rel = rel_to(proj, path)
    allow = Allowlist.load(proj)
    g = protected_reason(rel)
    if g:
        hint = "INDEX.md/graph.json are generated: run `python3 .claude/tools/kg.py index`." if "knowledge" in g else \
            "Git internals are changed with git commands, by the user." if g.startswith(".git") else \
            "Harness config is human-maintained; ask the user to change it."
        return deny(f"Writing '{rel}' is not allowed (protected: {g}). {hint}")
    reason = sensitive_path_reason(path)
    if reason and not allow.path_allowed(rel):
        return deny(f"Writing '{rel}' blocked: {reason}. Secret files must be created by a human.")
    if not allow.path_allowed(rel):
        cfg = load_config(proj)["scan"]
        text = new_text(tool, ti)
        findings = scan_local(text, allow, cfg)
        if findings:
            lines = sorted({line_of(text, f.start) for f in findings})[:10]
            return deny(f"Write to '{rel}' blocked: new content contains {summarize(findings)} at line(s) {lines} "
                        "of the new text. Next step: load secrets from env vars / a secret manager by NAME, and use "
                        f"obviously fake fixtures (e.g. user@example.com, 555-0100) in tests. {UNBLOCK_HINT}")
    if _match(rel, AUTORUN):
        return ask(f"Changing '{rel}' changes what runs automatically or what Claude is told in every later session. "
                   "Approve only if you asked for this change.")
    return None


if __name__ == "__main__":
    run_guard(main)
