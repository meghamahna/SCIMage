---
name: diagram-maker
description: Build clear, accessible Mermaid or SVG diagrams (architecture, flowchart, sequence, ER, C4 container, state) that read well in light and dark mode. Use when asked to draw, diagram, visualize, sketch architecture, make a flowchart/sequence/ER/state diagram, or create an SVG figure.
allowed-tools: Bash(python3 .claude/tools/svg_lint.py *)
---

# Diagram maker

Each diagram carries one idea and is drawn from the **real** code, never from an imagined architecture.

## Workflow
1. **Ground it.** Get facts from the knowledge graph first: `INDEX.md` → node → `path:line`. Every box and arrow must map to real code or a real system. Note the source pointers.
2. **Pick the form:**

   | Question the reader has | Diagram |
   |---|---|
   | How do the pieces connect? | C4-style container / flowchart LR |
   | What happens, in what order? | sequenceDiagram |
   | What decisions/branches exist? | flowchart TD |
   | What does the data look like? | erDiagram |
   | What states can X be in? | stateDiagram-v2 |
3. **Pick the medium.** Use **Mermaid** for docs that live in Markdown. Use **SVG** when you need precise layout, custom styling, or standalone embedding.
4. **Pick the theme and start from its template.** Read `diagrams.theme` in `.claude/harness.json` (`light` by default; `dark` or `auto` if the user chose them). Copy the matching file and keep its palette; don't invent colors.
   - SVG, systems: `references/svg-architecture-<theme>.svg`. SVG, steps and decisions: `references/svg-flow-<theme>.svg`.
   - Mermaid: [references/mermaid-templates.md](references/mermaid-templates.md) (light init line by default; its "Dark theme" section has the dark one).
   - `auto` follows the viewer's light/dark setting. Use it only when the user asks: GitHub and many viewers apply the OS setting, not the page theme, so a fixed theme looks the same for everyone.
5. **Apply the rules below**, then lint:
   `python3 .claude/tools/svg_lint.py <file.svg|file.md|file.mmd>` and fix every ERROR. Fix WARNs unless they are justified.
6. **Hand off** to `diagram-reviewer` for non-trivial diagrams.

## Rules (both media)
- **At most 12 nodes per view.** Split into an overview plus detail views rather than cramming everything into one.
- **One flow direction:** LR for systems, TD for decisions. Minimize crossing edges and group related nodes in subgraphs/boundaries.
- **Labels:** short nouns for nodes, verbs for edges ("publishes", "reads"). Use sentence case and no jargon the reader won't know.
- **Color carries meaning.** Use palette roles, and include a legend if you use more than 2 roles:
  - `svc`: services
  - `data`: stores
  - `ext`: external systems
  - `user`: actors
  - `accent`: the one thing to notice
- **Never rely on color alone.** Pair it with shape (cylinder = store) or a label.
- **Accessibility:** Mermaid gets `accTitle` and `accDescr`; SVG gets `<title>`, `<desc>` and `role="img"`.

## Precision standard
Build every diagram so that a reader who notices every misaligned pixel finds nothing to fix. `svg_lint.py` checks the items marked (lint). Check the rest by eye before you hand off.

**Layout**
- Box positions and sizes sit on an 8px grid; text anchors, edge points and corner radii on a 4px grid (lint).
- Boxes in a row share one height and one centre line; boxes in a column share one width (lint). Use one box size per role across the diagram unless size carries meaning.
- Gaps between neighbours are identical along a row or column (lint), and the outer margin is the same on all four sides.
- Nothing almost lines up. If two edges or centres are within 8px of each other, make them identical (lint).
- Boxes never overlap, and containers keep at least 16px between their border and the boxes inside (lint).

**Edges**
- Use horizontal and vertical runs only (lint warns on diagonals), with one bend style for the whole diagram.
- No edge crosses another edge or passes through a box (lint). If one would, move the boxes rather than bending the line around them.
- Edges start and end exactly on a box border, and the arrow tip touches the border (lint warns). The template's marker (`markerUnits="userSpaceOnUse"`, `refX` at the tip) does this.
- Edge labels are centred on the gap they label, sit the same distance from every line, and never touch a line or a box (lint).
- Every arrow uses the same head and points the way data or control flows.

**Text**
- Labels are centred in their box (lint) with at least 12px of padding on each side (lint warns).
- Every box in a row uses the same label offsets, so baselines line up across the row (lint).
- One font family, at most three font sizes, two stroke widths and two corner radii (lint).
- One casing everywhere (sentence case), the same names the code uses, and no trailing punctuation.

**Colour and legend**
- Colours come only from the palette, one role per colour. Add a legend when more than two roles appear, with items in equal-width slots aligned to the diagram's left margin.

**Mermaid** (you don't control coordinates, so control everything else)
- Declare a direction, keep the template's `flowchart` spacing and curve settings, and style every node through a classDef: lint warns on unstyled nodes and errors on inline `style`.
- Keep labels to 32 characters or fewer in one case, and use at most two arrow styles (lint).
- Declare nodes in reading order and group them with subgraphs. Mermaid places nodes in declaration order, so the order you write controls how tidy the result is.
- Indent with 2 spaces, with no tabs or trailing spaces (lint).

**Final pass:** render the diagram in light and dark (open the SVG, or preview the Markdown on GitHub or in VS Code) and look for anything uneven. Lint must show no ERROR and no WARN; if a WARN has to stay, give the reason in one line in your reply.

## Conventions
- **Line meaning:** solid for the main flow, dashed (`edge-secondary`) for secondary or return flow, red (`edge-err` with `#arrow-err`) for error and deny paths. Don't use opacity to mark a path; faded lines fail contrast.
- **Title and caption** for standalone diagrams (blogs, slides, docs without surrounding text): a `title` at the top-left margin, and a one-sentence caption in `sub` text at the bottom-left that states what the reader should take away.
- **Flow shapes:** start and end are pills (`rx` = half the height), steps are boxes, decisions are diamonds (`<polygon>`, as wide as the column). Every element in a column has the same width.
- **Canvas presets** (all on the 8px grid): architecture, left to right, 704–800 x 288–400; flow, top to bottom, 528–608 wide and as tall as the steps need; component internals, sized to content.
- **Blueprint grid (optional):** the templates define a 16px `#grid` pattern in theme colours. To use it, add a second full-size background rect with `fill="url(#grid)"`. Leave it off unless the user wants the look.
- **Export (only when asked for an image):** keep the SVG as the source. For PNG, run `rsvg-convert -w 1600 in.svg -o out.png` (librsvg); for WebP, convert that PNG with Pillow using `lossless=True`. Both may need installing, so ask the user first.

## SVG specifics
- `viewBox` only (no fixed width/height), an 8px grid for every coordinate and size, and 8–16px padding inside boxes.
- Fonts: `font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`, at a minimum of 12px (the linter errors below 11).
- Colours come only from the CSS custom properties in the template's `:root` block (fixed for `light` and `dark`; `auto` adds an `@media (prefers-color-scheme: dark)` override). Use `currentColor` for strokes where possible.
- Stroke widths are 1.5px for boxes and edges. Arrowheads come from the template's markers (`#arrow`, and `#arrow-err` for error paths), with the tip on the border.
- **Forbidden:** `<script>`, `on*` handlers, `<foreignObject>`, external `href`/`url()`, and embedded raster images.

## Output
- Put the file next to the doc that uses it (e.g. `docs/diagrams/<name>.svg`), or inline the Mermaid in the doc.
- In your reply, list the source pointers each part of the diagram came from.
