#!/usr/bin/env python3
"""Todo: the plan widget reflects todo tools and the backlog persists on disk."""

from __future__ import annotations

import sys

from cli_tui_driver import CoddyTUI, ok


def main() -> int:
    tui = CoddyTUI("todo")
    try:
        tui.wait_for("coddy v", timeout=30)
        tui.prompt(
            "Create a todo plan with coddy_todo_plan_replace for making tea: "
            "three items (boil water, brew, pour). Then mark every item completed "
            "with coddy_todo_item_update, one by one."
        )
        tui.wait_tool_call("coddy_todo_plan_replace", timeout=300)
        tui.wait_idle(timeout=420)
        todo = tui.wait_session_file("todos/active.md", timeout=30)
        content = todo.read_text()
        if "tea" not in content.lower() and "boil" not in content.lower():
            raise AssertionError(f"todos/active.md has unexpected content:\n{content}")
        if "[x]" not in content:
            raise AssertionError(f"no completed items recorded in todos/active.md:\n{content}")
        return ok("cli_e2e_todo")
    finally:
        tui.close()


if __name__ == "__main__":
    sys.exit(main())
