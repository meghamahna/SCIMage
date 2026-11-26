---
name: doc-standards
description: Documentation standards and templates for READMEs, ADRs (architecture decision records), API docs, runbooks and changelogs. Covers voice, structure, length rules, and verifying code samples. Use when writing, updating or reviewing any documentation, README, ADR, API reference, runbook, guide or docstring.
allowed-tools: Bash(python3 .claude/tools/doc_format.py *)
---

# Documentation standards

## Principles
- **Write for a named reader** (new contributor, API consumer, on-call engineer) and their task. Put what they need first.
- **Be accurate first.** Every command, path, flag and code sample must match the code. Verify against source and give `path:line` evidence in your return block.
- **Be concise.** Short sentences, active voice, present tense, second person ("Run …").
  - No marketing language and no filler ("simply", "just", "easy").
- **Format rules are mandatory.** Every document has a linked table of contents (generate it with `python3 .claude/tools/doc_format.py toc <file>`) and one relevant emoji at the start of every heading, with no emoji anywhere else. Details: the "Format rules" section in `.claude/agents/doc-writer.md`. Check with `python3 .claude/tools/doc_format.py check <file>`.
- **Voice rules are mandatory.** Follow the "Voice rules" section in `.claude/agents/doc-writer.md`: plain words, no em-dashes, no hype words, and none of the four AI-prose patterns (staccato pairs, antithesis reframe, isocolon metaphor-pairs, backward-reference callbacks).
- **Structure for scanning.** Descriptive headings, numbered steps for procedures, tables for options, and code blocks with a language tag.
- **Show, then explain.** A runnable example before the prose.
- **One source of truth.** Link instead of duplicating. Don't copy config values that will drift; point to the file instead.
- **No secrets and no real PII** in docs or examples. Use env var names and `example.com`.
- **Diagrams** go through the `diagram-maker` skill, and each one needs an alt text or `accDescr`.

## Templates (load the one you need)
| Doc | Template |
|---|---|
| README | [references/readme.md](references/readme.md) |
| ADR | [references/adr.md](references/adr.md) |
| API reference | [references/api.md](references/api.md) |
| Runbook | [references/runbook.md](references/runbook.md) |

## Length guidance
- README: one screen to "running locally"; details go in linked docs.
- ADR: 1–2 pages.
- Runbook step: a single action plus its expected result.
- Docstrings: say what and why (not how), plus params, returns and raises for public APIs.

## Changelog
- [Keep a Changelog](https://keepachangelog.com) sections: Added, Changed, Deprecated, Removed, Fixed, Security.
- Write user-facing wording, and link issues/PRs.

## Review checklist (doc-reviewer)
- [ ] The audience and purpose are clear from the first paragraph.
- [ ] Every command was run or traced to source; paths and flags exist (`path:line` evidence).
- [ ] No stale versions, dead links, secrets or PII.
- [ ] Headings scan well; procedures are numbered; code blocks are tagged.
- [ ] Matches the project's existing doc tone and terminology (check the KG `glossary-*` nodes).
- [ ] KG `doc-*` node updated or created for major docs (`documented_in` links).
