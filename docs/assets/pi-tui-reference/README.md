# pi TUI reference captures

Visual reference set for the coddy interactive console surface (`coddy` with no
arguments). Captured from pi v0.84.2 (`badlogic/pi-mono`, commit `b1efcf7d7`)
running against the `neuraldeep/qwen3.8-27b` backend in a 100x35 emulated
terminal (pexpect + pyte, dark theme defaults, bg `#18181e`).

Each state ships as a PNG (rendered via headless Chromium at 2x scale) plus the
plain-text screen dump (`.txt`) used for layout comparisons in tests.

| File | State |
|------|-------|
| `00-pi-docs-canonical.png` | Canonical interactive-mode screenshot from pi docs (context sections, tool box, footer). |
| `01-startup` | Fresh start: `pi vX` header, compact hint line, `[Skills]` section, editor, footer. |
| `02-startup-hints-expanded` | Header hints expanded via ctrl+o. |
| `03-slash-menu` | `/` autocomplete: two-column select list, `→ ` cursor, `(1/38)` scroll info. |
| `04-slash-filtered` | Slash list filtered by typed prefix. |
| `05-model-selector` | Ctrl+L model selector with search input and scroll info. |
| `06-bash-mode` | `!command` bash mode (border switches to `bashMode` green). |
| `07-editor-multiline` | Multi-line editor between `─` borders. |
| `10-working-spinner` | Braille spinner `⠴ Working...` while a turn runs. |
| `11-chat-response` | User message block + streamed assistant markdown. |
| `12-tool-read` / `13-tool-read-done` | Tool execution box (pending → success background), bold title, output preview. |
| `14-thinking-stream` / `15-thinking-done` | Italic gray thinking text streaming, then final answer. |
| `16-interrupted` | Turn aborted with Escape. |
| `17-session-selector` | `/resume` picker: scope/sort toggles, search hints. |
| `18-settings` | `/settings` settings list with value column and footer hints. |

Color contract comes from pi `theme/dark.json` and `theme/light.json`
(`packages/coding-agent/src/modes/interactive/theme/`); coddy maps those token
roles onto its own palette. The console visual contract is `docs/cli.md`.
