#!/usr/bin/env python3
"""PreToolUse WebFetch|WebSearch|mcp__*: nothing secret or personal leaves via URLs, queries, or MCP args.

Secrets are always blocked. PII is blocked except in recipient-style fields of MCP
tools (e.g. `to`, `attendees`), where an address is the point of the call.
"""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
try:
    from common import deny, iter_strings, load_config, project_dir, run_guard  # noqa: E402
    from detectors import Allowlist, scan_text, summarize  # noqa: E402
except Exception:  # noqa: BLE001 - a broken install must not let the call through
    import json
    _MSG = "[harness:security] guard could not load (harness files damaged); blocked to be safe. Re-run install.py."
    sys.stdout.write(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny",
                                           "permissionDecisionReason": _MSG}}))
    sys.exit(0)


def main(event):
    proj = project_dir(event)
    cfg = load_config(proj)
    scan, recipients = cfg["scan"], {k.lower() for k in cfg["egress"]["recipient_keys"]}
    allow = Allowlist.load(proj)
    tool = event.get("tool_name", "")
    is_mcp = tool.startswith("mcp__")
    findings = []
    for key, value in iter_strings(event.get("tool_input") or {}):
        pii = scan["block_pii"] and not (is_mcp and key.lower() in recipients)
        findings += scan_text(value, allow, include_pii=pii, pii_detectors=scan["pii_detectors"])
    if findings:
        deny(f"{tool} call blocked: arguments contain {summarize(findings)}. "
             "Never send secrets or personal data to external services; reference them by name.")


if __name__ == "__main__":
    run_guard(main)
