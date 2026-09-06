#!/usr/bin/env python3
"""Mentions: a ranged ``@file:3-4`` typed into the console attaches only those lines."""

from __future__ import annotations

import re
import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CoddyTUI, ok

FILE_NAME = "mention_lines.txt"
LINE_TOKENS = ["MENTION_L1:alpha", "MENTION_L2:bravo", "MENTION_L3:charlie", "MENTION_L4:delta", "MENTION_L5:echo", "MENTION_L6:foxtrot"]


def main() -> int:
    workdir = Path(tempfile.mkdtemp(prefix="coddy-cli-mentions-work-"))
    (workdir / FILE_NAME).write_text("\n".join(LINE_TOKENS) + "\n", encoding="utf-8")
    tui = CoddyTUI("mentions", workdir=str(workdir))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "The attachment holds two lines of a file. Reply with exactly those two "
            f"lines verbatim and nothing else. @{FILE_NAME}:3-4"
        )
        tui.wait_idle(timeout=420)
        reply = tui.assistant_text()
        if LINE_TOKENS[2] not in reply or LINE_TOKENS[3] not in reply:
            raise AssertionError(f"reply does not quote lines 3-4: {reply[:800]!r}")
        user_rows = [str(m.get("content", "")) for m in tui.messages() if m.get("role") == "user"]
        hydrated = next((c for c in user_rows if 'lines="3-4"' in c), "")
        if not hydrated:
            raise AssertionError('persisted user turn lacks lines="3-4"')
        tag = re.search(r"<coddy_attachment [^>]*>", hydrated)
        if not tag or f'path="{FILE_NAME}"' not in tag.group(0):
            raise AssertionError(f"attachment tag missing: {hydrated[:600]!r}")
        body = hydrated[tag.end():]
        for tok in LINE_TOKENS[2:4]:
            if tok not in body:
                raise AssertionError(f"{tok} missing from the hydrated body")
        for tok in (LINE_TOKENS[0], LINE_TOKENS[4]):
            if tok in body:
                raise AssertionError(f"{tok} leaked into a 3-4 attachment")
        return ok("cli_e2e_mentions")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
