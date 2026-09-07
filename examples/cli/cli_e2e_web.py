#!/usr/bin/env python3
"""Web: websearch + webfetch run through the console and persist artifacts."""

from __future__ import annotations

import sys

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("web")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "First use websearch to search for: example domain. "
            "Then use webfetch to fetch https://example.com and quote one phrase from it."
        )
        tui.wait_tool_call("websearch", timeout=300)
        tui.wait_tool_call("webfetch", timeout=300)
        tui.wait_idle(timeout=300)
        fetched = ""
        for d in tui.session_dirs():
            for call in (d / "tool_calls").iterdir():
                meta = call / "meta.json"
                if meta.exists() and "webfetch" in meta.read_text():
                    result = call / "result.md"
                    if result.exists():
                        fetched += result.read_text()
        if "Example Domain" not in fetched and "example" not in fetched.lower():
            raise AssertionError("webfetch result.md does not carry example.com content")
        return ok("cli_e2e_web")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
