#!/usr/bin/env python3
"""PreToolUse Bash: block destructive commands and secret exfiltration.

The command line is parsed into simple commands (split on ; && || | & and
newlines, with $(...), backticks, <(...), `bash -c`, `eval`, `xargs`, `find -exec`
and common wrappers unwrapped) and every simple command is checked.
Settings `permissions.deny` mirrors the most important rules as a second layer.
"""
from __future__ import annotations

import glob
import os
import re
import shlex
import sys
from typing import List, Optional, Tuple

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib"))
try:
    from common import UNBLOCK_HINT, ask, deny, load_config, scan_local, project_dir, run_guard  # noqa: E402
    from detectors import SENSITIVE_INLINE_RE, Allowlist, sensitive_path_reason, summarize  # noqa: E402
except Exception:  # noqa: BLE001 - a broken install must not let the call through
    import json
    _MSG = "[harness:security] guard could not load (harness files damaged); blocked to be safe. Re-run install.py."
    sys.stdout.write(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny",
                                           "permissionDecisionReason": _MSG}}))
    sys.exit(0)

Verdict = Optional[Tuple[str, str]]  # ("deny"|"ask", reason)

SEPARATORS = {";", "&&", "||", "|", "&", "\n", "(", ")", "|&", ";;"}
REDIRECTS = {">", ">>", "<", "<<", "<<<", ">|", "&>", "&>>", "2>", "2>>", ">&", "<&"}
WRAPPERS = {"nohup", "time", "nice", "command", "builtin", "exec", "stdbuf", "caffeinate", "unbuffer",
            "setsid", "ionice", "watch", "busybox", "chronic"}
# Wrapper options that take a separate value (`nice -n 10 cmd`); other dash options are dropped on their own.
WRAPPER_ARG_OPTS = {"-n", "-s", "-k", "-a", "-o", "-f", "-c", "-t", "-w", "-i", "-e", "--signal", "--kill-after",
                    "--adjustment", "--output", "--format", "--interval"}
FETCHERS = {"curl", "wget", "fetch", "base64", "xxd"}
DOWNLOADERS = {"curl", "wget", "fetch"}
INLINE_DELETE_RE = re.compile(r"shutil\.(?:rmtree|move)|os\.(?:remove|unlink|rmdir|removedirs)\b|\.unlink\(|\.rmtree\(|"
                              r"fs\.(?:rm|unlink|rmdir)(?:Sync)?\b|rimraf|File\.delete|FileUtils\.rm|\bunlink\s*\(")
# Inline code that starts another program which deletes: subprocess.run(["rm", ...]), os.system("rm -rf x").
INLINE_SHELLOUT_RE = re.compile(r"subprocess\.|os\.(?:system|popen|exec[lv]p?e?)\b|child_process|execSync|spawnSync|"
                                r"\bsystem\s*\(|\bexec\s*\(|%x\{|Kernel\.")
INLINE_SHELLOUT_DELETE_RE = re.compile(r"(?:^|[\s'\"\[(,;|&])(?:rm|rmdir|unlink|shred|truncate|dd|sudo|mkfs\w*)(?:$|[\s'\",\])])")
# Harness files named inside inline code (python -c, heredoc scripts), where no argument shows the path.
HARNESS_INLINE_RE = re.compile(r"(?i)\.claude[/\\]+(?:settings(?:\.local)?\.json|harness\.json|hooks|tools|security|state|"
                               r"\.harness-manifest)|(?:^|[\s'\"/\\(=])\.git[/\\]+(?:hooks|config)\b")
# `sh -c "$(curl ...)"`, `eval "$(wget ...)"`, `bash <(curl ...)`, `source <(curl ...)`: same as `curl | sh`.
_FETCH = r"(?:\$\(|`|<\()\s*(?:command\s+|exec\s+|env\s+)?(?:\S*/)?(?:curl|wget|fetch)\b"
FETCH_RUN_RE = re.compile(
    r"(?:(?:^|[\s;&|(])(?:eval|source|(?:ba|z|k|da|fi)?sh)|(?:^|[;&|(])\s*\.)\s[^;&|\n]*?" + _FETCH +
    r"|(?:^|[\s;&|(])(?:python3?|node|ruby|perl|php|deno|bun)\s+(?:-\S+\s+)*(?:-c|-e|--eval)?\s*[\"']?" + _FETCH)
GIT_EXEC_CONFIG = re.compile(r"(?i)^(?:alias\.|core\.(?:pager|hookspath|sshcommand|editor|fsmonitor)|"
                             r"diff\.external|pager\.|credential\.helper|sequence\.editor)")
SHELLS = {"sh", "bash", "zsh", "dash", "ksh", "fish"}
INTERPRETERS = {"python", "python3", "node", "ruby", "perl", "php", "deno", "bun"}
PIPE_SINKS = SHELLS | INTERPRETERS | {"source", "."}
# Pre-approved in settings.json because they only read. With an output redirect they write, so that asks.
READ_ONLY_CMDS = {"cat", "head", "tail", "wc", "tree", "grep", "rg", "ls", "pwd"}
PATH_ONLY_CMDS = {"ls", "test", "[", "[[", "stat", "touch", "mkdir", "realpath", "dirname", "basename"}
WRITER_CMDS = {"tee", "mv", "cp", "rm", "chmod", "chown", "ln", "truncate", "dd", "install", "patch",
               "rsync", "sed", "perl", "awk", "python", "python3", "node", "ruby", "unlink", "shred",
               "tar", "unzip", "cpio", "touch", "mkdir"}
DB_CLIS = {"psql", "mysql", "mariadb", "sqlite3", "mongosh", "mongo", "redis-cli", "cqlsh", "clickhouse-client"}
SECRET_VAR_RE = re.compile(r"\$\{?[A-Za-z_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIAL)[A-Za-z_]*\}?", re.I)
DESTRUCTIVE_SQL = re.compile(r"(?is)\b(?:drop\s+(?:table|database|schema|user|role)|truncate\s+(?:table\s+)?\w|"
                             r"delete\s+from\s+[\w.\"`]+\s*(?:;|$|\"|')|flushall|flushdb|dropDatabase\s*\()")
FORK_BOMB = re.compile(r":\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:")
HARNESS_PROTECTED = re.compile(r"(?i)(?:^|/)(?:\.claude(?:/(?:settings(?:\.local)?\.json|harness\.json|hooks|tools|security|state|"
                               r"\.harness-manifest)(?:/|\b)|/?$)|\.git(?:/|$))")
# Files that run on their own or that Claude is told to follow in later sessions. Changing one asks the user.
AUTORUN_FILES = re.compile(r"(?i)(?:^|/)(?:\.mcp\.json$|\.claude/(?:agents|skills|commands)(?:/|$)|CLAUDE(?:\.local)?\.md$|"
                           r"AGENTS\.md$|\.github/workflows(?:/|$)|\.husky(?:/|$)|\.vscode/tasks\.json$)")
AUTORUN_REASON = ("changes what runs automatically or what Claude is told in every later session. "
                  "Approve only if you asked for this change")
# Set by analyze() so nested rule checks (find -exec, xargs) can recurse and look at the project.
_CTX = {"allow": None, "proj": "", "cwd": "", "install_scripts": "block"}
# Harness tools read and write sensitive files safely themselves; matched on the path inside this project.
HARNESS_TOOL = re.compile(r"^\.claude/tools/(?:safe_read|kg|save|svg_lint|doc_format|cleanup|doctor|stack)\.py$")
ASSIGN_RE = re.compile(r"^([A-Za-z_][A-Za-z0-9_]*)=(.*)$", re.S)
VAR_RE = re.compile(r"\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))")
PKG_NOTE = "Packages run code on this machine. Approve only if you asked for it or recognise the name."


# --------------------------------------------------------------------- parsing

def strip_heredocs(cmd: str) -> Tuple[str, List[Tuple[str, str]]]:
    """Remove heredoc bodies; return them as (header line, body) for content checks."""
    bodies: List[Tuple[str, str]] = []
    lines = cmd.split("\n")
    out, i = [], 0
    while i < len(lines):
        line = lines[i]
        out.append(line)
        delims = re.findall(r"<<-?\s*(['\"]?)([A-Za-z_][\w]*)\1", line)
        i += 1
        for _, d in delims:
            body = []
            while i < len(lines) and lines[i].strip() != d:
                body.append(lines[i])
                i += 1
            bodies.append((line, "\n".join(body)))
            i += 1  # skip delimiter
    return "\n".join(out), bodies


def extract_substitutions(cmd: str) -> List[str]:
    """Inner commands of $(...), `...`, <(...), >(...) (balanced parens)."""
    subs: List[str] = []
    i = 0
    while i < len(cmd):
        if cmd.startswith(("$(", "<(", ">("), i) and not cmd.startswith("$((", i):
            depth, j = 1, i + 2
            while j < len(cmd) and depth:
                depth += {"(": 1, ")": -1}.get(cmd[j], 0)
                j += 1
            subs.append(cmd[i + 2:j - 1])
            i = j
        elif cmd[i] == "`":
            j = cmd.find("`", i + 1)
            if j == -1:
                break
            subs.append(cmd[i + 1:j])
            i = j + 1
        else:
            i += 1
    return subs


def normalize_lines(cmd: str) -> str:
    """Unquoted newlines become `;`, line continuations are joined, comments dropped."""
    out, q, i = [], None, 0
    while i < len(cmd):
        ch = cmd[i]
        if q:
            if ch == "\\" and q == '"' and i + 1 < len(cmd):
                out.append(cmd[i:i + 2])
                i += 2
                continue
            if ch == q:
                q = None
            out.append(ch)
        elif ch == "\\" and i + 1 < len(cmd) and cmd[i + 1] == "\n":
            i += 2
            continue
        elif ch == "\\" and i + 1 < len(cmd):
            out.append(cmd[i:i + 2])
            i += 2
            continue
        elif ch in "'\"":
            q = ch
            out.append(ch)
        elif ch == "\n":
            out.append(" ; ")
        elif ch == "#" and (i == 0 or cmd[i - 1] in " \t;&|("):
            while i < len(cmd) and cmd[i] != "\n":
                i += 1
            continue
        else:
            out.append(ch)
        i += 1
    return "".join(out)


def tokenize(cmd: str) -> List[str]:
    lex = shlex.shlex(normalize_lines(cmd), posix=True, punctuation_chars=";&|()<>")
    lex.whitespace_split = True
    lex.commenters = ""
    return list(lex)


def split_simple(tokens: List[str]) -> List[List[str]]:
    cmds, cur = [], []
    for t in tokens:
        if t in SEPARATORS or (set(t) <= set(";&|()") and t):
            if cur:
                cmds.append(cur)
            cur = []
            if t in {"|", "|&"}:
                cmds.append(["|"])
        else:
            cur.append(t)
    if cur:
        cmds.append(cur)
    return cmds


def unwrap(argv: List[str]) -> List[str]:
    """Strip env assignments and wrapper commands; return the effective argv."""
    while argv:
        a0 = os.path.basename(argv[0])
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", argv[0]):
            argv = argv[1:]
        elif a0 in WRAPPERS:
            rest = argv[1:]
            if a0 == "command" and rest[:1] and rest[0] in {"-v", "-V"}:
                return []  # `command -v x` only looks a command up
            while rest and rest[0].startswith("-") and rest[0] != "--":
                opt = rest.pop(0)
                if opt in WRAPPER_ARG_OPTS and rest:
                    rest.pop(0)
            if rest[:1] == ["--"]:
                rest = rest[1:]
            argv = rest
        elif a0 == "env":
            rest = argv[1:]
            while rest and (rest[0].startswith("-") or "=" in rest[0]):
                rest = rest[1:]
            if not rest:
                return argv  # bare `env` = dump environment
            argv = rest
        elif a0 == "timeout":
            rest = [x for x in argv[1:]]
            while rest and rest[0].startswith("-"):
                opt = rest.pop(0)
                if opt in {"-s", "--signal", "-k", "--kill-after"} and rest:
                    rest.pop(0)
            argv = rest[1:]  # drop duration
        elif a0 == "flock":
            rest = [x for x in argv[1:] if not x.startswith("-")]
            argv = rest[1:]  # drop the lock file
        else:
            break
    if argv:
        argv = [os.path.basename(argv[0])] + argv[1:]
    return argv


# --------------------------------------------------------------------- rules

def _flags(args: List[str]) -> set:
    out = set()
    for a in args:
        if a.startswith("--"):
            out.add(a.split("=", 1)[0])
        elif a.startswith("-") and len(a) > 1:
            out.update("-" + c for c in a[1:])
    return out


def git_rules(args: List[str]) -> Verdict:
    i = 0
    while i < len(args) and args[i].startswith("-"):
        i += 2 if args[i] in {"-C", "-c", "--git-dir", "--work-tree", "--namespace"} else 1
    for k in range(i):
        if args[k] == "-c" and k + 1 < len(args) and GIT_EXEC_CONFIG.match(args[k + 1]):
            return "deny", "`git -c` settings that run other commands (alias, pager, hooks path) are not allowed"
    if i >= len(args):
        return None
    sub, rest = args[i], args[i + 1:]
    f = _flags(rest)
    if sub in {"diff", "log", "show", "format-patch", "bundle", "archive"}:
        for k, a in enumerate(rest):
            target = a.split("=", 1)[1] if a.startswith("--output=") else \
                rest[k + 1] if a in {"--output", "-o"} and k + 1 < len(rest) else None
            if target is not None:
                if HARNESS_PROTECTED.search(os.path.normpath(target).replace(os.sep, "/")):
                    return "deny", f"`git {sub} --output` into harness files is not allowed"
                return "ask", f"`git {sub} --output` writes (and can overwrite) '{target}'; confirm"
    if sub == "config" and any(GIT_EXEC_CONFIG.match(a) for a in rest if not a.startswith("-")):
        return "ask", "this git config setting can run other commands; confirm"
    if sub == "switch" and f & {"-f", "--force", "--discard-changes", "-C", "--force-create"}:
        return "ask", "`git switch` with force/-C discards local changes or moves a branch; confirm"
    if sub == "worktree" and rest[:1] == ["remove"] and f & {"-f", "--force"}:
        return "ask", "`git worktree remove --force` discards that worktree's changes; confirm"
    if sub == "push":
        if f & {"--force", "-f", "--force-with-lease", "--force-if-includes", "--mirror", "--delete", "-d", "--prune"} \
                or any(a.startswith("+") or (a.startswith(":") and len(a) > 1) for a in rest):
            return "deny", "force/destructive `git push` is not allowed"
        return "ask", "`git push` publishes commits; confirm with the user"
    if sub == "reset" and ("--hard" in f or "--merge" in f or "--keep" in f):
        return "deny", "`git reset --hard` discards work"
    if sub == "clean" and ("-f" in f or "--force" in f):
        return "deny", "`git clean -f` deletes untracked files"
    if sub == "branch" and ("-D" in f or ("--delete" in f and ("--force" in f or "-f" in f)) or "-f" in f or "--force" in f):
        return "deny", "force-deleting/moving branches is not allowed"
    whole_tree = any(a in {".", ":/", "*"} or a.endswith("/") for a in rest if not a.startswith("-"))
    if sub == "checkout" and (whole_tree or "-f" in f or "--force" in f):
        return "deny", "`git checkout .`/`-f` discards all local changes"
    if sub == "checkout" and ("--" in rest or "-B" in f):
        return "ask", "`git checkout -- <file>`/`-B` discards local changes to those files or moves a branch; confirm"
    if sub == "restore" and not (f & {"--staged", "-S"} and not f & {"--worktree", "-W"}):
        if whole_tree or not [a for a in rest if not a.startswith("-")]:
            return "deny", "`git restore` of the whole working tree discards local changes (use --staged to unstage)"
        return "ask", "`git restore <file>` discards local changes to those files; confirm"
    if sub in {"filter-branch", "filter-repo"}:
        return "deny", "history rewriting is not allowed"
    if sub == "update-ref" and "-d" in f:
        return "deny", "deleting refs is not allowed"
    if sub == "stash" and rest[:1] and rest[0] in {"drop", "clear"}:
        return "deny", "dropping stashes destroys work"
    if sub == "reflog" and rest[:1] and rest[0] in {"expire", "delete"}:
        return "deny", "expiring the reflog destroys recovery data"
    if sub == "gc" and any(a.startswith("--prune") for a in rest):
        return "deny", "`git gc --prune` destroys recovery data"
    if sub == "credential" or (sub == "config" and any("credential" in a for a in rest) and "--get" in " ".join(rest)):
        return "deny", "reading git credentials is not allowed"
    if sub == "rebase":
        return "ask", "`git rebase` rewrites history; confirm with the user"
    if sub == "commit" and ("--amend" in f or "--no-verify" in f or "-n" in f):
        return "ask", "amending/skipping hooks on a commit needs confirmation"
    if sub == "tag" and ("-d" in f or "--delete" in f):
        return "deny", "deleting tags is not allowed"
    if sub == "tag":
        return "ask", "creating tags needs confirmation"
    return None


def shell_code(argv: List[str]) -> Optional[str]:
    """The inline script of `bash -c`, `sh -lc`, `zsh -ec` and friends, or None."""
    for k, a in enumerate(argv[1:], 1):
        if re.match(r"^-[A-Za-z]*c[A-Za-z]*$", a):
            return argv[k + 1] if k + 1 < len(argv) else ""
    return None


def _inner_rules(inner: List[str]) -> Verdict:
    """Rules for a command run by find -exec / xargs, including `sh -c '...'`."""
    if inner[0] in SHELLS:
        code = shell_code(inner)
        if code is not None and _CTX["allow"] is not None:
            v = analyze(code, _CTX["allow"], _CTX["proj"], 2)
            if v:
                return v
    return cmd_rules(inner)


def _local_bin(name: str) -> bool:
    return any(os.path.exists(os.path.join(d, "node_modules", ".bin", name)) for d in {_CTX["proj"] or ".", os.getcwd()})


def install_scripts_rule(c: str, sub: str, args: List[str]) -> Verdict:
    """Package installs run each package's install scripts, which is how most malicious npm packages attack.
    Installs made by an agent must switch them off (supply_chain.install_scripts in harness.json)."""
    mode = _CTX.get("install_scripts", "block")
    if mode == "allow":
        return None
    if "--ignore-scripts" in args or "--mode=skip-build" in args or \
            any(a == "--mode" and args[i + 1:i + 2] == ["skip-build"] for i, a in enumerate(args)):
        return None
    what = f"`{c} {sub or 'install'}` would run the install scripts of every package it installs, the usual way a malicious package attacks. "
    if mode == "ask":
        return "ask", what + "Approve only if you trust every package being installed."
    flag = "`--mode=skip-build` (Yarn 2+) or `--ignore-scripts` (Yarn 1)" if c == "yarn" else "`--ignore-scripts`"
    return "deny", (what + f"Re-run it with {flag}. If a package then fails because it needs its build step, name that "
                    "package to the user and let them run the build themselves (for npm: `npm rebuild <package>`).")


def cmd_rules(argv: List[str]) -> Verdict:
    c, args = argv[0], argv[1:]
    f = _flags(args)
    joined = " ".join(args)
    if c in {"sudo", "su", "doas", "pkexec"}:
        return "deny", f"`{c}` (privilege escalation) is not allowed"
    if c in {"rm", "rmdir", "unlink", "shred", "srm", "trash", "wipefs", "fdisk", "sfdisk", "parted"} or c.startswith("mkfs"):
        return "deny", f"`{c}` deletes data; deletion is reserved for the user"
    if c == "truncate" or c == "dd":
        return "deny", f"`{c}` can destroy file contents"
    if c == "diskutil" and args[:1] and re.match(r"(?i)(erase|zero|secureErase|partition|reformat|apfs)", args[0]):
        return "deny", "disk-level operations are not allowed"
    if c == "find" and ("-delete" in args or any(a in {"-exec", "-execdir", "-ok", "-okdir"} for a in args)):
        idx = [i for i, a in enumerate(args) if a in {"-exec", "-execdir", "-ok", "-okdir"}]
        if "-delete" in args:
            return "deny", "`find -delete` deletes files"
        for i in idx:
            inner = unwrap(args[i + 1:])
            if inner:
                v = _inner_rules([x for x in inner if x not in {"{}", ";", "+", "\\;"}] or inner)
                if v:
                    return v
    if c == "xargs":
        rest = list(args)
        while rest and rest[0].startswith("-"):
            opt = rest.pop(0)
            if opt in {"-I", "-n", "-P", "-L", "-d", "-E", "-s"} and rest:
                rest.pop(0)
        inner = unwrap(rest)
        if inner:
            return _inner_rules(inner)
    if c in {"chmod"} and (("-R" in f or "--recursive" in f) or re.search(r"\b(?:777|666|a\+rwx|o\+w)\b", joined)):
        if re.search(r"\b(?:777|666|a\+rwx|o\+w)\b", joined) or "-R" in f:
            return "deny", "recursive or world-writable chmod is not allowed"
    if c in {"chown", "chgrp", "chattr", "chflags"} and ("-R" in f or c in {"chattr", "chflags"}):
        return "deny", f"recursive `{c}` is not allowed"
    if c == "git":
        return git_rules(args)
    if c in {"shutdown", "reboot", "halt", "poweroff", "launchctl", "systemctl"} and \
            (c not in {"launchctl", "systemctl"} or re.search(r"\b(stop|disable|unload|remove|bootout|mask|kill)\b", joined)):
        return "deny", f"`{c}` system control is not allowed"
    if c == "crontab" and "-r" in f:
        return "deny", "`crontab -r` deletes all cron jobs"
    if c in {"kill", "pkill", "killall"}:
        if c == "kill" and re.search(r"(?:^|\s)-?(?:1|-1)$", joined.strip()) and ("-9" in args or "-KILL" in args):
            return "deny", "killing init/all processes is not allowed"
        if c != "kill":
            return "ask", f"`{c}` terminates processes by name; confirm"
    if c == "history" and ("-c" in f or "-w" in f):
        return "deny", "clearing shell history is not allowed"
    if c in {"docker", "podman"}:
        if re.search(r"\b(system|volume|image|container|network|builder)\s+prune\b", joined) or \
                re.search(r"\b(rm|rmi|volume\s+rm)\b.*(-f|--force)", joined):
            return "deny", "destructive docker operation"
        if re.search(r"\bcompose\b.*\bdown\b.*(-v|--volumes)", joined):
            return "ask", "`docker compose down -v` deletes the project's volumes (local data); confirm"
        if args[:1] == ["push"]:
            return "ask", "`docker push` publishes an image; confirm"
        if args[:1] == ["login"]:
            return "deny", "`docker login` handles credentials; ask the user to run it"
    if c == "kubectl":
        if args[:1] and args[0] in {"delete", "drain", "cordon", "replace"}:
            return "deny", f"`kubectl {args[0]}` is destructive"
        if re.search(r"\bget\s+secrets?\b", joined) or re.search(r"config\s+view\b.*--raw", joined):
            return "deny", "reading cluster secrets/credentials is not allowed"
        if args[:1] and args[0] in {"apply", "scale", "rollout", "patch", "edit"}:
            return "ask", f"`kubectl {args[0]}` changes a cluster; confirm"
    if c in {"terraform", "tofu", "pulumi"}:
        if args[:1] and args[0] in {"destroy"} or "-auto-approve" in args or "--yes" in args:
            return "deny", f"`{c} {args[0] if args else ''}` without review is not allowed"
        if args[:2] in (["state", "list"], ["state", "show"]):
            return None  # read-only
        if args[:1] and args[0] in {"apply", "up", "import", "state"}:
            return "ask", f"`{c} {args[0]}` changes infrastructure; confirm"
    if c == "helm" and args[:1] and args[0] in {"uninstall", "delete", "rollback"}:
        return "deny", f"`helm {args[0]}` is destructive"
    if c == "aws":
        if re.search(r"\bs3\s+(rm|rb)\b", joined) or re.search(r"\s(delete|terminate|remove|deregister|purge)-[\w-]+", " " + joined):
            return "deny", "destructive AWS operation"
        if re.search(r"\bconfigure\s+(get|export-credentials)\b|\bsts\s+get-session-token\b|"
                     r"\bsecretsmanager\s+get-secret-value\b|\bssm\s+get-parameters?\b.*--with-decryption", joined):
            return "deny", "reading cloud credentials/secrets is not allowed"
    if c == "gcloud":
        if re.search(r"\bdelete\b", joined):
            return "deny", "destructive gcloud operation"
        if re.search(r"auth\s+(print-access-token|print-identity-token)|secrets\s+versions\s+access", joined):
            return "deny", "reading cloud credentials/secrets is not allowed"
    if c == "az" and (re.search(r"\bdelete\b", joined) or re.search(r"get-access-token|keyvault\s+secret\s+show", joined)):
        return "deny", "destructive or credential-reading az operation"
    if c == "gh":
        if re.search(r"\bauth\s+(token|status\s+.*(-t|--show-token))", joined):
            return "deny", "printing GitHub credentials is not allowed"
        if re.search(r"\b(repo|release|secret|variable)\s+delete\b", joined):
            return "deny", "destructive gh operation"
        if re.search(r"\b(pr\s+(create|merge)|release\s+create|repo\s+create)\b", joined):
            return "ask", "this gh command publishes to GitHub; confirm"
        if re.search(r"\bextension\s+(install|upgrade)\b", joined):
            return "ask", f"`gh extension install` downloads a program and runs it. {PKG_NOTE}"
    if c == "security" and re.search(r"\b(find-(generic|internet)-password|dump-keychain|export|find-certificate.*-p)\b", joined):
        return "deny", "reading the macOS keychain is not allowed"
    if c in {"gpg", "gpg2"} and re.search(r"--export-secret", joined):
        return "deny", "exporting private keys is not allowed"
    if c in {"vault", "op", "bw", "lpass", "pass"} and args[:1] and \
            re.search(r"^(read|get|kv|item|show|export|reveal)$", args[0]):
        return "deny", f"reading secrets via `{c}` is not allowed"
    if c in {"heroku"} and args[:1] and args[0].startswith("config"):
        return "deny", "reading app config vars is not allowed"
    if c in {"printenv", "export", "declare", "compgen", "set", "env", "typeset"}:
        if c == "printenv" or c == "env" or (c == "set" and not args) or (c == "export" and (not args or "-p" in args)) \
                or (c in {"declare", "typeset"} and ("-x" in args or "-p" in args or not args)) or c == "compgen":
            return "deny", "dumping environment variables can expose secrets"
    if c in {"echo", "printf", "print"} and SECRET_VAR_RE.search(joined):
        return "deny", "printing secret-looking environment variables is not allowed"
    if c in {"nc", "ncat", "netcat", "socat", "telnet"}:
        return "deny", f"`{c}` raw network connections are not allowed"
    if c in {"curl", "wget", "http", "https", "xh"}:
        if re.search(r"(?:^|\s)(?:-d|--data(?:-binary|-raw|-urlencode)?|-F|--form|--json)\s*=?\s*['\"]?@", " " + joined) \
                or re.search(r"(?:^|\s)(?:-F|--form)\s+['\"]?[^\s=]+=[<@]", " " + joined) \
                or re.search(r"(?:^|\s)(?:-T|--upload-file|--post-file)\b", " " + joined):
            return "deny", "uploading local files with curl/wget is not allowed"
    if c in {"scp", "sftp", "rsync", "ftp"} and any(":" in a and not a.startswith("-") for a in args):
        return "ask", f"`{c}` transfers files to/from a remote host; confirm"
    if c in {"npm", "pnpm", "yarn", "bun"}:
        sub = args[0] if args and not args[0].startswith("-") else ""
        if sub in {"publish", "unpublish", "deprecate", "owner", "token", "adduser", "login"}:
            return "deny", f"`{c} {sub}` is reserved for the user"
        positional = [a for a in args[1:] if not a.startswith("-")]
        bare_yarn = c == "yarn" and not sub and not (set(args) & {"--version", "-v", "--help", "-h"})  # `yarn` = install
        if sub in {"install", "i", "ci", "add", "update", "upgrade", "up"} or bare_yarn:
            v = install_scripts_rule(c, sub, args)
            if v:
                return v
        if sub in {"install", "i", "ci", ""} and not positional:
            return None  # installs what the lockfile already pins; normal permission rules apply
        if sub == "rebuild":
            return "ask", (f"`{c} rebuild` runs the build scripts of installed packages on this machine. "
                           "Approve only if you trust the packages named.")
        if sub in {"exec", "x"} and c in {"npm", "bun"}:
            first = next((a for a in args[1:] if not a.startswith("-")), "")
            if first and _local_bin(first):
                return None  # runs a binary already installed in node_modules
            return "ask", f"`{c} {sub}` downloads the package '{first}' and runs it. {PKG_NOTE}"
        if sub == "dlx":
            return "ask", f"`{c} dlx` downloads the package '{' '.join(positional[:1])}' and runs it. {PKG_NOTE}"
        if sub in {"install", "i", "add", "ci", "update", "upgrade", "up"}:
            return "ask", f"Claude wants to add or update the package(s) {', '.join(positional[:4])} with {c}. {PKG_NOTE}"
        if sub in {"remove", "uninstall"}:
            return "ask", f"`{c} {sub}` removes the package(s) {', '.join(positional[:4])} from the project; confirm"
    if c in {"npx", "pnpx", "bunx"}:
        first = next((a for a in args if not a.startswith("-")), "")
        if first and not (f & {"-y", "--yes", "-p", "--package"}) and _local_bin(first):
            return None  # runs a binary already installed in node_modules; normal permission rules apply
    if c in {"npx", "pnpx", "bunx", "uvx"}:
        first = next((a for a in args if not a.startswith("-")), "")
        return "ask", f"`{c}` downloads the package '{first}' and runs it. {PKG_NOTE}"
    if c == "pipx" and args[:1] and args[0] in {"run", "inject", "runpip"}:
        return "ask", f"`pipx {args[0]}` downloads the package '{' '.join(args[1:2])}' and runs it. {PKG_NOTE}"
    if c == "uv" and (args[:2] in (["tool", "run"], ["tool", "install"])
                      or (args[:1] == ["run"] and any(a == "--with" or a.startswith("--with") for a in args[1:]))):
        return "ask", f"`uv {' '.join(args[:2])}` downloads packages and runs them. {PKG_NOTE}"
    if c == "go" and args[:1] == ["run"] and any("@" in a for a in args[1:] if not a.startswith("-")):
        return "ask", f"`go run` downloads '{next(a for a in args[1:] if '@' in a)}' and runs it. {PKG_NOTE}"
    if c == "deno" and (args[:1] in (["install"], ["add"]) or (args[:1] == ["run"] and any(
            a.startswith(("http://", "https://", "npm:", "jsr:")) for a in args[1:]))):
        return "ask", f"`deno {args[0]}` downloads code and runs it. {PKG_NOTE}"
    if c == "claude" and (args[:2] in (["mcp", "add"], ["mcp", "add-json"], ["mcp", "add-from-claude-desktop"],
                                       ["plugin", "install"]) or args[:3] == ["plugin", "marketplace", "add"]):
        return "ask", ("this adds tools or instructions that Claude will load in every later session. "
                       "Approve only if you asked for it and trust where it comes from.")
    if c in {"pip", "pip3", "pipx", "poetry", "uv", "conda", "gem", "cargo", "go", "brew", "apt", "apt-get", "yum", "dnf"}:
        if c in {"twine"} or (args[:1] and args[0] in {"publish", "push", "upload"}):
            return "deny", "publishing packages is reserved for the user"
        if (c, args[:1]) in (("uv", ["sync"]), ("poetry", ["install"])) and not [a for a in args[1:] if not a.startswith("-")]:
            return None  # installs what the lockfile already pins; normal permission rules apply
        if args[:1] and args[0] in {"install", "add", "remove", "uninstall", "get", "upgrade", "update", "sync"} or \
                (c == "uv" and args[:2] == ["pip", "install"]):
            return "ask", f"`{c} {' '.join(args[:3])}` adds or changes packages. {PKG_NOTE}"
    if c in {"python", "python3"} and args[:2] == ["-m", "pip"] and args[2:3] and args[2] in {"install", "uninstall"}:
        return "ask", f"`pip {' '.join(args[2:4])}` adds or changes packages. {PKG_NOTE}"
    if c == "twine" or (c == "gem" and args[:1] == ["push"]) or (c == "cargo" and args[:1] == ["publish"]):
        return "deny", "publishing packages is reserved for the user"
    if c in DB_CLIS and DESTRUCTIVE_SQL.search(joined):
        return "deny", "destructive SQL/DB command"
    if c in {"dropdb", "dropuser"}:
        return "deny", f"`{c}` is destructive"
    if c == "mv" and args and args[-1] in {"/dev/null"}:
        return "deny", "moving files to /dev/null deletes them"
    return None


def start_cwd(event: dict, proj: str) -> str:
    """Where the shell is when the command starts. Claude Code keeps a `cd` from one Bash call to the next, so
    relative paths must be resolved from here, not from the project root. Relative to the project, or absolute
    when the shell is outside it."""
    cwd = event.get("cwd") or ""
    if not cwd:
        return ""
    try:
        rel = os.path.relpath(os.path.realpath(cwd), os.path.realpath(proj))
    except ValueError:
        return cwd
    if rel == ".":
        return ""
    return cwd if rel == ".." or rel.startswith(".." + os.sep) else rel


def locate(word: str, cwd: str, proj: str) -> str:
    """`word` as the shell resolves it from `cwd`: relative to the project when inside it, absolute otherwise."""
    p = os.path.expanduser(word)
    if not os.path.isabs(p):
        p = os.path.join(proj, cwd, p)
    p = os.path.normpath(p)
    try:
        rel = os.path.relpath(p, proj)
    except ValueError:
        return p.replace(os.sep, "/")
    return (p if rel == ".." or rel.startswith(".." + os.sep) else rel).replace(os.sep, "/")


def is_harness_tool(word: str, cwd: str, proj: str) -> bool:
    return bool(HARNESS_TOOL.match(locate(word, cwd, proj)))


def shell_path_reason(word: str, proj: str, cwd: str = "") -> Optional[str]:
    """Like sensitive_path_reason, but a wildcard is checked against the files it matches now, as the shell
    would expand it, instead of every file it could match in theory (`wc app/*` is fine when app/ holds no keys)."""
    if not any(ch in word for ch in "*?["):
        return sensitive_path_reason(word)
    base = os.path.join(proj, cwd, os.path.expanduser(word))
    for hit in sorted(glob.glob(base))[:2000]:
        r = sensitive_path_reason(hit)
        if r:
            return f"'{word}' matches {os.path.relpath(hit, proj)}: {r}"
    return None


def inline_code_rules(code: str) -> Verdict:
    """Rules for a script given inline to an interpreter (python -c, a heredoc), where no argument shows a path."""
    if HARNESS_INLINE_RE.search(code):
        return "deny", "inline code touches harness or git internals (.claude/hooks, tools, settings, .git/hooks)"
    if INLINE_DELETE_RE.search(code):
        return "ask", "inline code deletes or moves files; confirm"
    if INLINE_SHELLOUT_RE.search(code) and INLINE_SHELLOUT_DELETE_RE.search(code):
        return "ask", "inline code runs a delete command; confirm"
    return None


def path_rules(argv: List[str], redirect_targets: List[str], allow: Allowlist, proj: str, cwd: str = "") -> Verdict:
    c, args = argv[0], argv[1:]
    # Harness tools mask and check sensitive files themselves, so their arguments pass. Where their output is
    # redirected is still checked below.
    harness_tool = c in {"python", "python3"} and bool(args) and is_harness_tool(args[0], cwd, proj)
    # inline interpreter code: python -c "...open('.env')..."
    if c in INTERPRETERS | SHELLS and not harness_tool:
        for i, a in enumerate(args[:-1]):
            if a in {"-c", "-e", "-r", "-p", "--eval", "--print"}:
                code = args[i + 1]
                if SENSITIVE_INLINE_RE.search(code):
                    return "deny", "inline code references a sensitive file"
                if re.search(r"os\.environ|process\.env|ENV\[|getenv|\$ENV", code) and re.search(r"print|console\.log|puts|echo|write", code):
                    return "deny", "inline code prints environment variables"
                if c in INTERPRETERS:
                    v = inline_code_rules(code)
                    if v:
                        return v
    asks: Verdict = None
    for a in ([] if harness_tool else args) + redirect_targets:
        if a.startswith("-") and "=" in a:
            a = a.split("=", 1)[1]
        if not a or a.startswith("-"):
            continue
        located = locate(a, cwd, proj)
        r = shell_path_reason(a, proj, cwd)
        if not r and cwd and not any(ch in a for ch in "*?["):
            r = sensitive_path_reason(located)  # `cd secrets` earlier, then a plain file name
        if r and not allow.path_allowed(located) and c not in PATH_ONLY_CMDS:
            return "deny", f"`{c}` on '{a}' blocked: {r}. Use /safe-read to view it masked"
        writes = (c in WRITER_CMDS or a in redirect_targets) and c not in {"cat", "head", "tail", "less"}
        if writes and HARNESS_PROTECTED.search(located) and not (c in {"python", "python3"} and is_harness_tool(a, cwd, proj)):
            return "deny", f"modifying '{a}' is not allowed (harness files and git internals are human-owned)"
        if writes and asks is None and AUTORUN_FILES.search(located):
            asks = ("ask", f"`{c}` on '{a}' {AUTORUN_REASON}.")
    return asks


def expand_vars(word: str, env: dict) -> str:
    """Replace $NAME / ${NAME} with a value assigned earlier in the same command line (`f=.env; cat "$f"`)."""
    if "$" not in word or not env:
        return word
    return VAR_RE.sub(lambda m: env.get(m.group(1) or m.group(2), m.group(0)), word)


def download_targets(argv: List[str], writes: List[str]) -> set:
    """File names a curl/wget/fetch command saves its download under."""
    c, args = argv[0], argv[1:]
    out = {os.path.basename(w) for w in writes}
    flag = "O" if c == "wget" else "o"
    remote_name = c == "wget"  # wget saves under the URL's name unless -O says otherwise
    for i, a in enumerate(args):
        nxt = args[i + 1] if i + 1 < len(args) else ""
        val = None
        if a in {"--output", "--output-document"}:
            val = nxt
        elif a.startswith(("--output=", "--output-document=")):
            val = a.split("=", 1)[1]
        elif a in {"--remote-name", "--remote-name-all"}:
            remote_name = True
        elif a.startswith("-") and not a.startswith("--") and len(a) > 1:
            if flag in a[1:]:
                val = a[a.index(flag, 1) + 1:] or nxt
            elif c != "wget" and "O" in a[1:]:
                remote_name = True
        if val is not None:
            remote_name = remote_name and c != "wget"
            if val != "-":
                out.add(os.path.basename(val))
    if remote_name:
        out |= {os.path.basename(a.split("?", 1)[0].split("#", 1)[0].rstrip("/")) for a in args if "://" in a}
    out.discard("")
    return out


def heredoc_feeds_harness_tool(header: str, cwd: str, proj: str) -> bool:
    """True when the heredoc is data for a harness tool (`save.py node x <<'EOF'`), not a script to run."""
    try:
        commands = split_simple(tokenize(header))
    except ValueError:
        return False
    for raw in commands:
        if "<<" not in raw:
            continue
        argv = unwrap([t for t in raw[:raw.index("<<")] if t not in REDIRECTS])
        return len(argv) > 1 and argv[0] in {"python", "python3"} and is_harness_tool(argv[1], cwd, proj)
    return False


def analyze(cmd: str, allow: Allowlist, proj: str, depth: int = 0, cwd: Optional[str] = None) -> Verdict:
    if depth > 6:
        return "deny", "command nesting too deep to verify"
    if FORK_BOMB.search(cmd):
        return "deny", "fork bomb"
    if depth == 0:
        _CTX.update(allow=allow, proj=proj)
    if cwd is None:
        cwd = _CTX["cwd"]
    start = cwd
    body_cmd, heredocs = strip_heredocs(cmd)
    if FETCH_RUN_RE.search(body_cmd):
        return "deny", ("this runs code fetched from the network without anyone reading it (the same as `curl | sh`). "
                        "Download the file, show the user what it does, and let them run it")
    verdicts: List[Tuple[str, str]] = []
    for sub in extract_substitutions(body_cmd):
        v = analyze(sub, allow, proj, depth + 1, cwd)
        if v:
            verdicts.append(v)
    try:
        tokens = tokenize(body_cmd)
    except ValueError:
        return "deny", "could not parse the command safely (unbalanced quotes?); simplify it"
    simple = split_simple(tokens)
    chain: List[str] = []  # commands earlier in the current pipeline
    env: dict = {}         # NAME=value assignments seen so far on this command line
    fetched: set = set()   # files a downloader saved earlier on this command line
    for idx, raw in enumerate(simple):
        if raw == ["|"]:
            continue
        if idx == 0 or simple[idx - 1] != ["|"]:
            chain = []
        argv_with_redirs = list(raw)
        redirect_targets, argv, here_strings, writes = [], [], [], []
        skip_next = False
        for i in range(len(argv_with_redirs)):
            t = argv_with_redirs[i]
            if skip_next:
                skip_next = False
                continue
            if t in REDIRECTS or re.match(r"^\d?[<>]{1,2}&?$", t):
                if i + 1 < len(argv_with_redirs):
                    (here_strings if t == "<<<" else redirect_targets).append(argv_with_redirs[i + 1])
                    target = argv_with_redirs[i + 1]
                    # `2>&1`, `>&2`, `>&-` duplicate or close a file descriptor: no file is written.
                    fd_dup = t.endswith("&") and (target.isdigit() or target == "-")
                    if ">" in t and not fd_dup and target not in {"/dev/null", "&1", "&2"}:
                        writes.append(target)
                skip_next = True
                continue
            argv.append(t)
        # A variable must not hide a path or a command: `f=.env; cat "$f"`, `x=rm; $x file`.
        for t in argv:
            m = ASSIGN_RE.match(t)
            if not m:
                break
            env[m.group(1)] = expand_vars(m.group(2), env)
        argv = [expand_vars(t, env) for t in argv]
        redirect_targets = [expand_vars(t, env) for t in redirect_targets]
        writes = [expand_vars(t, env) for t in writes]
        argv = unwrap(argv)
        if not argv:
            continue
        c = argv[0]
        if c in {"export", "local", "readonly", "declare", "typeset"}:
            for t in argv[1:]:
                m = ASSIGN_RE.match(t)
                if m:
                    env[m.group(1)] = m.group(2)
        # pipe into a shell/interpreter (curl ... | sh)
        piped = next((p for p in chain if p in FETCHERS), None)
        if c in PIPE_SINKS and piped:
            verdicts.append(("deny", f"piping `{piped}` output into `{c}` executes untrusted code"))
        chain.append(c)
        # a file downloaded earlier on this line and run now (curl -o i.sh ... && sh i.sh)
        if c in DOWNLOADERS:
            fetched |= download_targets(argv, writes)
        elif fetched:
            script = c
            if c in PIPE_SINKS:
                script = next((a for a in argv[1:] if not a.startswith("-")), "")
            if os.path.basename(script) in fetched:
                verdicts.append(("deny", f"`{os.path.basename(script)}` was downloaded in this same command and would run "
                                         "without anyone reading it. Show the user what it does and let them run it"))
        if c in READ_ONLY_CMDS and writes:
            verdicts.append(("ask", f"`{c} ... > {writes[0]}` writes a file; confirm"))
        if c in {"cd", "pushd"} and argv[1:2] and not argv[1].startswith("-"):
            cwd = os.path.normpath(os.path.join(cwd, os.path.expanduser(argv[1])))
        if c in SHELLS | {"eval", "source", "."}:
            code = None
            if c == "eval":
                code = " ".join(argv[1:])
            elif c in SHELLS:
                code = shell_code(argv)
                if code is None and here_strings:
                    code = " ; ".join(here_strings)
            if code is not None:
                v = analyze(code, allow, proj, depth + 1, cwd)
                if v:
                    verdicts.append(v)
        v = cmd_rules(argv)
        if v:
            verdicts.append(v)
        v = path_rules(argv, redirect_targets, allow, proj, cwd)
        if v:
            verdicts.append(v)
    for header, body in heredocs:
        if heredoc_feeds_harness_tool(header, start, proj):
            continue  # a KG node, handoff note or scratch file: data that the tool scans itself, not a script
        feeds_code = re.search(r"\b(?:python3?|node|ruby|perl|php|bash|sh|zsh)\b", header)
        if feeds_code and SENSITIVE_INLINE_RE.search(body):
            verdicts.append(("deny", "heredoc script references a sensitive file"))
        if re.search(r"(?:^|[\s;&|(])(?:bash|sh|zsh|dash|ksh)\b", header):  # a shell reads the body as a script
            v = analyze(body, allow, proj, depth + 1, start)
            if v:
                verdicts.append(v)
        elif feeds_code:
            v = inline_code_rules(body)
            if v:
                verdicts.append(v)
    for kind in ("deny", "ask"):
        for v in verdicts:
            if v[0] == kind:
                return v
    return None


def main(event):
    proj = project_dir(event)
    cmd = (event.get("tool_input") or {}).get("command") or ""
    allow = Allowlist.load(proj)
    config = load_config(proj)
    cfg = config["scan"]
    _CTX.update(cwd=start_cwd(event, proj),
                install_scripts=str((config.get("supply_chain") or {}).get("install_scripts", "block")))
    findings = scan_local(cmd, allow, cfg)
    if findings:
        fix = ("Next step: reference secrets via env var names, never inline values."
               if any(f.kind == "secret" for f in findings)
               else "Next step: leave the personal data out of the command (read it from a file or variable).")
        return deny(f"Command contains {summarize(findings)}. {fix} {UNBLOCK_HINT}")
    v = analyze(cmd, allow, proj)
    if v:
        kind, reason = v
        (deny if kind == "deny" else ask)(reason)


if __name__ == "__main__":
    run_guard(main)
