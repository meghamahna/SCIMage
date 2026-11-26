"""Hook I/O helpers: read the event, load config, emit decisions.

Guards are fail-closed: `run_guard` turns any unexpected exception into a deny.
"""
from __future__ import annotations

import json
import os
import sys
import traceback
from typing import Any, Callable, Dict, Optional

DEFAULT_CONFIG: Dict[str, Any] = {
    "context": {"limit_pct": 75, "warn_pct": 70, "window_tokens": "auto"},
    "scan": {
        "max_file_bytes": 5 * 1024 * 1024,
        "grep_max_files": 2000,
        "grep_max_bytes": 50 * 1024 * 1024,
        "block_pii": True,
        "pii_detectors": ["email", "phone", "ssn", "sin", "credit_card", "iban"],
        # "warn" = allowed in local reads, writes, commands and prompts; still blocked when leaving the machine.
        "pii_action": {"email": "warn", "phone": "warn"},
        "pii_bulk_threshold": 20,
        "skip_dirs": [".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build",
                      ".next", "target", "vendor", ".tox", ".mypy_cache", ".pytest_cache", "coverage"],
    },
    "egress": {"recipient_keys": ["to", "cc", "bcc", "recipient", "recipients", "email", "emails",
                                  "user", "users", "user_id", "attendees", "assignee", "owner", "emailAddress"]},
    # "block" = package installs must pass --ignore-scripts; "ask" = confirm without it; "allow" = no check.
    "supply_chain": {"install_scripts": "block"},
    "diagrams": {"theme": "light"},
    "knowledge": {"max_index_lines": 200, "max_node_lines": 60, "pending_trigger_count": 5,
                  "ignore": ["tests/**", "test/**", "**/__tests__/**", "**/*.test.*", "**/*.spec.*",
                             "**/*_test.*", "**/test_*.py", "*.lock", "**/package-lock.json",
                             "**/pnpm-lock.yaml", "**/yarn.lock", ".claude/**", "**/*.snap"]},
}

PREFIX = "[harness:security]"


def read_event() -> Dict[str, Any]:
    data = sys.stdin.read()
    return json.loads(data) if data.strip() else {}


def project_dir(event: Optional[Dict[str, Any]] = None) -> str:
    d = os.environ.get("CLAUDE_PROJECT_DIR")
    if d:
        return d
    if event and event.get("cwd"):
        return event["cwd"]
    # hooks live in <project>/.claude/hooks/
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))


def _deep_merge(base: Dict[str, Any], over: Dict[str, Any]) -> Dict[str, Any]:
    out = dict(base)
    for k, v in over.items():
        out[k] = _deep_merge(out[k], v) if isinstance(v, dict) and isinstance(out.get(k), dict) else v
    return out


def load_config(proj: str) -> Dict[str, Any]:
    try:
        with open(os.path.join(proj, ".claude", "harness.json"), encoding="utf-8") as fh:
            return _deep_merge(DEFAULT_CONFIG, json.load(fh))
    except (OSError, ValueError):
        return DEFAULT_CONFIG


def scan_local(text: str, allow, scan: Dict[str, Any]) -> list:
    """Findings that block on local surfaces (Read, Grep, Write, Bash text, prompts, tool output).

    PII detectors set to "warn" in scan.pii_action (emails and phone numbers by default) only count here in
    bulk: `pii_bulk_threshold` or more distinct values in one text, which is what a customer export looks
    like. A contributor address in package.json or CODEOWNERS passes. guard_egress.py blocks all of them.
    """
    from detectors import scan_text  # same lib/ directory; imported here to keep this module standalone
    findings = scan_text(text, allow, include_pii=scan["block_pii"], pii_detectors=scan["pii_detectors"])
    warn = {f"pii.{d}" for d, action in (scan.get("pii_action") or {}).items() if action == "warn"}
    if warn and len({f.value for f in findings if f.detector in warn}) < int(scan.get("pii_bulk_threshold", 20)):
        findings = [f for f in findings if f.detector not in warn]
    return findings


UNBLOCK_HINT = ("False positive? A person can allow the file with a `path:<glob>` line in .claude/security/allowlist, "
                "or one value with `sha256:<hash>` (get the hash by running "
                "`python3 .claude/tools/safe_read.py --hash` in a terminal).")


def rel_to(proj: str, path: str) -> str:
    try:
        ap = os.path.abspath(os.path.join(proj, os.path.expanduser(path)))
        rel = os.path.relpath(ap, proj)
        return rel.replace(os.sep, "/")
    except ValueError:
        return path


def state_dir(proj: str) -> str:
    d = os.path.join(proj, ".claude", "state")
    os.makedirs(d, exist_ok=True)
    return d


def emit(obj: Dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(obj))
    sys.stdout.flush()


def pretool_decision(decision: str, reason: str) -> None:
    emit({"hookSpecificOutput": {"hookEventName": "PreToolUse",
                                 "permissionDecision": decision,
                                 "permissionDecisionReason": reason}})


def deny(reason: str) -> None:
    pretool_decision("deny", f"{PREFIX} {reason}")


def ask(reason: str) -> None:
    pretool_decision("ask", f"{PREFIX} {reason}")


def add_context(event_name: str, text: str) -> None:
    emit({"hookSpecificOutput": {"hookEventName": event_name, "additionalContext": text}})


def run_guard(fn: Callable[[Dict[str, Any]], None], event_name: str = "PreToolUse") -> None:
    """Run a guard; any crash denies (fail-closed) with a non-sensitive reason."""
    try:
        fn(read_event())
    except SystemExit:
        raise
    except Exception as exc:  # noqa: BLE001 - fail closed on anything
        if os.environ.get("HARNESS_DEBUG"):
            traceback.print_exc(file=sys.stderr)
        msg = f"guard error ({type(exc).__name__}); blocked to be safe. Run /harness-doctor."
        if event_name == "PreToolUse":
            deny(msg)
        elif event_name == "UserPromptSubmit":
            emit({"decision": "block", "reason": f"{PREFIX} {msg}"})
    sys.exit(0)


def run_soft(fn: Callable[[Dict[str, Any]], None]) -> None:
    """Advisory hooks (context, KG, session) never block on their own errors."""
    try:
        fn(read_event())
    except Exception:  # noqa: BLE001
        if os.environ.get("HARNESS_DEBUG"):
            traceback.print_exc(file=sys.stderr)
    sys.exit(0)


def iter_strings(obj: Any, key: str = ""):
    """Yield (key, string) for every string leaf in a JSON-like object."""
    if isinstance(obj, str):
        yield key, obj
    elif isinstance(obj, dict):
        for k, v in obj.items():
            yield from iter_strings(v, str(k))
    elif isinstance(obj, (list, tuple)):
        for v in obj:
            yield from iter_strings(v, key)
