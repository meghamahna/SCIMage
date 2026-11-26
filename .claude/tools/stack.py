#!/usr/bin/env python3
"""Detect a project's stack and its build/test/lint commands (stdlib only).

Usage: python3 .claude/tools/stack.py [--root DIR]   -> JSON on stdout
"""
from __future__ import annotations

import argparse
import json
import os
import re
from typing import Dict, List

CATEGORIES = ("install", "build", "test", "lint", "format", "typecheck", "run")


def _read(path: str) -> str:
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except OSError:
        return ""


def _exists(root: str, *names: str) -> bool:
    return any(os.path.exists(os.path.join(root, n)) for n in names)


def _add(cmds: Dict[str, List[str]], cat: str, cmd: str) -> None:
    if cmd not in cmds[cat]:
        cmds[cat].append(cmd)


def detect(root: str) -> Dict:
    root = os.path.abspath(root)
    langs: List[str] = []
    frameworks: List[str] = []
    manifests: List[str] = []
    cmds: Dict[str, List[str]] = {c: [] for c in CATEGORIES}

    # ---- JavaScript / TypeScript
    pkg_path = os.path.join(root, "package.json")
    if os.path.isfile(pkg_path):
        manifests.append("package.json")
        try:
            pkg = json.loads(_read(pkg_path) or "{}")
        except ValueError:
            pkg = {}
        pm = "pnpm" if _exists(root, "pnpm-lock.yaml") else "yarn" if _exists(root, "yarn.lock") else \
            "bun" if _exists(root, "bun.lockb", "bun.lock") else "npm"
        deps = {**(pkg.get("dependencies") or {}), **(pkg.get("devDependencies") or {})}
        langs.append("TypeScript" if "typescript" in deps or _exists(root, "tsconfig.json") else "JavaScript")
        for name, label in (("next", "Next.js"), ("react", "React"), ("vue", "Vue"), ("svelte", "Svelte"),
                            ("@angular/core", "Angular"), ("express", "Express"), ("@nestjs/core", "NestJS"),
                            ("vite", "Vite"), ("tailwindcss", "Tailwind"), ("jest", "Jest"), ("vitest", "Vitest"),
                            ("@playwright/test", "Playwright")):
            if name in deps:
                frameworks.append(label)
        _add(cmds, "install", f"{pm} install")
        scripts = pkg.get("scripts") or {}
        mapping = {"build": "build", "test": "test", "lint": "lint", "format": "format", "fmt": "format",
                   "typecheck": "typecheck", "type-check": "typecheck", "tsc": "typecheck", "dev": "run", "start": "run"}
        for s, cat in mapping.items():
            if s in scripts:
                _add(cmds, cat, f"{pm} test" if s == "test" and pm != "yarn" else f"{pm} run {s}" if pm != "yarn" else f"yarn {s}")

    # ---- Python
    pyproject = _read(os.path.join(root, "pyproject.toml"))
    reqs = "".join(_read(os.path.join(root, f)) for f in os.listdir(root)
                   if re.match(r"requirements.*\.txt$", f)) if os.path.isdir(root) else ""
    if pyproject or reqs or _exists(root, "setup.py", "setup.cfg", "Pipfile"):
        langs.append("Python")
        manifests += [f for f in ("pyproject.toml", "setup.py", "setup.cfg", "Pipfile") if _exists(root, f)]
        runner = "uv run " if _exists(root, "uv.lock") else "poetry run " if _exists(root, "poetry.lock") else ""
        blob = (pyproject + reqs).lower()
        for name, label in (("django", "Django"), ("fastapi", "FastAPI"), ("flask", "Flask"), ("pydantic", "Pydantic")):
            if name in blob:
                frameworks.append(label)
        _add(cmds, "install", "uv sync" if runner.startswith("uv") else "poetry install" if runner else
             "pip install -r requirements.txt" if reqs else "pip install -e .")
        if "pytest" in blob or _exists(root, "tests", "pytest.ini", "conftest.py"):
            _add(cmds, "test", f"{runner}pytest")
        if "ruff" in blob:
            _add(cmds, "lint", f"{runner}ruff check .")
            _add(cmds, "format", f"{runner}ruff format .")
        if "black" in blob:
            _add(cmds, "format", f"{runner}black .")
        if "mypy" in blob:
            _add(cmds, "typecheck", f"{runner}mypy .")
        if _exists(root, "manage.py"):
            _add(cmds, "run", f"{runner}python manage.py runserver")

    # ---- Go
    if _exists(root, "go.mod"):
        langs.append("Go")
        manifests.append("go.mod")
        for cat, c in (("build", "go build ./..."), ("test", "go test ./..."), ("lint", "go vet ./..."), ("format", "gofmt -l .")):
            _add(cmds, cat, c)
        if _exists(root, ".golangci.yml", ".golangci.yaml"):
            _add(cmds, "lint", "golangci-lint run")

    # ---- Rust
    if _exists(root, "Cargo.toml"):
        langs.append("Rust")
        manifests.append("Cargo.toml")
        for cat, c in (("build", "cargo build"), ("test", "cargo test"), ("lint", "cargo clippy"), ("format", "cargo fmt")):
            _add(cmds, cat, c)

    # ---- JVM
    if _exists(root, "pom.xml"):
        langs.append("Java")
        manifests.append("pom.xml")
        mvn = "./mvnw" if _exists(root, "mvnw") else "mvn"
        _add(cmds, "build", f"{mvn} package")
        _add(cmds, "test", f"{mvn} test")
    if _exists(root, "build.gradle", "build.gradle.kts"):
        langs.append("Kotlin/Java")
        manifests += [f for f in ("build.gradle", "build.gradle.kts") if _exists(root, f)]
        gr = "./gradlew" if _exists(root, "gradlew") else "gradle"
        _add(cmds, "build", f"{gr} build")
        _add(cmds, "test", f"{gr} test")

    # ---- Ruby
    if _exists(root, "Gemfile"):
        langs.append("Ruby")
        manifests.append("Gemfile")
        _add(cmds, "install", "bundle install")
        gem = _read(os.path.join(root, "Gemfile"))
        if "rspec" in gem:
            _add(cmds, "test", "bundle exec rspec")
        if "rubocop" in gem:
            _add(cmds, "lint", "bundle exec rubocop")
        if "rails" in gem:
            frameworks.append("Rails")

    # ---- Make
    mk = _read(os.path.join(root, "Makefile"))
    if mk:
        manifests.append("Makefile")
        targets = set(re.findall(r"^([A-Za-z0-9_\-]+):", mk, re.M))
        for t, cat in (("build", "build"), ("test", "test"), ("lint", "lint"), ("fmt", "format"),
                       ("format", "format"), ("run", "run"), ("dev", "run")):
            if t in targets:
                _add(cmds, cat, f"make {t}")

    if _exists(root, "Dockerfile", "docker-compose.yml", "compose.yaml"):
        frameworks.append("Docker")

    return {
        "name": os.path.basename(root),
        "languages": langs,
        "frameworks": sorted(set(frameworks), key=frameworks.index),
        "manifests": manifests,
        "commands": {k: v for k, v in cmds.items() if v},
    }


def summary(info: Dict) -> str:
    parts = info["languages"] + info["frameworks"]
    return ", ".join(parts) if parts else "not detected (fill in manually)"


def commands_markdown(info: Dict) -> str:
    if not info["commands"]:
        return "- _No build/test commands detected. Add them here._"
    return "\n".join(f"- **{cat}**: " + " · ".join(f"`{c}`" for c in cs) for cat, cs in info["commands"].items())


def allow_rules(info: Dict) -> List[str]:
    """Permission allow rules for detected read-only-ish commands (never install)."""
    rules: List[str] = []
    for cat in ("build", "test", "lint", "format", "typecheck"):
        for c in info["commands"].get(cat, []):
            rules += [f"Bash({c})", f"Bash({c} *)"]
    return rules


def layout_markdown(root: str, limit: int = 14) -> str:
    skip = {".git", "node_modules", ".venv", "venv", "__pycache__", ".idea", ".vscode", "dist", "build",
            ".next", "target", ".claude", ".pytest_cache", ".mypy_cache", "coverage"}
    try:
        entries = sorted(e for e in os.listdir(root) if os.path.isdir(os.path.join(root, e)) and e not in skip
                         and not e.startswith("."))
    except OSError:
        entries = []
    if not entries:
        return "- _(flat layout)_"
    lines = [f"- `{e}/`" for e in entries[:limit]]
    if len(entries) > limit:
        lines.append(f"- … {len(entries) - limit} more")
    return "\n".join(lines)


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..")))
    a = ap.parse_args()
    print(json.dumps(detect(a.root), indent=2))
