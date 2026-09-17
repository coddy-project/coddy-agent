#!/usr/bin/env python3
"""Compaction: /compact folds the older turns, the footer's context usage falls, and the session lives on.

The first turn attaches a large log file (``@service.log``, about 12k estimated
tokens), two short turns follow, and ``/compact`` then folds the first turn
(``keep_recent_turns`` keeps the two newest verbatim). The footer's context
percentage (``N.N%/<window> (auto)``) has to lose at least half of what the
log occupied, and stay down after the next turn.
"""

from __future__ import annotations

import re
import sys
import tempfile
from pathlib import Path

from cli_tui_driver import CR, CoddyTUI, ok

FILE_NAME = "service.log"
LOG_TOKENS = 12000
FOOTER_CONTEXT = re.compile(r"(\d+(?:\.\d+)?)%/(\d+(?:\.\d+)?)([kM]?) \(auto\)")


def service_log(tokens: int) -> str:
    """Deterministic log lines of about `tokens` estimated tokens (coddy estimates runes/4)."""
    lines, size, i = [], 0, 0
    while size < tokens * 4:
        i += 1
        line = f"log {i:05d}: service node-{i % 7} answered request {i * 37 % 1000:03d} in {i % 90 + 5} ms, status ok"
        lines.append(line)
        size += len(line) + 1
    return "\n".join(lines) + "\n"


def footer_context(tui: CoddyTUI) -> tuple[float, int]:
    """The footer's context percentage and the window it is a percentage of."""
    tui.pump(1.0)
    found = FOOTER_CONTEXT.findall(tui.text())
    if not found:
        tui.dump("no context percentage in the footer")
        raise AssertionError("the footer shows no context percentage")
    percent, window, unit = found[-1]
    scale = {"": 1, "k": 1_000, "M": 1_000_000}[unit]
    return float(percent), int(float(window) * scale)


def main() -> int:
    workdir = Path(tempfile.mkdtemp(prefix="coddy-cli-compact-work-"))
    (workdir / FILE_NAME).write_text(service_log(LOG_TOKENS), encoding="utf-8")
    tui = CoddyTUI("compact", workdir=str(workdir))
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            f"Remember this token: COMPACT_E2E_ALPHA. Keep @{FILE_NAME} in mind and reply with exactly: NOTED"
        )
        tui.wait_idle(timeout=240)
        tui.prompt("Reply with exactly: ONE")
        tui.wait_idle(timeout=240)
        tui.prompt("Now reply with one short sentence about terminals.")
        tui.wait_idle(timeout=240)
        before, window = footer_context(tui)
        log_share = LOG_TOKENS / window * 100
        if before < log_share:
            raise AssertionError(f"the attached log is not in the context: footer {before}% of {window}")

        tui.type_text("/compact")
        tui.send(CR)
        tui.wait_turn_started(timeout=60)
        tui.wait_idle(timeout=300)
        msgs = tui.messages()
        if not any(m.get("compaction_summary") for m in msgs):
            raise AssertionError("no compaction_summary row in messages.json")
        after, _ = footer_context(tui)
        if not after < before - log_share / 2:
            tui.dump("context percentage did not fall")
            raise AssertionError(f"footer context {before}% -> {after}% after /compact, want below {before - log_share / 2:.1f}%")

        tui.prompt("Reply with one short sentence: what did we talk about so far?")
        tui.wait_idle(timeout=240)
        later, _ = footer_context(tui)
        if not later < before - log_share / 2:
            raise AssertionError(f"footer context went back up after the next turn: {before}% -> {after}% -> {later}%")
        print(f"footer context: {before}% -> {after}% after /compact -> {later}% after the next turn (window {window})", file=sys.stderr)

        # The follow-up after compaction must produce a real assistant row
        # (matching the ACP twin's contract). Whether the model-generated
        # summary preserved the exact token is model quality, not surface
        # behavior, so the token itself is not asserted.
        msgs = tui.messages()
        compaction_at = next((i for i, m in enumerate(msgs) if m.get("compaction_summary")), None)
        if compaction_at is None:
            raise AssertionError("compaction row disappeared")
        after_rows = [
            m for m in msgs[compaction_at + 1 :]
            if m.get("role") == "assistant" and m.get("content", "").strip()
        ]
        if not after_rows:
            raise AssertionError("no assistant reply after compaction")
        return ok("cli_e2e_compact")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
