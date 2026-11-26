#!/usr/bin/env python3
"""PreToolUse Read|Grep: never let raw secrets/PII reach the model.

Read  -> sensitive-path deny, then a full content pre-scan of the target file.
Grep  -> sensitive-path deny; in `content` mode, pre-scan the lines the search would return.
"""
import fnmatch
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
try:
    from common import UNBLOCK_HINT, deny, load_config, scan_local, project_dir, rel_to, run_guard  # noqa: E402
    from detectors import Allowlist, line_of, sensitive_path_reason, summarize  # noqa: E402
except Exception:  # noqa: BLE001 - a broken install must not let the call through
    import json
    _MSG = "[harness:security] guard could not load (harness files damaged); blocked to be safe. Re-run install.py."
    sys.stdout.write(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny",
                                           "permissionDecisionReason": _MSG}}))
    sys.exit(0)

SAFE_READ_HINT = "Use /safe-read (python3 .claude/tools/safe_read.py <path>) to view it with values masked."
RANGE_HINT = "Next step: Read only the lines you need with offset/limit (only those lines are scanned), or"


def _is_binary(path):
    try:
        with open(path, "rb") as fh:
            return b"\x00" in fh.read(8192)
    except OSError:
        return True


def _read_text(path, max_bytes):
    with open(path, "rb") as fh:
        data = fh.read(max_bytes + 1)
    return data[:max_bytes].decode("utf-8", "replace"), len(data) > max_bytes


def _read_lines(path, offset, limit, max_bytes):
    """The lines a ranged Read returns (offset is 1-based), streamed so big files stay cheap."""
    out, size = [], 0
    with open(path, encoding="utf-8", errors="replace") as fh:
        for i, line in enumerate(fh, 1):
            if i < offset:
                continue
            if (limit and i >= offset + limit) or size > max_bytes:
                break
            out.append(line)
            size += len(line)
    return "".join(out), size > max_bytes


def _scan_file(path, rel, allow, cfg, offset=None, limit=None):
    if offset or limit:
        text, truncated = _read_lines(path, int(offset or 1), int(limit or 0), cfg["max_file_bytes"])
    else:
        text, truncated = _read_text(path, cfg["max_file_bytes"])
    return scan_local(text, allow, cfg), text, truncated


def check_read(event, proj, cfg, allow):
    path = (event.get("tool_input") or {}).get("file_path") or ""
    rel = rel_to(proj, path)
    reason = sensitive_path_reason(path)
    if reason and not allow.path_allowed(rel):
        return deny(f"Read of '{rel}' blocked: {reason}. {SAFE_READ_HINT}")
    abspath = os.path.join(proj, os.path.expanduser(path)) if not os.path.isabs(path) else path
    if not os.path.isfile(abspath) or allow.path_allowed(rel) or _is_binary(abspath):
        return None
    ti = event.get("tool_input") or {}
    offset, limit = ti.get("offset"), ti.get("limit")
    findings, text, truncated = _scan_file(abspath, rel, allow, cfg, offset, limit)
    if findings:
        first = int(offset or 1)
        lines = sorted({first - 1 + line_of(text, f.start) for f in findings})[:10]
        return deny(f"Read of '{rel}' blocked: contains {summarize(findings)} at line(s) {lines}. {RANGE_HINT} {SAFE_READ_HINT[0].lower() + SAFE_READ_HINT[1:]} {UNBLOCK_HINT}")
    if truncated:
        return deny(f"Read of '{rel}' blocked: the part requested is over the scan limit ({cfg['max_file_bytes']} bytes) "
                    "so it cannot be verified clean. Read a smaller range with offset/limit.")
    return None


# File names each Grep `type` searches. Each list is at least as wide as ripgrep's own, so a file ripgrep would
# read is always pre-scanned. A type that is not listed here gets no filter and every file is pre-scanned.
TYPE_SUFFIXES = {
    "py": (".py", ".pyi"), "python": (".py", ".pyi"),
    "js": (".js", ".jsx", ".mjs", ".cjs", ".vue"), "ts": (".ts", ".tsx", ".mts", ".cts"),
    "go": (".go",), "rust": (".rs",), "java": (".java", ".jsp", ".jspx", ".properties"),
    "ruby": (".rb", ".gemspec", ".rbw", ".rake", "Gemfile", "Rakefile", ".irbrc"), "php": (".php", ".phtml", ".php3", ".php4", ".php5"),
    "c": (".c", ".h", ".cats"), "cpp": (".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h", ".inl", ".c++", ".h++"),
    "cs": (".cs",), "csharp": (".cs",), "swift": (".swift",), "kotlin": (".kt", ".kts"),
    "md": (".md", ".markdown", ".mdown", ".mkdn", ".mkd", ".mdwn", ".mdx"),
    "markdown": (".md", ".markdown", ".mdown", ".mkdn", ".mkd", ".mdwn", ".mdx"),
    "html": (".html", ".htm", ".ejs"), "css": (".css", ".scss"), "sql": (".sql", ".psql"),
}


def _grep_targets(root, glob, cfg, type_=None):
    skip = set(cfg["skip_dirs"])
    if os.path.isfile(root):
        yield root
        return
    suffixes = TYPE_SUFFIXES.get(str(type_ or "").lower())
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in skip]
        for fn in filenames:
            if glob and not (fnmatch.fnmatch(fn, glob) or fnmatch.fnmatch(os.path.join(dirpath, fn), glob)):
                continue
            if suffixes and not fn.endswith(suffixes):
                continue  # ripgrep does not read this file for that type, so there is nothing to pre-scan
            yield os.path.join(dirpath, fn)


def check_grep(event, proj, cfg, allow):
    ti = event.get("tool_input") or {}
    path = ti.get("path") or "."
    glob = ti.get("glob")
    for candidate in (path, glob):
        if candidate and sensitive_path_reason(candidate) and not allow.path_allowed(rel_to(proj, candidate)):
            return deny(f"Grep over '{candidate}' blocked: {sensitive_path_reason(candidate)}. {SAFE_READ_HINT}")
    if (ti.get("output_mode") or "files_with_matches") != "content":
        return None  # only file paths / counts leave the tool
    try:
        flags = re.IGNORECASE if ti.get("-i") else 0
        if ti.get("multiline"):
            flags |= re.DOTALL | re.MULTILINE
        rx = re.compile(ti.get("pattern") or "", flags)
    except re.error:
        rx = None  # unknown syntax: scan whole files instead of matching lines
    ctx = max(int(ti.get("-C") or ti.get("context") or 0), int(ti.get("-A") or 0), int(ti.get("-B") or 0))
    root = path if os.path.isabs(path) else os.path.join(proj, path)
    hits, nfiles, nbytes = [], 0, 0
    for fp in _grep_targets(root, glob, cfg, ti.get("type")):
        rel = rel_to(proj, fp)
        if allow.path_allowed(rel):
            continue
        nfiles += 1
        if nfiles > cfg["grep_max_files"] or nbytes > cfg["grep_max_bytes"]:
            # Too big to pre-scan quickly (large repos). Sensitive paths were checked above; the matched
            # lines that come back are masked by redact_output.py, so let the search run.
            return None
        try:
            if os.path.getsize(fp) > cfg["max_file_bytes"] or _is_binary(fp):
                continue
            text, _ = _read_text(fp, cfg["max_file_bytes"])
        except OSError:
            continue
        nbytes += len(text)
        if sensitive_path_reason(fp) and (rx is None or rx.search(text)):
            return deny(f"Grep would return content from sensitive file '{rel}'. Add a narrower glob. {SAFE_READ_HINT}")
        if rx is None:
            sample = text
        else:
            lines = text.splitlines()
            idx = [i for i, ln in enumerate(lines) if rx.search(ln)]
            if not idx:
                continue
            keep = sorted({j for i in idx for j in range(max(0, i - ctx), min(len(lines), i + ctx + 1))})
            sample = "\n".join(lines[j] for j in keep)
        f = scan_local(sample, allow, cfg)
        if f:
            hits.append(f"{rel} ({summarize(f)})")
        if len(hits) >= 5:
            break
    if hits:
        return deny("Grep results would expose sensitive values in: " + "; ".join(hits) +
                    ". Exclude those files via `glob`, use output_mode=files_with_matches, or " + SAFE_READ_HINT + " " + UNBLOCK_HINT)
    return None


def main(event):
    proj = project_dir(event)
    cfg = load_config(proj)["scan"]
    allow = Allowlist.load(proj)
    tool = event.get("tool_name", "")
    if tool == "Grep":
        check_grep(event, proj, cfg, allow)
    else:
        check_read(event, proj, cfg, allow)


if __name__ == "__main__":
    run_guard(main)
