"""Shared constants and checks for the ask-mode e2e harnesses (HTTP, ACP, CLI).

The scenario is the same on every surface: the workspace holds a note with a
marker, the prompt asks the model to read the note AND to create a file. In ask
mode the read must work and the write must not happen (the tool is not offered
and would be refused if the model tried); in agent mode the same prompt must
produce the file, which is the regression half of the check.
"""

from __future__ import annotations

import time
from pathlib import Path

ASK_NOTE_FILE = "ask_e2e_note.txt"
ASK_NOTE_MARKER = "ASK_E2E_NOTE_MARKER_v1"
ASK_ARTIFACT = "ask_e2e_written.txt"
ASK_ARTIFACT_MARKER = "ASK_E2E_WRITE_OK"

# The read-only tool set ask mode advertises; anything else in a tool call
# during an ask turn is a failure.
READ_ONLY_TOOLS = frozenset(
    {
        "read",
        "keep_result",
        "glob",
        "grep",
        "print_tree",
        "websearch",
        "webfetch",
        "question",
        "load_skill",
    }
)


def seed_note(work: Path) -> Path:
    path = work / ASK_NOTE_FILE
    path.write_text(f"note for the ask e2e\n{ASK_NOTE_MARKER}\n", encoding="utf-8")
    artifact = work / ASK_ARTIFACT
    if artifact.exists():
        artifact.unlink()
    return path


def ask_prompt_text(work: Path | str) -> str:
    return f"""Working directory: {work}

Do exactly this, in order:

1. Read the file {ASK_NOTE_FILE} in the working directory with the read tool.
2. Create the file {ASK_ARTIFACT} in the working directory containing exactly: {ASK_ARTIFACT_MARKER}
3. Reply with one line: ASK_E2E_DONE followed by the marker line you read in step 1, quoted verbatim.

If a step is impossible with the tools you have, say so in one sentence and continue with the next step.
"""


def check_ask_turn(tool_names: set[str] | list[str], answer: str, work: Path) -> None:
    """Assert the read-only contract after an ask turn."""
    used = {n for n in tool_names if n}
    mutating = sorted(used - READ_ONLY_TOOLS)
    if mutating:
        raise RuntimeError(f"ask turn used non read-only tools: {mutating} (all: {sorted(used)})")
    if "read" not in used:
        raise RuntimeError(f"ask turn never called read (tools used: {sorted(used)})")
    artifact = work / ASK_ARTIFACT
    if artifact.exists():
        raise RuntimeError(f"ask turn created {artifact}: the write was not refused")
    if ASK_NOTE_MARKER not in answer:
        raise RuntimeError(f"ask answer lacks the note marker: {answer[:400]!r}")


def wait_for_artifact(work: Path, timeout_s: float = 240) -> Path:
    """Wait for the agent-mode half: the same prompt must now write the file."""
    path = work / ASK_ARTIFACT
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        if path.is_file() and ASK_ARTIFACT_MARKER in path.read_text(encoding="utf-8", errors="replace"):
            return path
        time.sleep(0.35)
    raise RuntimeError(f"agent turn did not create {path} with {ASK_ARTIFACT_MARKER}")
