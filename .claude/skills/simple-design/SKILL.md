---
name: simple-design
description: Keep code and changes small, plain and free of bloat. Covers YAGNI, no premature abstraction, built-in language features before new dependencies, readable code over clever code, small single-purpose changes, and protecting public interfaces from accidental contracts (Hyrum's law). Use when designing or implementing a change, adding a dependency or abstraction, splitting work into commits or units, or reviewing a diff for bloat or scope.
---

# Simple design

Write the least code that fully meets the requirement, in a form the next reader understands on first read. Draws on public material from *Software Engineering at Google* and Google's engineering-practices guide on code review.

## 1. Build only what is asked (YAGNI)
- Implement the stated requirement. Don't add options, flags, hooks or extension points for needs nobody has stated.
- **No abstraction with one consumer.** Don't add an interface, base class, factory or generic wrapper until a second real caller exists. At the third similar case, extract the shared part.
- Delete code you made unnecessary in the same change. Don't leave dead branches, unused parameters or "just in case" helpers.

## 2. Built-in first, dependencies last
- Use the language's standard library and the project's existing helpers before writing new utilities or adding a package. Search the codebase (KG first) for a helper that already does it.
- Don't add a dependency for something a few lines of standard code can do (padding a string, deep-cloning plain data, a simple retry).
- Do use a well-maintained library for hard, risky problems: cryptography, parsing standard formats, dates and time zones, HTML sanitizing. Hand-rolling those is the bigger risk (see secure-coding).
- New dependencies need the user's approval (the harness asks). In your reply, say why the standard library wasn't enough.

## 3. Readable over clever
- Prefer plain, explicit code: named intermediate values over dense one-liners, early returns over deep nesting, clear names over comments that explain unclear names.
- Match the surrounding code's idioms, naming and comment density. Consistency beats your personal preference.
- A comment says *why*, not *what*. If the code needs a comment to say what it does, rename or restructure first.

## 4. Small, single-purpose changes
- One change does one conceptual thing: a feature, a fix, or a refactor, never a mix. Put refactors and behavior changes in separate commits.
- If the work grows, split it into ordered steps that each leave the build green, with the dependency order stated (for example: add the new function, then switch callers, then remove the old one).
- Aim for diffs a reviewer can hold in their head (a few hundred changed lines at most). Mechanical changes such as renames or generated code can be larger, but keep them in their own commit.

## 5. Protect public interfaces (Hyrum's law)
With enough users, every observable behavior of an interface gets depended on, whether or not you promised it.
- Keep the public surface small: export only what callers need, and mark the rest internal (`_private` names, unexported symbols, `internal/` packages, non-exported modules).
- Don't return internal objects or mutable state that callers could start relying on. Return copies or narrow types.
- Treat changes to observable behavior as interface changes even when the signature stays the same: ordering of results, error types and messages, default values, timing, side effects. Call them out in the change description.
- Don't make promises in docs or names that the code doesn't keep.

## Review checklist
- [ ] Every new abstraction, option and file has a current, named consumer.
- [ ] No new dependency where the standard library or an existing helper would do; any new dependency is justified.
- [ ] The code reads top to bottom without decoding tricks.
- [ ] The change does one thing; refactors are separate from behavior changes.
- [ ] Public surface didn't grow without reason, and observable behavior changes are called out.
