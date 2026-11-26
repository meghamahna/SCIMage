#!/usr/bin/env python3
"""UserPromptSubmit: block prompts that contain secrets or PII."""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
try:
    from common import PREFIX, UNBLOCK_HINT, emit, load_config, scan_local, project_dir, run_guard  # noqa: E402
    from detectors import Allowlist, summarize  # noqa: E402
except Exception:  # noqa: BLE001 - a broken install must not let the call through
    import json
    _MSG = "[harness:security] guard could not load (harness files damaged); blocked to be safe. Re-run install.py."
    sys.stdout.write(json.dumps({"decision": "block", "reason": _MSG}))
    sys.exit(0)


def main(event):
    proj = project_dir(event)
    cfg = load_config(proj)["scan"]
    findings = scan_local(event.get("prompt", ""), Allowlist.load(proj), cfg)
    if findings:
        emit({
            "decision": "block",
            "reason": (f"{PREFIX} Prompt not sent: it contains {summarize(findings)}. "
                       "Remove or mask the value (refer to secrets by env var NAME), then resend. "
                       f"{UNBLOCK_HINT}"),
            "suppressOriginalPrompt": True,
        })


if __name__ == "__main__":
    run_guard(main, "UserPromptSubmit")
