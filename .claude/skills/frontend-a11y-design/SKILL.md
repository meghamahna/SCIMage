---
name: frontend-a11y-design
description: Clean, accessible, responsive UI. Covers design tokens, spacing and type scales, WCAG 2.2 AA contrast, keyboard and focus, semantic HTML before ARIA, reduced motion, dark mode, and responsive layout. Use when building or reviewing UI components, pages, forms, styles, themes or design systems, and for any frontend/accessibility/a11y request.
---

# Frontend: accessible, clean design

## 1. Use the project's system first
- Find existing tokens, components and conventions before writing new ones. Check the KG (`kg.py query ui component tokens`), the theme files, and the component library.
- Never hard-code colors, spacing or font sizes in components. Use tokens or CSS variables.

## 2. Tokens and scales (when none exist)
- **Spacing:** a 4px base with steps `4 8 12 16 24 32 48 64`.
- **Type:** base 16px with scale `12 14 16 18 20 24 30 36`; line-height 1.5 for body and 1.2–1.3 for headings; at most ~75ch line length.
- **Radius:** `4 8 12`, plus full for pills.
- **Color:** semantic tokens (`--color-bg`, `--color-fg`, `--color-muted`, `--color-accent`, `--color-danger`, `--color-border`), each redefined for dark mode via `prefers-color-scheme` and/or a `[data-theme]` attribute.

Token template: [references/tokens.css](references/tokens.css).

## 3. WCAG 2.2 AA essentials
- **Contrast:** text needs at least 4.5:1 (3:1 for 18px+ bold or 24px+ text). UI components and focus indicators need at least 3:1. Check both light and dark themes.
- **Semantics first:** `button` for actions, `a` for navigation, a proper heading order, `label` for every input, and landmarks (`header/nav/main/footer`). Use ARIA only when HTML can't express it.
- **Keyboard:**
  - Everything operable by keyboard, in logical tab order, with no traps.
  - Esc closes dialogs, and focus returns to the trigger afterwards.
- **Focus:** a visible `:focus-visible` ring (at least 2px, 3:1 contrast). Never `outline: none` without a replacement. Focused elements must not be obscured (2.4.11).
- **Target size:** at least 24×24px (2.5.8), with 44×44 preferred for touch.
- **Forms:**
  - Visible labels, and errors tied to inputs with `aria-describedby`.
  - No information conveyed by color alone.
  - Don't make users re-enter data you already have (3.3.7), and allow paste in auth fields (3.3.8).
- **Images and icons:** meaningful ones get `alt`; decorative ones get `alt=""` or `aria-hidden="true"`. Icon-only buttons need an `aria-label`.
- **Motion:** respect `prefers-reduced-motion`, give anything that auto-plays a pause control, and never flash more than 3 times per second.
- **Dynamic content:** `aria-live="polite"` for async status messages, and dialogs use `role="dialog"` + `aria-modal` + a labelled title.

## 4. Responsive layout
- Mobile-first CSS, fluid layouts with grid/flex, and `min()`/`clamp()` for type and spacing.
- No horizontal scrolling at 320px width (reflow, 1.4.10), and content usable at 200% zoom.
- Container queries for components where they are supported.

## 5. Review checklist
- [ ] Tokens only; no magic numbers or colors.
- [ ] Keyboard walkthrough done; focus visible everywhere.
- [ ] Contrast checked in light **and** dark.
- [ ] Screen-reader names for all controls; headings in order.
- [ ] 320px width and 200% zoom work; touch targets at least 24px.
- [ ] Reduced motion respected.
- [ ] Component tests include role-based queries (`getByRole`), plus an axe check if available.

More: [references/checklist.md](references/checklist.md).
