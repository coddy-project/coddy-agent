#!/usr/bin/env python3
"""Tool calls persist to disk: args.json, result.md, meta.json for a read."""

from __future__ import annotations

import sys

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("toolcalls")
    try:
        (tui.workdir / "note.txt").write_text("TOOLCALL_PERSIST_TOKEN\n")
        tui.wait_for("coddy v", timeout=30)
        tui.prompt("Use the read tool to read note.txt and quote its content.")
        tui.wait_tool_call("read", timeout=240)
        tui.wait_idle(timeout=240)
        found = False
        for d in tui.session_dirs():
            for call in (d / "tool_calls").iterdir():
                if not (call / "meta.json").exists():
                    continue
                if "read" in (call / "meta.json").read_text():
                    if (call / "args.json").exists() and (call / "result.md").exists():
                        if "TOOLCALL_PERSIST_TOKEN" in (call / "result.md").read_text():
                            found = True
        if not found:
            raise AssertionError("read call artifacts incomplete")
        return ok("cli_e2e_toolcalls_persist")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
