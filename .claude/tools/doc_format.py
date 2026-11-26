#!/usr/bin/env python3
"""Documentation format: linked table of contents and one emoji per heading.

Rules enforced by `check`:
  - every heading (h1-h4; the h1 may be a one-line HTML <h1>, e.g. centered) starts with exactly one emoji from the palette, then a space
  - no emoji anywhere else (body text, lists, tables, TOC entries); code is ignored
  - no variation selectors or joiners, so GitHub anchors stay clean (`## 🚀 Quick start` -> `#-quick-start`)
  - one h1, no skipped heading levels
  - a TOC between <!-- toc --> and <!-- /toc --> that matches the headings and whose links resolve

Usage:
  python3 .claude/tools/doc_format.py toc <file.md>...     insert or refresh the TOC in place
  python3 .claude/tools/doc_format.py check <file.md>...   validate; exit 1 on any ERROR
  python3 .claude/tools/doc_format.py palette              list allowed heading emoji
"""
from __future__ import annotations

import re
import sys
import unicodedata
from typing import Dict, List, Tuple

TOC_START, TOC_END = "<!-- toc -->", "<!-- /toc -->"
TOC_TITLE = "## 📑 Contents"
TOC_LEVELS = (2, 3)

# Single-codepoint emoji that render as emoji by default (no U+FE0F needed). Meaning is a hint, not a rule.
PALETTE: Dict[str, str] = {
    "📑": "contents", "🚀": "quick start / getting started", "📦": "install / packages / what ships",
    "⚡": "usage / commands", "🧭": "overview / orientation / routing", "🧠": "knowledge / memory",
    "🤖": "agents / automation", "🧩": "skills / components / modules", "🔒": "security",
    "🔐": "secrets / credentials", "🔑": "auth / keys", "🛂": "permissions / access", "🚫": "limits / denied",
    "✅": "verify / checks / done", "❌": "failures / not supported", "🧪": "testing", "🔧": "configuration / options",
    "🔨": "build", "🧰": "tools", "🪝": "hooks", "📝": "writing / notes", "📚": "docs / reference",
    "📖": "guide / how-to", "📁": "files / layout", "📂": "repo layout", "🌳": "structure / tree",
    "🔀": "git / flow", "🌿": "branches", "📊": "metrics / diagrams", "📈": "performance / growth",
    "🎨": "design / style", "♿": "accessibility", "🌐": "network / web", "🔌": "integrations / plugins",
    "📡": "APIs / endpoints", "🧱": "architecture", "🔍": "search / lookup", "🔎": "review / inspection",
    "🧹": "cleanup / maintenance", "🐛": "troubleshooting / bugs", "🚨": "incidents / alerts",
    "🆘": "help / support", "🔁": "handoff / retry / loops", "🔄": "sync / updates / upgrade",
    "⏳": "budget / time", "🎯": "goals / scope", "💡": "tips / decisions / rationale", "📌": "key pointers",
    "🔗": "links / related", "📋": "checklists / templates", "👥": "roles / team", "🙋": "contributing",
    "📜": "history / changelog", "🔖": "versions / releases", "🧾": "contracts / formats",
    "🤝": "agreements / protocols", "🧮": "algorithms / data structures", "🧬": "data model / schema",
    "💾": "storage / persistence", "🔔": "notifications", "🚦": "status / policy", "👤": "users / actors",
    "⭐": "highlights", "🪄": "project title / setup",
}

HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
# A one-line, plain-text HTML title, e.g. a centered <h1 align="center">🪄 Name</h1>. Only h1: GitHub gives raw
# HTML headings no anchor, so a lower one could not be linked from the TOC. Nested tags (a logo) are not a title.
HTML_H1_RE = re.compile(r"^ {0,3}<h1(?:\s[^>]*)?>\s*([^<>]*?)\s*</h1>\s*$", re.IGNORECASE)
FENCE_RE = re.compile(r"^\s*(```|~~~)")
INLINE_CODE_RE = re.compile(r"`[^`]*`")
LINK_RE = re.compile(r"\[([^\]]*)\]\(#([^)]+)\)")


def is_emoji(ch: str) -> bool:
    cp = ord(ch)
    return (0x1F000 <= cp <= 0x1FAFF or 0x2600 <= cp <= 0x27BF or 0x2B00 <= cp <= 0x2BFF
            or 0x2300 <= cp <= 0x23FF or cp in (0x3030, 0x303D, 0x3297, 0x3299, 0xFE0F, 0x200D, 0x20E3))


def slugify(text: str, seen: Dict[str, int]) -> str:
    """GitHub-compatible anchor (github-slugger): strip markup, lowercase, drop punctuation and emoji."""
    t = re.sub(r"`([^`]*)`", r"\1", text)
    t = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", t)
    t = re.sub(r"<[^>]+>", "", t).lower()
    kept = []
    for ch in t:
        cat = unicodedata.category(ch)
        if ch in " -_" or cat[0] in ("L", "N") or cat in ("Mn", "Mc", "Me") and not is_emoji(ch):
            kept.append(ch)
    slug = "".join(kept).replace(" ", "-")
    if slug in seen:
        seen[slug] += 1
        return f"{slug}-{seen[slug]}"
    seen[slug] = 0
    return slug


def scan(lines: List[str]) -> Tuple[List[Tuple[int, int, str]], List[Tuple[int, str]]]:
    """Return headings (line_no, level, text) and body lines (line_no, text) outside code fences."""
    headings, body, in_fence = [], [], False
    for i, line in enumerate(lines, 1):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        m = HEADING_RE.match(line)
        h = HTML_H1_RE.match(line)
        if m:
            headings.append((i, len(m.group(1)), m.group(2)))
        elif h:
            headings.append((i, 1, h.group(1)))
        else:
            body.append((i, line))
    return headings, body


def heading_title(text: str) -> str:
    """Heading text without its leading emoji (used for TOC entries)."""
    parts = text.split(" ", 1)
    return parts[1].strip() if len(parts) == 2 and parts[0] and all(is_emoji(c) for c in parts[0]) else text


def build_toc(lines: List[str]) -> List[str]:
    headings, _ = scan(strip_toc(lines))
    seen: Dict[str, int] = {}
    toc_anchor = slugify(TOC_TITLE[3:], seen)  # the TOC heading itself comes first in the rendered doc
    out = [TOC_START, TOC_TITLE, ""]
    for _, level, text in headings:
        anchor = slugify(text, seen)
        if anchor == toc_anchor or level not in TOC_LEVELS:
            continue
        indent = "  " * (level - TOC_LEVELS[0])
        out.append(f"{indent}- [{heading_title(text)}](#{anchor})")
    out += ["", TOC_END]
    return out


def toc_span(lines: List[str]):
    """Line indexes of the TOC markers, ignoring any that appear inside code fences."""
    start = end = None
    in_fence = False
    for i, line in enumerate(lines):
        if FENCE_RE.match(line):
            in_fence = not in_fence
        elif not in_fence and line.strip() == TOC_START and start is None:
            start = i
        elif not in_fence and line.strip() == TOC_END and start is not None:
            end = i
            break
    return (start, end) if start is not None and end is not None else None


def strip_toc(lines: List[str]) -> List[str]:
    span = toc_span(lines)
    if span:
        a, b = span
        tail = lines[b + 1:]
        if tail and tail[0] == "":
            tail = tail[1:]
        return lines[:a] + tail
    return lines


def insert_toc(text: str) -> str:
    lines = strip_toc(text.split("\n"))
    headings, _ = scan(lines)
    h2 = next((ln for ln, lvl, _ in headings if lvl == 2), None)
    toc = build_toc(lines)
    if h2 is None:
        return "\n".join(lines)
    idx = h2 - 1
    block = toc + [""]
    if idx > 0 and lines[idx - 1] != "":
        block = [""] + block
    return "\n".join(lines[:idx] + block + lines[idx:])


def check(path: str, text: str) -> Tuple[List[str], List[str]]:
    errors, warns = [], []
    lines = text.split("\n")
    headings, body = scan(lines)
    if not headings:
        return [f"{path}: no headings"], warns
    h1 = [h for h in headings if h[1] == 1]
    if len(h1) != 1:
        errors.append(f"{path}: expected exactly one h1, found {len(h1)}")
    prev = 0
    for ln, level, text_ in headings:
        if prev and level > prev + 1:
            errors.append(f"{path}:{ln}: heading level jumps from h{prev} to h{level}")
        prev = level
        first, _, rest = text_.partition(" ")
        if not first or not all(is_emoji(c) for c in first) or not rest.strip():
            errors.append(f"{path}:{ln}: heading must start with one emoji and a space: '{text_}'")
            continue
        if "️" in first or "‍" in first or len(first) != 1:
            errors.append(f"{path}:{ln}: use a single-codepoint emoji (no variation selector/joiner) so the anchor stays clean")
        elif first not in PALETTE:
            warns.append(f"{path}:{ln}: '{first}' is not in the palette (may render as text on some platforms); see `doc_format.py palette`")
        if any(is_emoji(c) for c in INLINE_CODE_RE.sub("", rest)):
            errors.append(f"{path}:{ln}: only one emoji per heading")
    for ln, line in body:
        if any(is_emoji(c) for c in INLINE_CODE_RE.sub("", line)):
            errors.append(f"{path}:{ln}: emoji outside a heading: {line.strip()[:70]}")
    if len([h for h in headings if h[1] in TOC_LEVELS]) >= 1:
        span = toc_span(lines)
        if not span:
            errors.append(f"{path}: missing TOC (run `doc_format.py toc {path}`)")
        else:
            current = lines[span[0]:span[1] + 1]
            if current != build_toc(lines):
                errors.append(f"{path}: TOC is out of date (run `doc_format.py toc {path}`)")
    seen: Dict[str, int] = {}
    anchors = {slugify(t, seen) for _, _, t in headings}
    for ln, line in body:
        for m in LINK_RE.finditer(line):
            if m.group(2) not in anchors:
                errors.append(f"{path}:{ln}: link to missing anchor #{m.group(2)}")
    return errors, warns


def main(argv: List[str]) -> int:
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__.strip())
        return 0 if argv else 2
    cmd, files = argv[0], argv[1:]
    if cmd == "palette":
        for e, meaning in PALETTE.items():
            print(f"{e}  {meaning}")
        return 0
    if cmd not in ("toc", "check") or not files:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    status = 0
    for path in files:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        if cmd == "toc":
            new = insert_toc(text)
            if new != text:
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(new)
                print(f"{path}: TOC updated")
            else:
                print(f"{path}: TOC already current")
        else:
            errors, warns = check(path, text)
            for w in warns:
                print(f"WARN  {w}")
            for e in errors:
                print(f"ERROR {e}")
            status |= 1 if errors else 0
            if not errors and not warns:
                print(f"{path}: OK")
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
