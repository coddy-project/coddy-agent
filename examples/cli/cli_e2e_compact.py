#!/usr/bin/env python3
"""Compaction: /compact inserts a compaction summary row and the session lives on."""

from __future__ import annotations

import sys

from cli_tui_driver import CR, CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("compact")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt("Remember this token: COMPACT_E2E_ALPHA. Reply briefly.")
        tui.wait_idle(timeout=240)
        tui.prompt("Now reply with one short sentence about terminals.")
        tui.wait_idle(timeout=240)
        tui.type_text("/compact")
        tui.send(CR)
        tui.wait_for("Working...", timeout=60)
        tui.wait_idle(timeout=300)
        msgs = tui.messages()
        if not any(m.get("compaction_summary") for m in msgs):
            raise AssertionError("no compaction_summary row in messages.json")
        tui.prompt("Which token did I ask you to remember? Answer with the token only.")
        tui.wait_idle(timeout=240)
        # Only assistant rows AFTER the compaction row prove the summary kept
        # the token; the original user prompt row must not satisfy this.
        msgs = tui.messages()
        compaction_at = next((i for i, m in enumerate(msgs) if m.get("compaction_summary")), None)
        if compaction_at is None:
            raise AssertionError("compaction row disappeared")
        after = "\n".join(
            m.get("content", "") for m in msgs[compaction_at + 1 :] if m.get("role") == "assistant"
        )
        if "COMPACT_E2E_ALPHA" not in after:
            raise AssertionError("post-compaction follow-up lost the summary context")
        return ok("cli_e2e_compact")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
