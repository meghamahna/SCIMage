#!/usr/bin/env python3
"""Print a file with secret/PII values masked as ***REDACTED:<detector>***.

Usage: python3 .claude/tools/safe_read.py <path> [--lines A-B]
Shows structure (keys, line layout) without exposing values. Binary files are refused.
"""
import argparse
import json
import os
import re
import stat
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "hooks", "lib"))
from detectors import Allowlist, scan_text, sensitive_path_reason  # noqa: E402

MAX_BYTES = 2 * 1024 * 1024
MASK = "***REDACTED:value***"
# In a credential file every value is masked, whatever the format, so nothing depends on a detector knowing it.
KV_RE = re.compile(r"^(\s*(?:export\s+)?[^\s=]+\s*=\s*)(.*)$")       # KEY=value, //registry/:_authToken=value
YAML_RE = re.compile(r"^(\s*(?:-\s+)?[^\s:]+:\s+)(\S.*)$")            # key: value
JSON_STRING_RE = re.compile(r'("(?:[^"\\]|\\.)*")(\s*:)?')            # every JSON string; group 2 marks a key
YAML_KEY_ONLY_RE = re.compile(r"^(?:-\s+)?[\w.\-]{1,64}:$")
ONLY_MARKERS_RE = re.compile(r"^(?:\*\*\*REDACTED:[\w.]+\*\*\*\s*)+$")
PUNCT_ONLY_RE = re.compile(r"^[\W_]+$")


def looks_like_json(path: str, text: str) -> bool:
    if path.lower().endswith((".json", ".tfstate")) or ".tfstate." in path.lower():
        return True
    if text.lstrip()[:1] not in ("{", "["):
        return False
    try:
        json.loads(text)
        return True
    except ValueError:
        return False


def mask_credential_line(line: str, as_json: bool) -> str:
    """One line of a credential file with its values masked and its keys and layout kept."""
    if as_json:
        return JSON_STRING_RE.sub(lambda m: m.group(0) if m.group(2) or m.group(1) == '""' else f'"{MASK}"', line)
    s = line.strip()
    if not s or s[0] in "#;" or (s[0] == "[" and s[-1] == "]") or YAML_KEY_ONLY_RE.match(s):
        return line  # blank, comment, section header, `key:` that opens a block
    for rx in (KV_RE, YAML_RE):
        m = rx.match(line)
        if m and not m.group(2).startswith("="):  # `abc==` is a padded token, not KEY=value
            value = m.group(2).strip()
            if value in ("", '""', "''") or ONLY_MARKERS_RE.match(value):
                return line
            return m.group(1) + MASK
    tokens = s.split()
    indent = line[:len(line) - len(line.lstrip())]
    if len(tokens) == 1:
        return line if PUNCT_ONLY_RE.match(s) or ONLY_MARKERS_RE.match(s) else indent + MASK
    return f"{indent}{tokens[0]} {MASK}"  # `machine host login x password y` keeps only its first word


def main(argv=None):
    ap = argparse.ArgumentParser(description="Print a file with sensitive values masked")
    ap.add_argument("path", nargs="?")
    ap.add_argument("--hash", action="store_true",
                    help="type one value at a hidden prompt; prints its allowlist line (terminal only)")
    ap.add_argument("--lines", help="A-B (1-based, inclusive)")
    a = ap.parse_args(argv)
    if a.hash:
        if not sys.stdin.isatty():
            print("--hash only works when a person runs it in a terminal", file=sys.stderr)
            return 2
        import getpass
        import hashlib
        value = getpass.getpass("Value to allow (input hidden): ")
        print("Add this line to .claude/security/allowlist:")
        print("sha256:" + hashlib.sha256(value.encode("utf-8", "replace")).hexdigest())
        return 0
    if not a.path:
        ap.error("path is required")
    proj = os.environ.get("CLAUDE_PROJECT_DIR") or os.path.abspath(os.path.join(HERE, "..", ".."))
    path = os.path.expanduser(a.path)
    try:
        st = os.stat(path)
    except FileNotFoundError:
        print(f"no such file: {a.path}", file=sys.stderr)
        return 2
    except PermissionError:
        # The Bash sandbox denies credential files (the Read deny rules), so this is not proof the file is missing.
        print(f"cannot read {a.path}: access denied (the Bash sandbox blocks credential files). The file may exist; "
              "do not tell the user it is missing. Ask them to run `python3 .claude/tools/safe_read.py "
              f"{a.path}` in their own terminal for a masked view.", file=sys.stderr)
        return 2
    if not stat.S_ISREG(st.st_mode):
        print(f"not a regular file: {a.path}", file=sys.stderr)
        return 2
    with open(path, "rb") as fh:
        raw = fh.read(MAX_BYTES + 1)
    if b"\x00" in raw[:8192]:
        print("binary file; refusing to print", file=sys.stderr)
        return 2
    text = raw[:MAX_BYTES].decode("utf-8", "replace")
    allow = Allowlist.load(proj)
    sensitive = sensitive_path_reason(path) is not None
    # Scan the whole text (not line by line) so multi-line values such as private key blocks are masked in
    # full. Value/path allowlists are ignored: this tool always masks. Line numbers are kept stable.
    parts, pos = [], 0
    for f in scan_text(text, Allowlist(disabled=allow.disabled)):
        parts += [text[pos:f.start], f"***REDACTED:{f.detector}***", "\n" * f.value.count("\n")]
        pos = f.end
    as_json = sensitive and looks_like_json(path, text)
    text = "".join(parts) + text[pos:]
    # In credential files every value is masked, detector match or not.
    out_lines = [mask_credential_line(line, as_json) if sensitive else line for line in text.splitlines()]
    lo, hi = 1, len(out_lines)
    if a.lines:
        x, _, y = a.lines.partition("-")
        lo, hi = max(1, int(x)), min(len(out_lines), int(y or x))
    width = len(str(hi))
    for i in range(lo, hi + 1):
        print(f"{str(i).rjust(width)}\t{out_lines[i - 1]}")
    if len(raw) > MAX_BYTES:
        print(f"... truncated at {MAX_BYTES} bytes")
    return 0


if __name__ == "__main__":
    sys.exit(main())
