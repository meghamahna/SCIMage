#!/usr/bin/env python3
"""Diagram quality linter for SVG files and Mermaid blocks.

Beyond safety and accessibility it enforces a precision standard: an 8px grid, exact alignment,
equal spacing, consistent sizes and styles, centered labels, and edges that never cross or cut boxes.

Usage: python3 .claude/tools/svg_lint.py <file.svg|file.md|file.mmd>...
Prints `path: ERROR|WARN: message` lines; exits 1 if any ERROR was reported.
"""
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

MAX_NODES = 12
MIN_FONT_SIZE = 11
SVG_NS = "{http://www.w3.org/2000/svg}"
XLINK_HREF = "{http://www.w3.org/1999/xlink}href"

HEX_COLOR = re.compile(r"#[0-9a-fA-F]{3,8}\b")
FONT_SIZE_ATTR = re.compile(r"font-size\s*:\s*([0-9.]+)\s*(px)?", re.I)
MERMAID_FENCE = re.compile(r"^```mermaid[^\n]*\n(.*?)^```", re.M | re.S)
MERMAID_TYPES = re.compile(
    r"^\s*(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(-v2)?|erDiagram|"
    r"journey|gantt|pie|C4Context|C4Container|C4Component|C4Dynamic|mindmap|timeline|"
    r"gitGraph|quadrantChart|requirementDiagram|sankey-beta|xychart-beta|block-beta|architecture-beta)\b",
    re.M,
)
# Node id followed by a shape opener: A[...], B(...), C{...}, D((...)), E>...]
FLOW_NODE = re.compile(r"(?<![\w-])([A-Za-z_][\w-]*)\s*(\[\[|\[\(|\[/|\[\\|\(\(|\(\[|\[|\(|\{\{|\{|>)")
FLOW_EDGE_ID = re.compile(r"(?:^|-->|---|-\.->|==>|--[^>-]*-->|&)\s*([A-Za-z_][\w-]*)\s*(?=$|-->|---|-\.->|==>|&|;|\s)", re.M)
SEQ_PARTICIPANT = re.compile(r"^\s*(?:participant|actor)\s+([^\s]+)", re.M)
SEQ_MESSAGE = re.compile(r"^\s*([\w-]+)\s*-[->x)]+[+-]?\s*([\w-]+)\s*:", re.M)
ER_REL = re.compile(r"^\s*([\w-]+)\s+[|}{o.\-]+\s+([\w-]+)\s*:", re.M)
ER_ENTITY = re.compile(r"^\s*([\w-]+)\s*\{", re.M)
STATE_TRANS = re.compile(r"^\s*([\w\[\]*-]+)\s*-->\s*([\w\[\]*-]+)", re.M)
C4_ELEM = re.compile(r"^\s*(?:Person|System|Container|Component|ContainerDb|SystemDb|System_Ext|Person_Ext|ContainerQueue)(?:_Ext)?\s*\(\s*([\w-]+)", re.M)
CLASS_DEF = re.compile(r"^\s*class(Def)?\s", re.M)
FLOW_KEYWORDS = {"subgraph", "end", "classDef", "class", "style", "linkStyle", "click", "direction", "flowchart", "graph"}


class Report:
    def __init__(self):
        self.lines = []
        self.errors = 0

    def error(self, path, msg):
        self.errors += 1
        self.lines.append(f"{path}: ERROR: {msg}")

    def warn(self, path, msg):
        self.lines.append(f"{path}: WARN: {msg}")


def _local(tag):
    return tag.split("}", 1)[1] if "}" in tag else tag


def lint_svg(path, text, rep):
    try:
        root = ET.fromstring(text)
    except ET.ParseError as exc:
        rep.error(path, f"not well-formed XML ({exc})")
        return
    if _local(root.tag) != "svg":
        rep.error(path, "root element is not <svg>")
        return
    if "viewBox" not in root.attrib:
        rep.error(path, "root <svg> has no viewBox (needed for crisp scaling)")
    elements = list(root.iter())
    if not any(_local(el.tag) == "title" for el in elements):
        rep.error(path, "missing <title> (accessibility)")
    if not any(_local(el.tag) == "desc" for el in elements):
        rep.warn(path, "missing <desc> (accessibility)")
    if root.attrib.get("role") != "img":
        rep.warn(path, 'root <svg> should have role="img"')

    for el in elements:
        tag = _local(el.tag)
        if tag == "script":
            rep.error(path, "<script> is not allowed")
        if tag == "foreignObject":
            rep.error(path, "<foreignObject> is not allowed")
        for name, value in el.attrib.items():
            lname = _local(name)
            if lname.lower().startswith("on"):
                rep.error(path, f"event handler attribute '{lname}' on <{tag}> is not allowed")
            if (lname == "href" or name == XLINK_HREF) and re.match(r"\s*(https?:)?//", value):
                rep.error(path, f"external reference '{value}' on <{tag}> is not allowed")
            if lname == "font-size":
                _check_font_size(path, value, rep)
            if lname == "style":
                for m in FONT_SIZE_ATTR.finditer(value):
                    _check_font_size(path, m.group(1), rep)

    styles = "\n".join((el.text or "") for el in elements if _local(el.tag) == "style")
    for m in FONT_SIZE_ATTR.finditer(styles):
        _check_font_size(path, m.group(1), rep)
    if re.search(r"url\(\s*['\"]?(https?:)?//", styles + text):
        rep.error(path, "external url() reference is not allowed")

    themed = "prefers-color-scheme" in text or "currentColor" in text or "var(--" in text
    if HEX_COLOR.search(text) and not themed:
        rep.warn(path, "hardcoded hex colors without CSS variables/currentColor/prefers-color-scheme; will not adapt to dark mode")

    lint_precision(path, root, text, rep)


def _check_font_size(path, value, rep):
    m = re.match(r"\s*([0-9.]+)\s*(px)?\s*$", str(value))
    if m and float(m.group(1)) < MIN_FONT_SIZE:
        rep.error(path, f"font-size {m.group(1)} is below minimum {MIN_FONT_SIZE}")


def _strip_quoted(s):
    return re.sub(r'"[^"]*"', '""', s)


def _bracket_balanced(line):
    pairs = {"]": "[", ")": "(", "}": "{"}
    stack = []
    for ch in _strip_quoted(line):
        if ch in "[({":
            stack.append(ch)
        elif ch in pairs:
            if not stack or stack[-1] != pairs[ch]:
                return False
            stack.pop()
    return not stack


def _count_nodes(body):
    m = MERMAID_TYPES.search(body)
    kind = m.group(1) if m else ""
    lines = [ln for ln in body.splitlines() if ln.strip() and not ln.strip().startswith("%%")]
    text = "\n".join(lines)
    nodes = set()
    if kind in ("flowchart", "graph"):
        for ln in lines:
            s = ln.strip()
            first = s.split()[0] if s.split() else ""
            if first in FLOW_KEYWORDS:
                continue
            for nm in FLOW_NODE.finditer(_strip_quoted(s)):
                nodes.add(nm.group(1))
            for em in FLOW_EDGE_ID.finditer(_strip_quoted(s)):
                if em.group(1) not in FLOW_KEYWORDS:
                    nodes.add(em.group(1))
    elif kind == "sequenceDiagram":
        nodes.update(SEQ_PARTICIPANT.findall(text))
        for a, b in SEQ_MESSAGE.findall(text):
            nodes.update((a, b))
    elif kind == "erDiagram":
        for a, b in ER_REL.findall(text):
            nodes.update((a, b))
        nodes.update(ER_ENTITY.findall(text))
    elif kind.startswith("stateDiagram"):
        for a, b in STATE_TRANS.findall(text):
            nodes.update((a, b))
    elif kind.startswith("C4"):
        nodes.update(C4_ELEM.findall(text))
    return kind, len(nodes)


def lint_mermaid_block(path, body, idx, rep):
    label = f"mermaid block {idx}"
    content = [ln for ln in body.splitlines() if ln.strip() and not ln.strip().startswith("%%")]
    if not content:
        rep.error(path, f"{label} is empty")
        return
    kind, count = _count_nodes(body)
    if not kind:
        rep.warn(path, f"{label}: unknown or missing diagram type")
    if count > MAX_NODES:
        rep.warn(path, f"{label}: {count} nodes (> {MAX_NODES}); split into smaller views")
    if "%%{init" not in body and not CLASS_DEF.search(body):
        rep.warn(path, f"{label}: no %%{{init}}%% theme or classDef palette; use the shared palette")
    lint_mermaid_precision(path, body, label, kind, rep)
    if kind in ("flowchart", "graph", "stateDiagram", "stateDiagram-v2"):
        for n, ln in enumerate(body.splitlines(), 1):
            s = ln.strip()
            if not s or s.startswith("%%"):
                continue
            if not _bracket_balanced(s):
                rep.error(path, f"{label} line {n}: unbalanced brackets in node label: {s[:80]}")


# ---------------------------------------------------------------- precision (SVG)
# The standard: someone who notices every misaligned pixel finds nothing to fix.
GRID = 8          # boxes: position and size
FINE_GRID = 4     # text anchors, edge points, radii
MIN_PAD = 12      # label padding inside a box (each side)
CONTAINER_PAD = 16
SKIP_CONTAINERS = {"defs", "marker", "pattern", "symbol", "clipPath", "mask", "linearGradient", "radialGradient"}
NUM = re.compile(r"-?\d*\.?\d+(?:e-?\d+)?")
CSS_RULE = re.compile(r"([^{}]+)\{([^{}]*)\}")


def _num(v):
    try:
        return float(str(v).replace("px", "").strip())
    except (TypeError, ValueError):
        return None


def _off(v, grid):
    return v is not None and abs(v / grid - round(v / grid)) > 1e-6


def _css_classes(style_text):
    rules = {}
    for sel, body in CSS_RULE.findall(re.sub(r"@media[^{]*\{", "", style_text)):
        for one in sel.split(","):
            one = one.strip()
            if one.startswith("."):
                rules.setdefault(one[1:], []).append(body)
            elif one == "text":
                rules.setdefault("@text", []).append(body)
    return rules


def _font_size(el, rules):
    fs = _num(el.attrib.get("font-size"))
    if fs:
        return fs
    m = FONT_SIZE_ATTR.search(el.attrib.get("style", ""))
    if m:
        return float(m.group(1))
    for cls in el.attrib.get("class", "").split() + ["@text"]:
        for body in rules.get(cls, []):
            m = FONT_SIZE_ATTR.search(body)
            if m:
                return float(m.group(1))
    return 16.0


def _path_points(d):
    """Walk M/L/H/V/A/C/S/Q/T/Z (abs + rel). Returns (points, all_absolute_orthogonal_ok, has_relative)."""
    toks = re.findall(r"[MmLlHhVvAaCcSsQqTtZz]|" + NUM.pattern, d)
    pts, x, y, cmd, i, start = [], 0.0, 0.0, None, 0, (0.0, 0.0)
    has_rel = False
    argc = {"M": 2, "L": 2, "H": 1, "V": 1, "A": 7, "C": 6, "S": 4, "Q": 4, "T": 2, "Z": 0}
    while i < len(toks):
        t = toks[i]
        if re.match(r"[A-Za-z]", t):
            cmd = t
            i += 1
            if cmd in "Zz":
                x, y = start
                pts.append((x, y))
            continue
        if cmd is None:
            break
        U = cmd.upper()
        n = argc[U]
        args = [float(v) for v in toks[i:i + n]]
        i += n
        if len(args) < n:
            break
        rel = cmd.islower()
        has_rel |= rel
        if U == "H":
            x = x + args[0] if rel else args[0]
        elif U == "V":
            y = y + args[0] if rel else args[0]
        else:
            nx, ny = args[-2], args[-1]
            x, y = (x + nx, y + ny) if rel else (nx, ny)
        if U == "M":
            start = (x, y)
            if cmd == "M":
                cmd = "L"
            elif cmd == "m":
                cmd = "l"
        pts.append((x, y))
    return pts, has_rel


def _collect(root):
    """Absolute geometry with translate() applied. Returns dict of lists and a flag for unsupported transforms."""
    out = {"rects": [], "texts": [], "edges": [], "shapes": [], "solids": [], "points": [], "unsupported": False}

    def walk(el, tx, ty):
        tag = _local(el.tag)
        if tag in SKIP_CONTAINERS:
            return
        tr = el.attrib.get("transform")
        if tr:
            m = re.fullmatch(r"\s*translate\(\s*(" + NUM.pattern + r")(?:[\s,]+(" + NUM.pattern + r"))?\s*\)\s*", tr)
            if not m:
                out["unsupported"] = True
                return
            tx, ty = tx + float(m.group(1)), ty + float(m.group(2) or 0)
        a = el.attrib
        if tag == "rect":
            r = {k: _num(a.get(k, 0)) or 0.0 for k in ("x", "y", "width", "height")}
            out["rects"].append({"x": r["x"] + tx, "y": r["y"] + ty, "w": r["width"], "h": r["height"],
                                 "rx": _num(a.get("rx")), "el": el})
        elif tag == "text":
            out["texts"].append({"x": (_num(a.get("x")) or 0) + tx, "y": (_num(a.get("y")) or 0) + ty,
                                 "anchor": a.get("text-anchor", "start"),
                                 "text": "".join(el.itertext()).strip(), "el": el})
        elif tag in ("line", "path", "polyline"):
            is_edge = "edge" in a.get("class", "") or "marker-end" in a or "marker-start" in a
            if tag == "line":
                pts = [((_num(a.get("x1")) or 0) + tx, (_num(a.get("y1")) or 0) + ty),
                       ((_num(a.get("x2")) or 0) + tx, (_num(a.get("y2")) or 0) + ty)]
                rel = False
            elif tag == "polyline":
                vals = [float(v) for v in NUM.findall(a.get("points", ""))]
                pts, rel = [(vals[i] + tx, vals[i + 1] + ty) for i in range(0, len(vals) - 1, 2)], False
            else:
                raw, rel = _path_points(a.get("d", ""))
                pts = [(px + tx, py + ty) for px, py in raw]
            if pts:
                (out["edges"] if is_edge else out["shapes"]).append({"pts": pts, "rel": rel, "el": el})
        elif tag == "polygon":
            vals = [float(v) for v in NUM.findall(a.get("points", ""))]
            pts = [(vals[i] + tx, vals[i + 1] + ty) for i in range(0, len(vals) - 1, 2)]
            if pts:
                out["solids"].append(_bbox(pts))
        elif tag in ("circle", "ellipse"):
            cx, cy = (_num(a.get("cx")) or 0) + tx, (_num(a.get("cy")) or 0) + ty
            rx = _num(a.get("r") if tag == "circle" else a.get("rx")) or 0
            ry = _num(a.get("r") if tag == "circle" else a.get("ry")) or 0
            out["points"].append((cx, cy, tag))
            out["solids"].append({"x": cx - rx, "y": cy - ry, "w": 2 * rx, "h": 2 * ry})
        for child in el:
            walk(child, tx, ty)

    walk(root, 0.0, 0.0)
    return out


def _inside(px, py, r, strict=False):
    if strict:
        return r["x"] < px < r["x"] + r["w"] and r["y"] < py < r["y"] + r["h"]
    return r["x"] <= px <= r["x"] + r["w"] and r["y"] <= py <= r["y"] + r["h"]


def _contains(a, b):
    return (a is not b and a["x"] <= b["x"] and a["y"] <= b["y"] and
            a["x"] + a["w"] >= b["x"] + b["w"] and a["y"] + a["h"] >= b["y"] + b["h"])


def _seg_cross(p1, p2, p3, p4):
    """Proper crossing of two segments (touching at endpoints does not count)."""
    def orient(a, b, c):
        v = (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])
        return 0 if abs(v) < 1e-9 else (1 if v > 0 else -1)
    if {p1, p2} & {p3, p4}:
        return False
    o1, o2, o3, o4 = orient(p1, p2, p3), orient(p1, p2, p4), orient(p3, p4, p1), orient(p3, p4, p2)
    return o1 * o2 < 0 and o3 * o4 < 0


def _seg_through_rect(p, q, r):
    """True if the segment passes through the rect's interior (not just touching its border)."""
    steps = 32
    for k in range(1, steps):
        t = k / steps
        if _inside(p[0] + (q[0] - p[0]) * t, p[1] + (q[1] - p[1]) * t, r, strict=True):
            return True
    return False


def _on_border(px, py, r, tol=0.5):
    inx = r["x"] - tol <= px <= r["x"] + r["w"] + tol
    iny = r["y"] - tol <= py <= r["y"] + r["h"] + tol
    on_v = abs(px - r["x"]) <= tol or abs(px - (r["x"] + r["w"])) <= tol
    on_h = abs(py - r["y"]) <= tol or abs(py - (r["y"] + r["h"])) <= tol
    return (on_v and iny) or (on_h and inx)


def _bbox(pts):
    xs, ys = [p[0] for p in pts], [p[1] for p in pts]
    return {"x": min(xs), "y": min(ys), "w": max(xs) - min(xs), "h": max(ys) - min(ys)}


def _fmt(v):
    return f"{v:g}"


def lint_precision(path, root, text, rep):
    vb = [_num(v) for v in root.attrib.get("viewBox", "").replace(",", " ").split()]
    if len(vb) == 4 and any(_off(v, GRID) for v in vb if v is not None):
        rep.error(path, f"viewBox {' '.join(_fmt(v) for v in vb)} is not on the {GRID}px grid")
    g = _collect(root)
    if g["unsupported"]:
        rep.warn(path, "transform other than translate(); geometry checks skipped for that subtree")
    styles = "\n".join((el.text or "") for el in root.iter() if _local(el.tag) == "style")
    rules = _css_classes(styles)

    # style consistency
    def visible(el):
        if _local(el.tag) in SKIP_CONTAINERS:
            return
        yield el
        for child in el:
            yield from visible(child)
    attr_text = " ".join(f'{k}:{v};' for el in visible(root) for k, v in el.attrib.items()
                         if k in ("stroke-width", "font-size", "font-family", "style"))
    blob = styles + " " + attr_text
    widths = {_fmt(float(v)) for v in re.findall(r"stroke-width\s*:\s*([\d.]+)", blob)}
    if len(widths) > 2:
        rep.error(path, f"{len(widths)} different stroke widths ({', '.join(sorted(widths))}); use at most 2 (boxes, emphasis)")
    sizes = {_fmt(float(v)) for v in re.findall(r"font-size\s*:\s*([\d.]+)", blob)}
    if len(sizes) > 3:
        rep.error(path, f"{len(sizes)} different font sizes ({', '.join(sorted(sizes))}); use at most 3 (title, label, caption)")
    families = {re.sub(r"\s+", " ", f.strip().rstrip(";")) for f in re.findall(r"font-family\s*:\s*([^;}\n]+)", blob)}
    if len(families) > 1:
        rep.error(path, f"{len(families)} different font-family stacks; use one")

    # boxes
    bg = [r for r in g["rects"] if len(vb) == 4 and r["x"] <= (vb[0] or 0) and r["y"] <= (vb[1] or 0)
          and r["w"] >= vb[2] and r["h"] >= vb[3]]
    rects = [r for r in g["rects"] if r not in bg]
    for r in rects:
        bad = [k for k in ("x", "y", "w", "h") if _off(r[k], GRID)]
        if bad:
            rep.error(path, f"box at ({_fmt(r['x'])},{_fmt(r['y'])}) size {_fmt(r['w'])}x{_fmt(r['h'])} is off the {GRID}px grid ({', '.join(bad)})")
        if _off(r["rx"], FINE_GRID):
            rep.error(path, f"corner radius {_fmt(r['rx'])} is off the {FINE_GRID}px grid")
    radii = {_fmt(r["rx"] or 0) for r in rects}
    if len(radii) > 2:
        rep.error(path, f"{len(radii)} different corner radii ({', '.join(sorted(radii))}); use one for boxes and one for small markers")

    nodes = [r for r in rects if r["w"] >= 32 and r["h"] >= 24]
    containers = [r for r in nodes if any(_contains(r, o) for o in nodes)]
    leaves = [r for r in nodes if r not in containers]
    for c in containers:
        for b in leaves:
            if _contains(c, b):
                pad = min(b["x"] - c["x"], b["y"] - c["y"], c["x"] + c["w"] - b["x"] - b["w"], c["y"] + c["h"] - b["y"] - b["h"])
                if pad < CONTAINER_PAD:
                    rep.error(path, f"box at ({_fmt(b['x'])},{_fmt(b['y'])}) is {_fmt(pad)}px from its container edge; keep >= {CONTAINER_PAD}px")
    for i, a in enumerate(leaves):
        for b in leaves[i + 1:]:
            if a["x"] < b["x"] + b["w"] and b["x"] < a["x"] + a["w"] and a["y"] < b["y"] + b["h"] and b["y"] < a["y"] + a["h"]:
                rep.error(path, f"boxes overlap at ({_fmt(a['x'])},{_fmt(a['y'])}) and ({_fmt(b['x'])},{_fmt(b['y'])})")

    # near-miss alignment: values that are almost, but not exactly, equal
    for label, key in (("left edges", lambda r: r["x"]), ("top edges", lambda r: r["y"]),
                       ("horizontal centers", lambda r: r["x"] + r["w"] / 2),
                       ("vertical centers", lambda r: r["y"] + r["h"] / 2)):
        vals = sorted({key(r) for r in leaves})
        for v1, v2 in zip(vals, vals[1:]):
            if 0 < v2 - v1 < GRID:
                rep.error(path, f"near-miss alignment of {label}: {_fmt(v1)} vs {_fmt(v2)}; align them exactly")

    # rows and columns: equal sizes, equal gaps
    def groups(key, items=None):
        out = {}
        for r in (leaves if items is None else items):
            out.setdefault(key(r), []).append(r)
        return {k: v for k, v in out.items() if len(v) >= 2}

    spaced = leaves + g["solids"]

    for cy, row in groups(lambda r: r["y"] + r["h"] / 2).items():
        row.sort(key=lambda r: r["x"])
        if len({r["h"] for r in row}) > 1:
            rep.error(path, f"boxes in the row at y={_fmt(cy)} have different heights ({', '.join(sorted({_fmt(r['h']) for r in row}))})")
        if len({r["w"] for r in row}) > 1:
            rep.warn(path, f"boxes in the row at y={_fmt(cy)} have different widths ({', '.join(sorted({_fmt(r['w']) for r in row}))})")
    for cy, row in groups(lambda r: r["y"] + r["h"] / 2, spaced).items():
        row.sort(key=lambda r: r["x"])
        gaps = [b["x"] - (a["x"] + a["w"]) for a, b in zip(row, row[1:])]
        if len(gaps) >= 2 and max(gaps) != min(gaps):
            rep.error(path, f"uneven horizontal spacing in the row at y={_fmt(cy)}: gaps {', '.join(_fmt(x) for x in gaps)}")
    for cx, col in groups(lambda r: r["x"] + r["w"] / 2).items():
        col.sort(key=lambda r: r["y"])
        if len({r["w"] for r in col}) > 1:
            rep.error(path, f"boxes in the column at x={_fmt(cx)} have different widths ({', '.join(sorted({_fmt(r['w']) for r in col}))})")
    for cx, col in groups(lambda r: r["x"] + r["w"] / 2, spaced).items():
        col.sort(key=lambda r: r["y"])
        gaps = [b["y"] - (a["y"] + a["h"]) for a, b in zip(col, col[1:])]
        if len(gaps) >= 2 and max(gaps) != min(gaps):
            rep.error(path, f"uneven vertical spacing in the column at x={_fmt(cx)}: gaps {', '.join(_fmt(x) for x in gaps)}")

    # labels
    owner = {}
    for t in g["texts"]:
        if _off(t["x"], FINE_GRID) or _off(t["y"], FINE_GRID):
            rep.error(path, f"text '{t['text'][:24]}' at ({_fmt(t['x'])},{_fmt(t['y'])}) is off the {FINE_GRID}px grid")
        fs = _font_size(t["el"], rules)
        width = len(t["text"]) * fs * 0.6
        box = min((r for r in leaves if _inside(t["x"], t["y"], r)), key=lambda r: r["w"] * r["h"], default=None)
        t["box"] = box
        if box is None:
            if t["anchor"] == "middle":
                x0, x1 = t["x"] - width / 2, t["x"] + width / 2
            elif t["anchor"] == "end":
                x0, x1 = t["x"] - width, t["x"]
            else:
                x0, x1 = t["x"], t["x"] + width
            y0, y1 = t["y"] - fs * 0.8, t["y"] + fs * 0.3
            for e in g["edges"]:
                for p, q in zip(e["pts"], e["pts"][1:]):
                    if any(x0 < p[0] + (q[0] - p[0]) * k / 16 < x1 and y0 < p[1] + (q[1] - p[1]) * k / 16 < y1 for k in range(17)):
                        rep.error(path, f"label '{t['text'][:24]}' collides with an edge; move it clear of the line")
                        break
            continue
        owner.setdefault(id(box), []).append(t)
        if t["anchor"] == "middle" and t["x"] != box["x"] + box["w"] / 2:
            rep.error(path, f"label '{t['text'][:24]}' is not centered in its box (x={_fmt(t['x'])}, center={_fmt(box['x'] + box['w'] / 2)})")
        if width > box["w"] - 2 * MIN_PAD:
            rep.warn(path, f"label '{t['text'][:24]}' is about {_fmt(round(width))}px wide in a {_fmt(box['w'])}px box; keep >= {MIN_PAD}px padding each side")
    for cy, row in groups(lambda r: r["y"] + r["h"] / 2).items():
        offsets = {tuple(sorted(t["y"] - r["y"] for t in owner.get(id(r), []))) for r in row if owner.get(id(r))}
        if len(offsets) > 1:
            rep.error(path, f"label baselines differ between boxes in the row at y={_fmt(cy)}; use the same offsets in every box")

    # edges
    segs = []
    for n, e in enumerate(g["edges"]):
        if e["rel"]:
            rep.warn(path, "edge path uses relative commands; use absolute M/H/V so alignment can be verified")
        for x, y in e["pts"]:
            if _off(x, FINE_GRID) or _off(y, FINE_GRID):
                rep.error(path, f"edge point ({_fmt(x)},{_fmt(y)}) is off the {FINE_GRID}px grid")
        for p, q in zip(e["pts"], e["pts"][1:]):
            if p[0] != q[0] and p[1] != q[1]:
                rep.warn(path, f"diagonal edge segment ({_fmt(p[0])},{_fmt(p[1])}) to ({_fmt(q[0])},{_fmt(q[1])}); prefer horizontal and vertical runs")
            segs.append((n, p, q))
            for r in leaves:
                if _seg_through_rect(p, q, r):
                    rep.error(path, f"edge passes through the box at ({_fmt(r['x'])},{_fmt(r['y'])}); route around it")
        targets = leaves + g["solids"] + [_bbox(s["pts"]) for s in g["shapes"] if len(s["pts"]) > 1]
        for end in (e["pts"][0], e["pts"][-1]):
            if not any(_on_border(end[0], end[1], r) for r in targets):
                rep.warn(path, f"edge end ({_fmt(end[0])},{_fmt(end[1])}) does not touch a box border; start and end edges exactly on borders")
    for i, (n1, p1, q1) in enumerate(segs):
        for n2, p2, q2 in segs[i + 1:]:
            if n1 != n2 and _seg_cross(p1, q1, p2, q2):
                rep.error(path, f"edges cross near ({_fmt((p1[0] + q1[0]) / 2)},{_fmt((p1[1] + q1[1]) / 2)}); reroute so no lines cross")


# ---------------------------------------------------------------- precision (Mermaid)
DIRECTION = re.compile(r"^\s*(flowchart|graph)\s+(LR|RL|TD|TB|BT)\b", re.M)
LABEL_RE = re.compile(r"(?<=[\w-])(?:\[\[|\[\(|\(\(|\(\[|\{\{|\[|\(|\{|>)\"?([^\[\]\(\)\{\}\"]+)\"?[\]\)\}]+")
ARROWS = ("-.->", "==>", "-->", "---")


def _label_case(label):
    words = [w for w in re.findall(r"[A-Za-z][A-Za-z'/-]*", label)]
    plain = [w for w in words if not (w.isupper() and len(w) > 1)]
    if len(plain) < 2:
        return None
    caps = sum(1 for w in plain if w[0].isupper())
    if caps == len(plain):
        return "Title Case"
    if plain[0][0].isupper() and caps == 1:
        return "Sentence case"
    return None


def lint_mermaid_precision(path, body, label, kind, rep):
    lines = body.splitlines()
    if kind in ("flowchart", "graph") and "'flowchart'" not in body:
        rep.warn(path, f"{label}: init block has no 'flowchart' layout settings; copy the template's init line")
    if kind in ("flowchart", "graph") and not DIRECTION.search(body):
        rep.error(path, f"{label}: flowchart has no direction; declare LR (systems) or TD (decisions)")
    if not re.search(r"^\s*accTitle\s*:", body, re.M) or not re.search(r"^\s*accDescr\s*[:{]", body, re.M):
        rep.warn(path, f"{label}: add accTitle and accDescr for screen readers")
    for n, ln in enumerate(lines, 1):
        s = ln.strip()
        if re.match(r"style\s+\S+", s):
            rep.error(path, f"{label} line {n}: inline `style`; use a classDef from the shared palette")
        if re.match(r"linkStyle\s+\d", s):
            rep.warn(path, f"{label} line {n}: linkStyle by index breaks when edges change; avoid it")
        if "\t" in ln:
            rep.warn(path, f"{label} line {n}: tab indentation; use 2 spaces")
        elif ln != ln.rstrip():
            rep.warn(path, f"{label} line {n}: trailing whitespace")
        elif s and (len(ln) - len(ln.lstrip())) % 2:
            rep.warn(path, f"{label} line {n}: indentation is not a multiple of 2 spaces")
    if kind not in ("flowchart", "graph"):
        return
    code = [ln for ln in lines if ln.strip() and not ln.strip().startswith(
        ("%%", "classDef", "class ", "accTitle", "accDescr", "subgraph", "end", "direction"))]
    labels = []
    for ln in code:
        for m in LABEL_RE.finditer(ln):
            text = re.sub(r"<[^>]+>", "\n", m.group(1)).split("\n")[0].strip()
            if text:
                labels.append(text)
    for t in labels:
        if len(t) > 32:
            rep.warn(path, f"{label}: label '{t[:32]}...' is over 32 characters; shorten it")
    cases = {c for c in (_label_case(t) for t in labels) if c}
    if len(cases) > 1:
        rep.warn(path, f"{label}: labels mix Title Case and Sentence case; pick one (sentence case)")
    used = set()
    for ln in code:
        rest = ln
        for a in ARROWS:
            if a in rest:
                used.add(a)
                rest = rest.replace(a, " ")
    if len(used) > 2:
        rep.warn(path, f"{label}: {len(used)} arrow styles ({' '.join(sorted(used))}); use at most 2 and explain them in a legend")
    if CLASS_DEF.search(body):
        defined = {m.group(1) for ln in code for m in FLOW_NODE.finditer(_strip_quoted(ln))
                   if m.group(1) not in FLOW_KEYWORDS}
        styled = set(re.findall(r"([A-Za-z_][\w-]*)\s*(?:\[[^\]]*\]|\([^)]*\)|\{[^}]*\}|\[\([^)]*\)\])?:::", body))
        for m in re.finditer(r"^\s*class\s+([\w,\s-]+?)\s+\w+\s*$", body, re.M):
            styled.update(x.strip() for x in m.group(1).split(","))
        unstyled = sorted(d for d in defined - styled if d not in ("subgraph",))
        if unstyled:
            rep.warn(path, f"{label}: nodes without a palette class: {', '.join(unstyled[:8])}")


def lint_markdown(path, text, rep, whole_is_mermaid=False):
    blocks = [text] if whole_is_mermaid else MERMAID_FENCE.findall(text)
    if not blocks:
        rep.warn(path, "no mermaid blocks found")
        return
    for i, body in enumerate(blocks, 1):
        lint_mermaid_block(path, body, i, rep)


def lint_file(path, rep):
    p = Path(path)
    try:
        text = p.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        rep.error(path, f"cannot read file ({exc})")
        return
    suffix = p.suffix.lower()
    if suffix == ".svg":
        lint_svg(path, text, rep)
    elif suffix == ".mmd":
        lint_markdown(path, text, rep, whole_is_mermaid=True)
    elif suffix in (".md", ".markdown"):
        lint_markdown(path, text, rep)
    else:
        rep.error(path, "unsupported file type (expected .svg, .md or .mmd)")


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__.strip())
        return 0 if argv else 2
    rep = Report()
    for path in argv:
        lint_file(path, rep)
    for line in rep.lines:
        print(line)
    if not rep.lines:
        print("svg_lint: OK")
    return 1 if rep.errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
