"""Secret and PII detectors shared by every harness hook and tool.

Stdlib only. Every finding carries a detector name (see docs/contracts.md);
callers must report names, never matched values.
"""
from __future__ import annotations

import bisect
import fnmatch
import hashlib
import math
import os
import posixpath
import re
from collections import Counter
from dataclasses import dataclass
from typing import Callable, Iterable, List, Optional


@dataclass(frozen=True)
class Finding:
    detector: str
    start: int
    end: int
    value: str

    @property
    def kind(self) -> str:
        return self.detector.split(".", 1)[0]

    @property
    def sha256(self) -> str:
        return hashlib.sha256(self.value.encode("utf-8", "replace")).hexdigest()


# ---------------------------------------------------------------- validators

def shannon_entropy(s: str) -> float:
    if not s:
        return 0.0
    counts = Counter(s)
    n = len(s)
    return -sum(c / n * math.log2(c / n) for c in counts.values())


def luhn_ok(digits: str) -> bool:
    total, alt = 0, False
    for ch in reversed(digits):
        d = ord(ch) - 48
        if alt:
            d *= 2
            if d > 9:
                d -= 9
        total += d
        alt = not alt
    return total % 10 == 0


def iban_ok(raw: str) -> bool:
    s = raw.replace(" ", "").upper()
    if not 15 <= len(s) <= 34:
        return False
    rearranged = s[4:] + s[:4]
    try:
        num = "".join(str(int(c, 36)) for c in rearranged)
    except ValueError:
        return False
    return int(num) % 97 == 1


# A value is a placeholder when it is template syntax, a mask, or contains a placeholder word that starts a word
# (`test_key`, `my-fake-token`, `yourPassword`, `myTestKey`). A word buried inside another one does not count:
# `Contest2024x` and `Latest_pw` are real values that happen to contain "test".
_PLACEHOLDER_WORDS = ("your", "example", "sample", "dummy", "placeholder", "changeme", "change_me", "redacted", "todo",
                      "fixme", "replace", "insert", "test", "fake", "mock", "secret_here", "password_here")
_PLACEHOLDER_RE = re.compile(
    r"^(?:\$\{?|<|%|\{\{)|(?i:xxxx)|\*\*\*|<.*>"
    r"|(?<![A-Za-z])(?i:" + "|".join(_PLACEHOLDER_WORDS) + r")"
    r"|(?<=[a-z])(?:" + "|".join(w.capitalize() for w in _PLACEHOLDER_WORDS) + r")"
)
_CODE_VALUE_RE = re.compile(r"(?i)(process\.env|os\.environ|getenv|\benv\(|config\[|settings\.|vault:|secretsmanager:)")


# Whole values that are well-known local-development defaults, not credentials.
_DEV_VALUES = {
    "postgres", "postgresql", "mysql", "mariadb", "mongo", "mongodb", "redis", "rabbitmq", "guest", "admin",
    "administrator", "root", "toor", "password", "passwd", "pass", "secret", "user", "username", "docker",
    "airflow", "minioadmin", "localstack", "dev", "devpass", "development", "local", "localhost", "default",
    "letmein", "qwerty", "123456", "12345678", "password1", "password123", "admin123", "s3cr3t",
}


def _not_placeholder(v: str) -> bool:
    if v.lower() in _DEV_VALUES or v.lower().startswith("django-insecure-"):
        return False
    return not _PLACEHOLDER_RE.search(v) and not _CODE_VALUE_RE.search(v)


# ---------------------------------------------------------------- patterns

@dataclass(frozen=True)
class Rule:
    name: str
    regex: re.Pattern
    group: int = 0
    validate: Optional[Callable[[str], bool]] = None


def _r(p: str, flags: int = 0) -> re.Pattern:
    return re.compile(p, flags)


_TEST_CARDS = {
    "4242424242424242", "4111111111111111", "4000056655665556", "5555555555554444",
    "5200828282828210", "378282246310005", "371449635398431", "6011111111111117",
    "3056930009020004", "36227206271667", "3566002020360505", "4012888888881881",
    "5105105105105100", "4000000000000002", "4000000000009995",
}
_EXAMPLE_SSNS = {"123-45-6789", "078-05-1120", "219-09-9999"}
_EMAIL_IGNORE_DOMAINS = re.compile(
    r"(?i)(?:^|\.)(?:example\.(?:com|org|net)|example|test|invalid|localhost|local|"
    r"users\.noreply\.github\.com|anthropic\.com)$"
)
_EMAIL_IGNORE_LOCAL = re.compile(r"(?i)^(?:no-?reply|donotreply|do-not-reply|git|nobody|user|foo|bar|test|email|name|someone|you|me)$")
_FILE_EXT_TLDS = {"png", "jpg", "jpeg", "gif", "svg", "webp", "js", "ts", "tsx", "jsx", "css", "scss",
                  "map", "json", "md", "py", "rb", "go", "rs", "java", "kt", "html", "txt", "lock", "yml", "yaml"}


def _email_ok(v: str) -> bool:
    local, _, domain = v.rpartition("@")
    tld = domain.rsplit(".", 1)[-1].lower()
    if tld in _FILE_EXT_TLDS:
        return False
    if _EMAIL_IGNORE_DOMAINS.search(domain) or _EMAIL_IGNORE_LOCAL.match(local):
        return False
    return True


def _card_ok(v: str) -> bool:
    d = re.sub(r"\D", "", v)
    if not 13 <= len(d) <= 19 or d in _TEST_CARDS or len(set(d)) == 1:
        return False
    if not re.match(r"^(?:4|5[1-5]|2[2-7]|3[47]|3[068]|35|6011|65)", d):
        return False
    return luhn_ok(d)


def _sin_ok(v: str) -> bool:
    d = re.sub(r"\D", "", v)
    return len(d) == 9 and d[0] not in "08" and len(set(d)) > 1 and luhn_ok(d)


def _phone_ok(v: str) -> bool:
    d = re.sub(r"\D", "", v)
    if len(set(d)) <= 2:
        return False
    # NANP fictional 555 exchange / documented example ranges
    if re.search(r"(?:^|\D)555[\s.\-]?01\d\d", v) or re.search(r"\b555[\s.\-]\d{4}\b", v):
        return False
    return 10 <= len(d) <= 15


def _generic_ok_password(v: str) -> bool:
    return len(v) >= 6 and _not_placeholder(v) and shannon_entropy(v) >= 2.5


def _generic_ok_token(v: str) -> bool:
    if re.match(r"(?i)^(?:https?|wss?)://[^@]*$", v) or v.startswith(("/", "./", "../")):
        return False  # an endpoint or a file path, not a credential
    return len(v) >= 16 and _not_placeholder(v) and shannon_entropy(v) >= 3.5


def _connstr_ok(v: str) -> bool:
    return _not_placeholder(v) and v.lower() not in {"pwd"}


SECRET_RULES: List[Rule] = [
    # The whole block, body included (also when newlines are JSON-escaped as in service-account files).
    Rule("secret.private_key", _r(
        r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----"
        r"(?:(?:\\[nr]|\s)+(?:[A-Za-z\-]+: [^\n\\]*|[A-Za-z0-9+/=]{16,}))*"
        r"(?:(?:\\[nr]|\s)+[A-Za-z0-9+/=]{1,15}(?=(?:\\[nr]|\s)+-----END))?"
        r"(?:(?:\\[nr]|\s)+-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)?")),
    Rule("secret.aws_access_key", _r(r"\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b"),
         validate=lambda v: not v.endswith("EXAMPLE")),
    Rule("secret.aws_secret_key",
         _r(r"(?i)aws.{0,20}(?:secret|private).{0,20}?[:=]\s*[\"']?([A-Za-z0-9/+=]{40})(?![A-Za-z0-9/+=])"), 1),
    Rule("secret.github_token", _r(r"\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})\b")),
    Rule("secret.gitlab_token", _r(r"\bglpat-[A-Za-z0-9_\-]{20,}\b")),
    Rule("secret.slack_token", _r(r"\bxox[abposre]-[A-Za-z0-9-]{10,}\b")),
    Rule("secret.slack_webhook", _r(r"https://hooks\.slack\.com/services/T[A-Za-z0-9]+/B[A-Za-z0-9]+/[A-Za-z0-9]+")),
    Rule("secret.stripe_key", _r(r"\b(?:sk|rk)_live_[A-Za-z0-9]{20,}\b")),
    Rule("secret.google_api_key", _r(r"\bAIza[0-9A-Za-z_\-]{35}\b")),
    Rule("secret.anthropic_key", _r(r"\bsk-ant-[A-Za-z0-9_\-]{20,}")),
    Rule("secret.openai_key", _r(r"\bsk-(?!ant-)(?:proj-|svcacct-|admin-)?[A-Za-z0-9_\-]{20,}"),
         validate=lambda v: shannon_entropy(v) >= 3.5 and any(c.isdigit() for c in v)),
    Rule("secret.npm_token", _r(r"\bnpm_[A-Za-z0-9]{36}\b")),
    Rule("secret.sendgrid_key", _r(r"\bSG\.[A-Za-z0-9_\-]{22}\.[A-Za-z0-9_\-]{43}\b")),
    Rule("secret.jwt", _r(r"\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}")),
    Rule("secret.connection_string",
         _r(r"\b[a-zA-Z][a-zA-Z0-9+.\-]{1,20}://[^\s:/@'\"]+:([^\s@/'\"]{3,})@[^\s'\"]+"), 1, _connstr_ok),
    # quoted assignment in code: password = "..." / "apiKey": "..."
    Rule("secret.generic_assignment",
         _r(r"(?i)[\"']?(?<![A-Za-z0-9_.\-/\\])[A-Za-z0-9_.\-]*(?:passw(?:or)?d|pwd|passphrase)[A-Za-z0-9_.\-]*[\"']?\s*(?::=|=>|[:=])\s*[\"']([^\"'\s]{6,})[\"']"),
         1, _generic_ok_password),
    Rule("secret.generic_assignment",
         _r(r"(?i)[\"']?(?<![A-Za-z0-9_.\-/\\])[A-Za-z0-9_.\-]*(?:secret|token|api[_\-]?key|apikey|access[_\-]?key|private[_\-]?key|client[_\-]?secret|auth[_\-]?key|credentials?)"
            r"(?!izer|[_.\-]?(?:endpoint|url|uri|name|path|file|type|header|ttl|expiry|expires|limit|count|length|prefix|id)s?\b)"
            r"[A-Za-z0-9_.\-]*[\"']?\s*(?::=|=>|[:=])\s*[\"']([^\"'\s]{16,})[\"']"),
         1, _generic_ok_token),
    # dotenv / shell style: API_KEY=abcd...
    Rule("secret.generic_assignment",
         _r(r"(?m)^\s*(?:export\s+)?[A-Z0-9_]*(?:PASSWORD|PASSWD|PWD|PASSPHRASE)[A-Z0-9_]*\s*=\s*([^\s\"'#]{6,})"),
         1, _generic_ok_password),
    Rule("secret.generic_assignment",
         _r(r"(?m)^\s*(?:export\s+)?[A-Z0-9_]*(?:SECRET|TOKEN|API_?KEY|ACCESS_KEY|PRIVATE_KEY|CLIENT_SECRET|CREDENTIALS?)[A-Z0-9_]*\s*=\s*([^\s\"'#]{16,})"),
         1, _generic_ok_token),
]

PII_RULES: List[Rule] = [
    Rule("pii.email", _r(r"(?<![\w.+\-/@])[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}\b"),
         validate=_email_ok),
    Rule("pii.ssn", _r(r"\b(?!000|666|9\d\d)\d{3}-(?!00)\d{2}-(?!0000)\d{4}\b"),
         validate=lambda v: v not in _EXAMPLE_SSNS),
    Rule("pii.sin", _r(r"\b\d{3}[ \-]\d{3}[ \-]\d{3}\b"), validate=_sin_ok),
    Rule("pii.credit_card", _r(r"\b(?:\d[ \-]?){12,18}\d\b"), validate=_card_ok),
    Rule("pii.iban", _r(r"\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}\b"), validate=iban_ok),
    Rule("pii.phone",
         _r(r"(?<![\w.])(?:\+\d{1,3}[\s.\-]?\(?\d{1,4}\)?(?:[\s.\-]?\d{2,4}){2,4}|\(?\b[2-9]\d{2}\)?[\s.\-]\d{3}[\s.\-]\d{4})\b"),
         validate=_phone_ok),
]

ALL_DETECTORS = sorted({r.name for r in SECRET_RULES + PII_RULES})


# ---------------------------------------------------------------- allowlist

class Allowlist:
    """Human-maintained exceptions (`.claude/security/allowlist`)."""

    def __init__(self, path_globs=(), hashes=(), disabled=()):
        self.path_globs = list(path_globs)
        self.hashes = set(hashes)
        self.disabled = set(disabled)

    @classmethod
    def load(cls, project_dir: Optional[str]) -> "Allowlist":
        globs, hashes, disabled = [], set(), set()
        if project_dir:
            p = os.path.join(project_dir, ".claude", "security", "allowlist")
            try:
                with open(p, encoding="utf-8") as fh:
                    for raw in fh:
                        line = raw.split("#", 1)[0].strip()
                        if not line:
                            continue
                        if line.startswith("sha256:"):
                            hashes.add(line[7:].strip().lower())
                        elif line.startswith("detector:"):
                            disabled.add(line[9:].strip())
                        elif line.startswith("path:"):
                            globs.append(line[5:].strip())
                        else:
                            globs.append(line)
            except OSError:
                pass
        return cls(globs, hashes, disabled)

    def path_allowed(self, rel_path: Optional[str]) -> bool:
        if not rel_path:
            return False
        rel = posixpath.normpath(rel_path.replace(os.sep, "/"))  # `a/../.env` must not ride on an `a/**` entry
        if rel.startswith(".."):
            return False
        return any(fnmatch.fnmatch(rel, g) or fnmatch.fnmatch(rel, g.rstrip("/") + "/*") for g in self.path_globs)


# ---------------------------------------------------------------- scanning

def scan_text(text: str, allow: Optional[Allowlist] = None, include_pii: bool = True,
              pii_detectors: Optional[Iterable[str]] = None) -> List[Finding]:
    """Return non-overlapping findings, longest match wins."""
    if not text:
        return []
    allow = allow or Allowlist()
    enabled_pii = None if pii_detectors is None else {f"pii.{d}" if not d.startswith("pii.") else d for d in pii_detectors}
    rules = list(SECRET_RULES)
    if include_pii:
        rules += [r for r in PII_RULES if enabled_pii is None or r.name in enabled_pii]
    raw: List[Finding] = []
    for rule in rules:
        if rule.name in allow.disabled:
            continue
        for m in rule.regex.finditer(text):
            v = m.group(rule.group)
            if not v:
                continue
            if rule.validate and not rule.validate(v):
                continue
            f = Finding(rule.name, m.start(rule.group), m.end(rule.group), v)
            if f.sha256 in allow.hashes:
                continue
            raw.append(f)
    # Secrets win over overlapping PII guesses; within a kind, earliest-then-longest wins.
    # One sorted pass per kind, so a file with many findings stays fast.
    order = lambda f: (f.start, -(f.end - f.start))  # noqa: E731
    secrets: List[Finding] = []
    for f in sorted((f for f in raw if f.kind == "secret"), key=order):
        if not secrets or f.start >= secrets[-1].end:
            secrets.append(f)
    starts = [s.start for s in secrets]
    pii: List[Finding] = []
    for f in sorted((f for f in raw if f.kind != "secret"), key=order):
        if pii and f.start < pii[-1].end:
            continue
        i = bisect.bisect_right(starts, f.start) - 1
        if (i >= 0 and secrets[i].end > f.start) or (i + 1 < len(secrets) and secrets[i + 1].start < f.end):
            continue
        pii.append(f)
    return sorted(secrets + pii, key=lambda f: f.start)


def redact(text: str, findings: List[Finding]) -> str:
    if not findings:
        return text
    parts, pos = [], 0
    for f in sorted(findings, key=lambda f: f.start):
        parts.append(text[pos:f.start])
        parts.append(f"***REDACTED:{f.detector}***")
        pos = f.end
    parts.append(text[pos:])
    return "".join(parts)


def summarize(findings: List[Finding], limit: int = 6) -> str:
    """Detector names with counts and line numbers are fine; values never."""
    c = Counter(f.detector for f in findings)
    return ", ".join(f"{d} x{n}" for d, n in c.most_common(limit))


def line_of(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


# ---------------------------------------------------------------- sensitive paths

SENSITIVE_NAME_GLOBS = [
    ".env", ".env.*", "*.env", ".envrc",
    "*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.p8", "*.ppk", "*.asc",
    "id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "*.kdbx",
    ".npmrc", ".pypirc", ".netrc", "_netrc", ".git-credentials", ".pgpass", ".htpasswd", ".my.cnf",
    "credentials*.json", "credentials", "service-account*.json", "*serviceaccount*.json",
    "*.tfstate", "*.tfstate.*", "secrets.y*ml", "secrets.json", "secret.y*ml",
    ".bash_history", ".zsh_history", ".python_history", ".psql_history", ".mysql_history",
]
SENSITIVE_NAME_EXCEPTIONS = [
    "*.example", "*.sample", "*.template", "*.dist", "*.tmpl", ".env.*example*", "*.pub", "*.md",
]
SENSITIVE_DIRS = {".ssh", ".aws", ".gnupg", "secrets", ".secrets", ".password-store"}
SENSITIVE_SUFFIXES = [
    ".docker/config.json", ".kube/config", ".config/gcloud", ".azure", ".config/gh/hosts.yml",
    "Library/Keychains",
]
# Representative names used to test whether a shell glob could expand onto a sensitive file.
_GLOB_PROBES = [".env", ".env.local", ".env.production", "id_rsa", "id_ed25519", "server.pem", "tls.key",
                "credentials.json", ".npmrc", ".netrc", "terraform.tfstate", ".git-credentials"]


def sensitive_path_reason(path: str) -> Optional[str]:
    """Return a short reason if `path` points at a credential/secret store."""
    if not path:
        return None
    p = os.path.expanduser(str(path)).replace("\\", "/")
    parts = [x for x in p.lower().split("/") if x not in ("", ".")]  # macOS and Windows ignore case
    if not parts:
        return None
    name = parts[-1]
    norm = "/".join(parts)
    for suf in SENSITIVE_SUFFIXES:
        if ("/" + norm).endswith("/" + suf.lower()) or ("/" + suf.lower() + "/") in ("/" + norm + "/"):
            return f"credential store ({suf})"
    for d in parts[:-1]:
        if d in SENSITIVE_DIRS:
            return f"inside sensitive directory '{d}'"
    if name in SENSITIVE_DIRS:
        return f"sensitive directory '{name}'"
    if any(fnmatch.fnmatch(name, e) for e in SENSITIVE_NAME_EXCEPTIONS):
        return None
    for g in SENSITIVE_NAME_GLOBS:
        if fnmatch.fnmatch(name, g):
            return f"sensitive file pattern '{g}'"
    if any(ch in name for ch in "*?["):
        for probe in _GLOB_PROBES:
            if fnmatch.fnmatch(probe, name):
                return f"glob '{name}' can match sensitive file '{probe}'"
    return None


SENSITIVE_INLINE_RE = re.compile(
    r"(?:(?<![\w.])\.env(?!\.(?:example|sample|template))(?:\.[\w\-]+)*\b|id_(?:rsa|dsa|ecdsa|ed25519)\b|\.pem\b|"
    r"\.aws/credentials|\.ssh/|\.netrc\b|\.git-credentials|\.npmrc\b|\.pypirc\b|\.kube/config|"
    r"\.docker/config\.json|tfstate|\.gnupg)"
)
