#!/usr/bin/env python3
"""Attach `.cursor/rules/*.mdc` to ZCode sessions deterministically.

ZCode has no glob-based rule attachment: its instruction file (`AGENTS.md`) is
resolved once per session from the working directory up to the project root, so
nested instruction files never load, and there is no equivalent of the `globs`
field in `.cursor/rules/*.mdc`. Asking the model to "read the relevant rule
file" is advisory and gets skipped.

This hook restores Cursor's behaviour by reading the frontmatter of the rule
files directly:

  SessionStart -> inject every rule with `alwaysApply: true`
  PreToolUse   -> inject rules whose `globs` match the file paths a tool is
                  about to touch, at most once per rule per session

`.cursor/rules/` stays the single source of truth; nothing is duplicated here.
The hook fails open: any unexpected input exits 0 with no output, so a broken
rule file can never block an edit.

Wired up in `.zcode/config.json` under `hooks.events.{SessionStart,PreToolUse}`.
Unlike the Codex sibling (`.codex/hooks/attach_rules.py`) the edited file paths
arrive as JSON fields of the tool payload (`tool_input.file_path`, `.path`, ...),
not as an embedded `*** Update File:` patch, so this variant extracts them from
those fields instead of parsing a patch blob.
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
from pathlib import Path

# The repository root is two levels up from this script:
# .zcode/hooks/attach_rules.py -> repo root.
SCRIPT_ROOT = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_ROOT.parents[1]
RULES_DIR = Path(os.environ.get("ZCODE_RULES_DIR") or (REPO_ROOT / ".cursor" / "rules"))
STATE_DIR = Path(os.environ.get("ZCODE_RULES_STATE_DIR") or (Path(tempfile.gettempdir()) / "zcode-coddy-rules"))

# Field names inside a ZCode tool payload that carry a file path. The matcher is
# intentionally permissive: it also walks nested structures, so a path nested
# deeper than these keys is still found. The known names keep the walk focused
# and avoid treating arbitrary string values as paths.
PATH_FIELDS = frozenset(
    {
        "file_path",
        "path",
        "notebook_path",
        "source",
        "destination",
        "new_path",
        "old_path",
        "old_string_path",
        "filePath",
        "fileName",
        "directory",
    }
)


class Rule:
    def __init__(self, path: Path, description: str, globs: list[str], always: bool, body: str):
        self.path = path
        self.description = description
        self.globs = globs
        self.always = always
        self.body = body

    @property
    def rel(self) -> str:
        return self.path.relative_to(REPO_ROOT).as_posix()


def glob_to_regex(pattern: str) -> re.Pattern[str]:
    """Translate a Cursor glob into a regex.

    `fnmatch` is unusable here because its `*` also crosses `/`, which makes
    `external/httpserver/**/*.go` miss `external/httpserver/server.go`.
    """
    out: list[str] = []
    i, n = 0, len(pattern)
    while i < n:
        if pattern.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif pattern.startswith("**", i):
            out.append(".*")
            i += 2
        elif pattern[i] == "*":
            out.append("[^/]*")
            i += 1
        elif pattern[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(pattern[i]))
            i += 1
    return re.compile("^" + "".join(out) + "$")


def parse_rule(path: Path) -> Rule | None:
    """Read one `.mdc` file. Frontmatter is flat, so no YAML dependency."""
    text = path.read_text(encoding="utf-8")
    if not text.startswith("---"):
        return None
    end = text.find("\n---", 3)
    if end < 0:
        return None
    head = text[3:end]
    body = text[end + 4 :].strip()

    description, globs, always = "", [], False
    for line in head.splitlines():
        key, sep, value = line.partition(":")
        if not sep:
            continue
        key, value = key.strip(), value.strip()
        if key == "description":
            description = value
        elif key == "globs":
            globs = [g.strip() for g in value.split(",") if g.strip()]
        elif key == "alwaysApply":
            always = value.lower() == "true"
    return Rule(path, description, globs, always, body)


def load_rules() -> list[Rule]:
    rules = []
    for path in sorted(RULES_DIR.glob("*.mdc")):
        rule = parse_rule(path)
        if rule and rule.body:
            rules.append(rule)
    return rules


def state_file(session_id: str) -> Path:
    safe = re.sub(r"[^A-Za-z0-9._-]", "_", session_id or "unknown")[:120]
    return STATE_DIR / f"{safe}.json"


def load_sent(session_id: str) -> set[str]:
    try:
        return set(json.loads(state_file(session_id).read_text(encoding="utf-8")))
    except Exception:
        return set()


def save_sent(session_id: str, sent: set[str]) -> None:
    try:
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        state_file(session_id).write_text(json.dumps(sorted(sent)), encoding="utf-8")
    except Exception:
        pass


def collect_target_paths(tool_input: object) -> list[str]:
    """Collect repo-relative file paths from a ZCode tool payload.

    ZCode hands the hook a JSON object whose fields depend on the tool
    (`file_path` for Edit/Write, `path`/`notebook_path`/... for others). We walk
    the structure and keep the string values found under known path-carrying
    keys, so an absolute or nested path is still matched once normalised.
    """
    found: list[str] = []

    def walk(node: object) -> None:
        if isinstance(node, dict):
            for key, value in node.items():
                if key in PATH_FIELDS and isinstance(value, str) and value:
                    found.append(value)
                else:
                    walk(value)
        elif isinstance(node, list):
            for item in node:
                walk(item)

    walk(tool_input)

    rel: list[str] = []
    for raw in found:
        path = Path(raw)
        if path.is_absolute():
            try:
                path = path.relative_to(REPO_ROOT)
            except ValueError:
                continue
        rel.append(path.as_posix())
    return rel


def render(rules: list[Rule], preamble: str) -> str:
    chunks = [preamble]
    for rule in rules:
        chunks.append(f"<!-- from {rule.rel} -->\n\n{rule.body}")
    return "\n\n".join(chunks)


def emit(event: str, context: str) -> None:
    json.dump(
        {"hookSpecificOutput": {"hookEventName": event, "additionalContext": context}},
        sys.stdout,
    )


def main() -> int:
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0

    event = payload.get("hook_event_name") or os.environ.get("ZCODE_HOOK_EVENT", "")
    session_id = payload.get("session_id", "")

    try:
        rules = load_rules()
    except Exception:
        return 0
    if not rules:
        return 0

    if event == "SessionStart":
        if payload.get("source") == "clear":
            state_file(session_id).unlink(missing_ok=True)
        always = [r for r in rules if r.always]
        if not always:
            return 0
        save_sent(session_id, {r.rel for r in always})
        emit(
            event,
            render(
                always,
                "Project rules for this repository, always in force. They are"
                " authoritative; `.cursor/rules/` is their single source of truth."
                " More rules are attached automatically when you edit files they"
                " cover.",
            ),
        )
        return 0

    if event != "PreToolUse":
        return 0

    paths = collect_target_paths(payload.get("tool_input"))
    if not paths:
        return 0

    sent = load_sent(session_id)
    matched: list[Rule] = []
    for rule in rules:
        if rule.always or rule.rel in sent or not rule.globs:
            continue
        patterns = [glob_to_regex(g) for g in rule.globs]
        if any(p.match(path) for path in paths for p in patterns):
            matched.append(rule)

    if not matched:
        return 0

    save_sent(session_id, sent | {r.rel for r in matched})
    touched = ", ".join(sorted(set(paths))[:8])
    emit(
        event,
        render(
            matched,
            f"Project rules that cover the files you are editing ({touched})."
            " Apply them to this change before continuing.",
        ),
    )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        # Fail open: never block an edit because of this hook.
        sys.exit(0)
