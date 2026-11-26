#!/usr/bin/env python3
"""PostToolUse(*) + UserPromptSubmit: enforce the per-agent context budget.

Reads the agent's own transcript (subagents: <session>/subagents/agent-<id>.jsonl),
takes the latest assistant `usage` (input + cache_read + cache_creation + output) and
compares it with the context window. >= warn_pct: warn once. >= limit_pct: demand a
handoff on every call until the agent stops.
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
from common import add_context, load_config, project_dir, run_soft, state_dir  # noqa: E402

TAIL_BYTES = 4 * 1024 * 1024
# The main session is reminded every 25th call; auto-compact at the same percentage is the backstop.
LIMIT_REPEAT_EVERY = 25


def transcript_for(event):
    tp = event.get("transcript_path") or ""
    aid = event.get("agent_id")
    if aid and tp:
        base = tp[:-6] if tp.endswith(".jsonl") else tp
        cand = os.path.join(base, "subagents", f"agent-{aid}.jsonl")
        if os.path.isfile(cand):
            return cand, True
        if "subagents" in tp:
            return tp, True
    return tp, False


def last_usage(path, is_sub):
    try:
        size = os.path.getsize(path)
        with open(path, "rb") as fh:
            fh.seek(max(0, size - TAIL_BYTES))
            lines = fh.read().decode("utf-8", "replace").splitlines()
    except OSError:
        return None
    for line in reversed(lines):
        if '"usage"' not in line:
            continue
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        if rec.get("type") != "assistant" or (rec.get("isSidechain") and not is_sub):
            continue
        msg = rec.get("message") or {}
        u = msg.get("usage")
        if not u:
            continue
        used = sum(int(u.get(k) or 0) for k in
                   ("input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "output_tokens"))
        return used, str(msg.get("model") or ""), str(rec.get("requestedModel") or "")
    return None


def window_for(cfg_window, model, requested, used):
    if isinstance(cfg_window, int) and cfg_window > 0:
        return cfg_window
    if "[1m]" in model or "1m" in requested.lower() or used > 200_000:
        return 1_000_000
    return 200_000


def session_window(proj, event):
    """Main-session window size as reported to the status line (exact, incl. 1M sessions)."""
    sid = event.get("session_id")
    if not sid or event.get("agent_id"):
        return None
    try:
        with open(os.path.join(state_dir(proj), f"window-{sid}.json")) as fh:
            return int(json.load(fh)["window"])
    except (OSError, ValueError, KeyError):
        return None


def measure(event, cfg, proj):
    path, is_sub = transcript_for(event)
    if not path:
        return None
    lu = last_usage(path, is_sub)
    if not lu:
        return None
    used, model, requested = lu
    win = window_for(cfg["window_tokens"], model, requested, used)
    if cfg["window_tokens"] == "auto":
        win = max(win, session_window(proj, event) or 0)
    return round(100 * used / win), used, win


def main(event):
    proj = project_dir(event)
    cfg = load_config(proj)["context"]
    m = measure(event, cfg, proj)
    if not m:
        return
    pct, used, win = m
    who = event.get("agent_type") or ("subagent" if event.get("agent_id") else "main")
    key = event.get("agent_id") or "main"
    sfile = os.path.join(state_dir(proj), f"context-{key}.json")
    try:
        with open(sfile) as fh:
            st = json.load(fh)
    except (OSError, ValueError):
        st = {}
    st.update({"pct": pct, "used": used, "window": win, "agent": who, "ts": int(time.time())})
    ev_name = event.get("hook_event_name") or "PostToolUse"
    save = f"`python3 .claude/tools/save.py handoff {who} <<'EOF'` ... `EOF` (it prints the path)"
    msg = None
    if pct < cfg["warn_pct"]:  # a new session, or after /clear or compaction: warn again next time
        st.pop("warned_band", None)
        st.pop("limit_seen", None)
    if pct >= cfg["limit_pct"]:
        seen = st["limit_seen"] = int(st.get("limit_seen") or 0) + 1
        if event.get("agent_id"):  # a subagent must stop, so it is told on every call
            msg = (f"[harness:context] LIMIT {pct}% ({used:,}/{win:,} tokens) for {who}. STOP WORK NOW. "
                   f"Save a handoff note with {save}, using the schema in AGENTS.md (Goal, Done, Remaining, "
                   "Key pointers, Decisions made, Open questions, Next first step). If you cannot run Bash, "
                   "put that content in SUMMARY and use `pending-parent-write` as the path. Then end your turn with "
                   f"`STATUS: HANDOFF <path>`. Do not call any other tools.")
        elif seen % LIMIT_REPEAT_EVERY == 1:  # the main session is reminded, not nagged on every call
            msg = (f"[harness:context] LIMIT {pct}% ({used:,}/{win:,} tokens) in the main session. Stop starting new work. "
                   f"Save a handoff note with {save} (schema in AGENTS.md), then delegate the remaining work to a "
                   "fresh `orchestrator` subagent with prompt `Resume from handoff <path>`. If delegation is not "
                   "possible, tell the user to run /clear then `/resume-handoff <path>`.")
    elif pct >= cfg["warn_pct"] and st.get("warned_band") != "warn":
        st["warned_band"] = "warn"
        msg = (f"[harness:context] WARN {pct}% ({used:,}/{win:,} tokens) for {who}. Finish the current unit of work; "
               f"no large reads; delegate exploration (repo-explorer). At {cfg['limit_pct']}% you must hand off.")
    try:
        with open(sfile, "w") as fh:
            json.dump(st, fh)
    except OSError:
        pass
    if msg:
        add_context(ev_name, msg)


if __name__ == "__main__":
    run_soft(main)
