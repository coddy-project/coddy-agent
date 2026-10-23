#!/usr/bin/env python3
"""Models: the /model command switches the session model and the footer follows;
--model beats the model the console remembers from its last run."""

from __future__ import annotations

import json
import sys
import tempfile
import time
from pathlib import Path

from cli_tui_driver import CR, DEFAULT_MODEL, CoddyTUI, ok

# The two models of examples/config.demo.yaml, as the footer names them.
DEMO_MODELS = ["rpa/qwen3.6-35b-a3b", "neuraldeep/qwen3.8-27b"]


def footer_name(model: str) -> str:
    provider, name = model.split("/", 1)
    return f"({provider}) {name}"


def footer_text(tui: CoddyTUI) -> str:
    """The footer rows: the screen below the editor's bottom border.

    The model the session runs on is named there, as "(provider) model", and
    nowhere else a stale name could stay: the rest of the screen is the
    transcript.
    """
    rows = tui.screen.display
    border = max((i for i, row in enumerate(rows) if row.startswith("─" * 20)), default=-1)
    return "\n".join(rows[border + 1 :]) if border >= 0 else ""


def wait_footer(tui: CoddyTUI, needle: str, timeout: float) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        tui.pump(0.3)
        if needle in footer_text(tui):
            return
    tui.dump(f"the footer never showed {needle!r}")
    raise AssertionError(f"the footer never showed {needle!r}")


def flag_beats_remembered_model() -> None:
    """`--model X` starts on X while console-state.json remembers another model.

    The remembered model is applied by the console at startup and the launch
    option after it; the launch option wins however the two are scheduled
    (issue #542). The driver always passes --model, so the home is seeded with
    the other demo model as the console's last pick.
    """
    remembered = next(m for m in DEMO_MODELS if m != DEFAULT_MODEL)
    home = Path(tempfile.mkdtemp(prefix="coddy-cli-models-flag-home-"))
    (home / "console-state.json").write_text(json.dumps({"last_model": remembered}))
    tui = CoddyTUI("models-flag", home=str(home))
    try:
        tui.wait_for("coddy v", timeout=30)
        wait_footer(tui, footer_name(DEFAULT_MODEL), timeout=10)
        # A write of the remembered model that lands late would show by now.
        tui.pump(2.0)
        footer = footer_text(tui)
        if footer_name(remembered) in footer or footer_name(DEFAULT_MODEL) not in footer:
            tui.dump("the remembered model replaced --model")
            raise AssertionError(
                f"--model {DEFAULT_MODEL} was overridden by the remembered {remembered}; footer: {footer!r}"
            )
    finally:
        tui.close()


def main() -> int:
    tui = CoddyTUI("models")
    try:
        tui.wait_for("coddy v", timeout=30)
        wait_footer(tui, footer_name(DEFAULT_MODEL), timeout=10)  # footer shows the default
        # Switch by explicit id (validated by the manager against YAML models)
        # to the other demo model, whichever MODEL starts the console on.
        other = next(m for m in DEMO_MODELS if m != DEFAULT_MODEL)
        tui.type_text(f"/model {other}")
        tui.send(CR)
        wait_footer(tui, footer_name(other), timeout=20)
        # A settings command is not a message: the session is written with
        # its first prompt, the model chosen here with it
        # (TestDeferredBundleKeepsASettingChosenBeforeTheFirstPrompt).
        if tui.session_dirs():
            raise AssertionError(f"/model alone wrote the session: {[d.name for d in tui.session_dirs()]}")
    finally:
        tui.close()
    flag_beats_remembered_model()
    return ok("cli_e2e_models")


if __name__ == "__main__":
    sys.exit(main())
