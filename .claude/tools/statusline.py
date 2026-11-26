#!/usr/bin/env python3
"""Status line: model · ctx NN% (budget-colored) · KG nodes · pending KG updates."""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def main():
    try:
        ev = json.loads(sys.stdin.read() or "{}")
    except ValueError:
        ev = {}
    proj = (ev.get("workspace") or {}).get("project_dir") or os.environ.get("CLAUDE_PROJECT_DIR") or \
        os.path.abspath(os.path.join(HERE, "..", ".."))
    try:
        with open(os.path.join(proj, ".claude", "harness.json")) as fh:
            ctx_cfg = json.load(fh).get("context", {})
    except (OSError, ValueError):
        ctx_cfg = {}
    limit, warn = ctx_cfg.get("limit_pct", 75), ctx_cfg.get("warn_pct", 70)
    model = (ev.get("model") or {}).get("display_name") or "?"
    size = (ev.get("context_window") or {}).get("context_window_size")
    if size and ev.get("session_id"):  # lets context_guard.py use the real window size
        try:
            sd = os.path.join(proj, ".claude", "state")
            os.makedirs(sd, exist_ok=True)
            with open(os.path.join(sd, f"window-{ev['session_id']}.json"), "w") as fh:
                json.dump({"window": int(size)}, fh)
        except (OSError, ValueError):
            pass
    pct = (ev.get("context_window") or {}).get("used_percentage")
    parts = [model]
    if pct is not None:
        pct = round(float(pct))
        color = "\033[31m" if pct >= limit else "\033[33m" if pct >= warn else "\033[32m"
        parts.append(f"{color}ctx {pct}%/{limit}%\033[0m")
    try:
        sys.path.insert(0, HERE)
        import kg as kgmod
        st = kgmod.stats(kgmod.KG(proj))
        parts.append(f"KG {st['nodes']}" + (f" · {st['pending']} pending" if st["pending"] else ""))
    except Exception:  # noqa: BLE001
        pass
    print(" · ".join(parts))


if __name__ == "__main__":
    main()
