---
name: ask-me
description: Answer a question about this repo cheaply. The Haiku repo-explorer looks it up knowledge-graph first and returns a short answer with path:line pointers, so the search never fills the main session's context.
argument-hint: "<question about the code>"
disable-model-invocation: true
context: fork
agent: repo-explorer
---

# /ask-me: answer a question about the repo

Question: $ARGUMENTS

If the question is empty, reply only: `Usage: /ask-me <question about the code>`.

1. **Knowledge graph first.** Run `python3 .claude/tools/kg.py query <key terms>`. If a node answers it, open the node, check the cited `path:line` with a ranged read, and stop.
2. **Search only for what the graph doesn't cover.** Grep with a tight pattern and a `head_limit`, Glob for file names, and read line ranges, never whole large files.
3. **Stop as soon as you can answer.** Don't survey the whole repo unless the question asks for every occurrence.

Reply in this shape and nothing else:

```
<the answer in 1-5 lines, plain words>

Where:
- path:line: what is there

KG: <none | gap <topic>>
```

`KG: gap <topic>` means the graph had nothing on it. The main session can then ask knowledge-keeper to add a node, so the next question costs less.
