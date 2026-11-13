# PR #712: rendering and selectors in small terminals

The `ctrl+l` model selector in a real nine-row pty (72 columns, dark theme),
taken with `examples/cli/capture_short_terminal.py` against three binaries.
The highlight was walked five rows down in every shot, so the list has to
scroll in all three.

| File | Build | What the terminal shows |
|---|---|---|
| `before-main.png` | `origin/main` | no title, no top border, no scroll indicator: the modal is taller than the terminal and scrolls itself off |
| `pr-as-submitted.png` | `ad8bb5c2e`, the PR as opened | the title and `(6/8)` are back, the top border is still cut: the budget counted one row for a footer that draws two |
| `after-fix.png` | `b506871b1`, with the follow-up commit | both borders, the title, the highlighted model, `(6/8)`, the help line and the footer all fit in nine rows |
