#!/usr/bin/env python3
"""Attach `.cursor/rules/*.mdc` to Codex sessions deterministically.

Codex has no glob-based rule attachment. Its `AGENTS.md` chain is resolved once
per session, walking from the repository root down to the launch directory, so
nested instruction files never load when Codex starts at the root. Telling the
model to "read the relevant rule file" is advisory and gets skipped.

This hook restores Cursor's behaviour by reading the frontmatter of the rule
files directly:

  SessionStart -> inject every rule with `alwaysApply: true`
  PreToolUse   -> inject rules whose `globs` match the paths an `apply_patch`
                  call is about to touch, at most once per rule per session

The hook reads `.cursor/rules/` directly and creates no Codex copy of a rule body.
It fails open: any unexpected input exits 0 with no output, so a broken rule file
can never block an edit.

Wired up in `.codex/hooks.json`. Requires `/hooks` approval in Codex once, and
again after every change to this file (Codex tracks hooks by content hash).
"""

from __future__ import annotations

from contextlib import contextmanager
import hashlib
import json
import os
import re
import sys
import tempfile
from pathlib import Path

if os.name == "nt":
    import msvcrt
else:
    import fcntl

REPO_ROOT = Path(__file__).resolve().parents[2]
RULES_DIR = REPO_ROOT / ".cursor" / "rules"
# State is keyed by repository path so two clones never share dedup state.
STATE_DIR = Path(tempfile.gettempdir()) / (
    "codex-attach-rules-" + hashlib.sha1(str(REPO_ROOT).encode("utf-8")).hexdigest()[:12]
)

# `*** Add File: path`, `*** Update File: path`, `*** Delete File: path`
PATCH_FILE_RE = re.compile(r"^\*\*\*\s+(?:Add|Update|Delete)\s+File:\s*(.+?)\s*$", re.M)
# `*** Move to: path` carries the destination of a rename
PATCH_MOVE_RE = re.compile(r"^\*\*\*\s+Move to:\s*(.+?)\s*$", re.M)
PATH_FIELDS = frozenset(
    {
        "destination",
        "destination_path",
        "file",
        "file_path",
        "fileName",
        "filePath",
        "new_path",
        "notebook_path",
        "old_path",
        "path",
        "paths",
        "source",
        "target",
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
    `src/api/**/*.py` miss `src/api/server.py`.
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


def strip_yaml_comment(value: str) -> str:
    quote = ""
    escaped = False
    for index, char in enumerate(value):
        if quote:
            if char == "\\" and quote == '"' and not escaped:
                escaped = True
                continue
            if char == quote and not escaped:
                quote = ""
            escaped = False
            continue
        if char in ("'", '"'):
            quote = char
            continue
        if char == "#" and (index == 0 or value[index - 1].isspace()):
            return value[:index].strip()
    return value.strip()


YAML_DOUBLE_ESCAPES = {
    "0": "\0",
    "a": "\x07",
    "b": "\x08",
    "t": "\t",
    "n": "\n",
    "v": "\x0b",
    "f": "\x0c",
    "r": "\r",
    "e": "\x1b",
    " ": " ",
    '"': '"',
    "/": "/",
    "\\": "\\",
    "N": "\x85",
    "_": "\xa0",
    "L": "\u2028",
    "P": "\u2029",
}


def decode_yaml_double_quoted(value: str) -> str:
    out: list[str] = []
    index = 0
    while index < len(value):
        char = value[index]
        if char != "\\":
            out.append(char)
            index += 1
            continue
        if index + 1 >= len(value):
            out.append("\\")
            break
        escape = value[index + 1]
        if escape in YAML_DOUBLE_ESCAPES:
            out.append(YAML_DOUBLE_ESCAPES[escape])
            index += 2
            continue
        width = {"x": 2, "u": 4, "U": 8}.get(escape)
        if width is not None:
            digits = value[index + 2 : index + 2 + width]
            if len(digits) == width:
                try:
                    out.append(chr(int(digits, 16)))
                    index += 2 + width
                    continue
                except (ValueError, OverflowError):
                    pass
        out.extend(("\\", escape))
        index += 2
    return "".join(out)


def unquote(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == '"' and value[-1] == '"':
        return decode_yaml_double_quoted(value[1:-1])
    if len(value) >= 2 and value[0] == "'" and value[-1] == "'":
        return value[1:-1].replace("''", "'")
    return value


def split_globs(value: str) -> list[str]:
    value = strip_yaml_comment(value)
    value = value.removeprefix("[").removesuffix("]")
    out: list[str] = []
    current: list[str] = []
    quote = ""
    escaped = False
    brace_depth = 0

    def flush() -> None:
        item = unquote("".join(current))
        if item:
            out.append(item)
        current.clear()

    for char in value:
        if quote:
            current.append(char)
            if char == "\\" and quote == '"' and not escaped:
                escaped = True
                continue
            if char == quote and not escaped:
                quote = ""
            escaped = False
            continue
        if char in ("'", '"'):
            quote = char
            current.append(char)
        elif char == "{":
            brace_depth += 1
            current.append(char)
        elif char == "}":
            brace_depth = max(0, brace_depth - 1)
            current.append(char)
        elif char == "," and brace_depth == 0:
            flush()
        else:
            current.append(char)
    flush()
    return out


def flow_sequence_complete(value: str) -> bool:
    quote = ""
    escaped = False
    depth = 0
    for char in value:
        if quote:
            if char == "\\" and quote == '"' and not escaped:
                escaped = True
                continue
            if char == quote and not escaped:
                quote = ""
            escaped = False
            continue
        if char in ("'", '"'):
            quote = char
        elif char == "[":
            depth += 1
        elif char == "]":
            depth = max(0, depth - 1)
    return depth == 0


def flow_quote_state(value: str) -> str:
    quote = ""
    escaped = False
    for char in value:
        if quote:
            if char == "\\" and quote == '"' and not escaped:
                escaped = True
                continue
            if char == quote and not escaped:
                quote = ""
            escaped = False
            continue
        if char in ("'", '"'):
            quote = char
    return quote


def flow_line_continues(value: str) -> bool:
    quote = ""
    escaped = False
    for char in value:
        if quote:
            if char == "\\" and quote == '"' and not escaped:
                escaped = True
                continue
            if char == quote and not escaped:
                quote = ""
            escaped = False
            continue
        if char in ("'", '"'):
            quote = char
    return quote == '"' and value.endswith("\\")


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
    lines = head.splitlines()
    index = 0
    while index < len(lines):
        line = lines[index]
        key, sep, value = line.partition(":")
        if not sep:
            index += 1
            continue
        key, value = key.strip(), value.strip()
        if key == "description":
            description = unquote(strip_yaml_comment(value))
        elif key == "globs":
            cleaned_value = strip_yaml_comment(value)
            if cleaned_value.startswith("[") and not flow_sequence_complete(cleaned_value):
                while index + 1 < len(lines):
                    index += 1
                    raw_continuation = lines[index].strip()
                    continuation = (
                        raw_continuation
                        if flow_quote_state(cleaned_value)
                        else strip_yaml_comment(raw_continuation)
                    )
                    if continuation:
                        if flow_line_continues(cleaned_value):
                            cleaned_value = cleaned_value[:-1] + continuation.lstrip()
                        else:
                            cleaned_value += " " + continuation
                    if flow_sequence_complete(cleaned_value):
                        break
            if cleaned_value:
                globs = split_globs(cleaned_value)
            else:
                index += 1
                while index < len(lines):
                    item = lines[index].strip()
                    if not item or item.startswith("#"):
                        index += 1
                        continue
                    if not item.startswith("-"):
                        index -= 1
                        break
                    pattern = unquote(strip_yaml_comment(item[1:].strip()))
                    if pattern:
                        globs.append(pattern)
                    index += 1
        elif key == "alwaysApply":
            always = unquote(strip_yaml_comment(value)).lower() == "true"
        index += 1
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


def save_sent(session_id: str, sent: set[str]) -> bool:
    try:
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        destination = state_file(session_id)
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=STATE_DIR,
            prefix=destination.name + ".",
            suffix=".tmp",
            delete=False,
        ) as handle:
            json.dump(sorted(sent), handle)
            temporary = Path(handle.name)
        os.replace(temporary, destination)
        return True
    except Exception:
        return False


def clear_sent(session_id: str) -> None:
    if save_sent(session_id, set()):
        return
    try:
        state_file(session_id).unlink(missing_ok=True)
    except Exception:
        pass


@contextmanager
def session_lock(session_id: str):
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    lock_path = state_file(session_id).with_suffix(".lock")
    with lock_path.open("a+b") as handle:
        if os.name == "nt":
            handle.seek(0, os.SEEK_END)
            if handle.tell() == 0:
                handle.write(b"\0")
                handle.flush()
            handle.seek(0)
            msvcrt.locking(handle.fileno(), msvcrt.LK_LOCK, 1)
        else:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            handle.seek(0)
            if os.name == "nt":
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def deliver_rule_context(session_id: str, candidates: list[Rule], deliver) -> bool:
    lock = session_lock(session_id)
    try:
        lock.__enter__()
    except Exception:
        if not candidates:
            return False
        deliver(candidates)
        return True

    try:
        sent = load_sent(session_id)
        selected = [rule for rule in candidates if rule.rel not in sent]
        if not selected:
            return False
        deliver(selected)
        save_sent(session_id, sent | {rule.rel for rule in selected})
        return True
    finally:
        lock.__exit__(*sys.exc_info())


def deliver_session_start(session_id: str, source: str, always: list[Rule], deliver) -> bool:
    lock = session_lock(session_id)
    try:
        lock.__enter__()
    except Exception:
        if source != "resume":
            clear_sent(session_id)
        if not always:
            return False
        deliver(always)
        return True

    try:
        sent = load_sent(session_id) if source == "resume" else set()
        if source != "resume":
            clear_sent(session_id)
        if always:
            deliver(always)
        save_sent(session_id, sent | {rule.rel for rule in always})
        return bool(always)
    finally:
        lock.__exit__(*sys.exc_info())


def patched_paths(tool_input: object) -> list[str]:
    """Collect repo-relative paths from patch text and structured tool fields."""
    blobs: list[str] = []
    structured: list[str] = []

    def walk(node: object, key: str = "") -> None:
        if isinstance(node, str):
            blobs.append(node)
            if key in PATH_FIELDS:
                structured.append(node)
        elif isinstance(node, list):
            for item in node:
                walk(item, key)
        elif isinstance(node, dict):
            for child_key, item in node.items():
                walk(item, child_key)

    def repo_relative(raw: str) -> str | None:
        raw = raw.strip()
        if not raw or "\x00" in raw:
            return None
        root = REPO_ROOT.resolve()
        path = Path(raw)
        candidate = path if path.is_absolute() else root / path
        try:
            relative = candidate.resolve(strict=False).relative_to(root)
        except (OSError, RuntimeError, ValueError):
            return None
        if not relative.parts:
            return None
        return relative.as_posix()

    walk(tool_input)

    candidates = list(structured)
    for blob in blobs:
        candidates.extend(PATCH_FILE_RE.findall(blob))
        candidates.extend(PATCH_MOVE_RE.findall(blob))

    found: list[str] = []
    for raw in candidates:
        relative = repo_relative(raw.strip())
        if relative and relative not in found:
            found.append(relative)
    return found


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

    event = payload.get("hook_event_name") or os.environ.get("CODEX_HOOK_EVENT", "")
    session_id = payload.get("session_id", "")

    try:
        rules = load_rules()
    except Exception:
        if event == "SessionStart" and payload.get("source", "") != "resume":
            clear_sent(session_id)
        return 0

    if event == "SessionStart":
        source = payload.get("source", "")
        always = [r for r in rules if r.always]

        def deliver(selected: list[Rule]) -> None:
            emit(
                event,
                render(
                    selected,
                    "Project rules for this repository, always in force."
                    " The Codex project hook attached them from `.cursor/rules/`;"
                    " no separate Codex copy is maintained. More rules are attached"
                    " automatically when you edit files they cover.",
                ),
            )

        deliver_session_start(session_id, source, always, deliver)
        return 0

    if not rules:
        return 0

    if event != "PreToolUse":
        return 0

    paths = patched_paths(payload.get("tool_input"))
    if not paths:
        return 0

    candidates: list[Rule] = []
    for rule in rules:
        if rule.always or not rule.globs:
            continue
        patterns = [glob_to_regex(g) for g in rule.globs]
        if any(p.match(path) for path in paths for p in patterns):
            candidates.append(rule)

    if not candidates:
        return 0

    touched = ", ".join(sorted(set(paths))[:8])

    def deliver(matched: list[Rule]) -> None:
        emit(
            event,
            render(
                matched,
                f"Project rules that cover the files you are editing ({touched})."
                " Apply them to this change before continuing.",
            ),
        )

    deliver_rule_context(session_id, candidates, deliver)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        # Fail open: never block an edit because of this hook.
        sys.exit(0)
