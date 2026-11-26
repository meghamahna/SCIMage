# Accessibility and UI review checklist (WCAG 2.2 AA)

## Perceivable
- [ ] Text contrast ≥ 4.5:1 (large text ≥ 3:1) in light and dark themes.
- [ ] Non-text contrast ≥ 3:1: borders of inputs, icons that convey meaning, focus rings.
- [ ] Images have appropriate `alt`; decorative ones are hidden from assistive tech.
- [ ] Info is not conveyed by color alone (add an icon, text or pattern).
- [ ] Content reflows at 320px width; text resizes to 200% without loss.
- [ ] Video has captions; audio has transcripts.

## Operable
- [ ] All functionality is reachable by keyboard; no keyboard traps.
- [ ] Logical focus order; visible focus indicator; focused element not obscured by sticky headers (2.4.11).
- [ ] A skip link to main content on pages with repeated navigation.
- [ ] Target size ≥ 24×24px (2.5.8).
- [ ] Dragging has a single-pointer alternative (2.5.7).
- [ ] No time limits, or they are adjustable; auto-moving content can be paused.
- [ ] Respects `prefers-reduced-motion`.

## Understandable
- [ ] `<html lang>` set; labels and instructions are visible.
- [ ] Errors are identified in text, with suggestions; linked via `aria-describedby`.
- [ ] Consistent navigation and help location (3.2.6).
- [ ] No redundant entry (3.3.7); accessible authentication with no cognitive tests and paste allowed (3.3.8).

## Robust
- [ ] Valid semantic HTML; ARIA roles, states and properties correct (`aria-expanded`, `aria-controls`, etc.).
- [ ] Status messages announced via `role="status"` / `aria-live`.
- [ ] Custom widgets follow WAI-ARIA Authoring Practices patterns (dialog, menu, tabs, combobox).

## Component patterns
| Widget | Must have |
|---|---|
| Dialog | `role="dialog"`, `aria-modal="true"`, labelled title, focus trapped inside, Esc closes, focus returns to the trigger |
| Tabs | `role="tablist"/"tab"/"tabpanel"`, arrow keys move between tabs, `aria-selected` |
| Menu button | `aria-haspopup`, `aria-expanded`, arrow keys, Esc |
| Toggle | `button` with `aria-pressed`, or `role="switch"` with `aria-checked` |
| Form field | `<label for>`, hint and error via `aria-describedby`, `aria-invalid` on error |

## Testing
- Keyboard-only walkthrough.
- Screen reader smoke test (VoiceOver on macOS: Cmd+F5).
- Automated: axe (`@axe-core/*`, `jest-axe`, `playwright` + axe) in component and e2e tests. axe catches only some issues, so passing it doesn't mean the UI is accessible.
