# Interactive console TUI (`coddy` / `coddy cli`)

The console surface is a terminal UI over the same machinery every other
surface uses: `session.Manager`, the agent runner, and ACP session updates.
Nothing agent-side is console-specific — the TUI is a fourth `UpdateSender`
next to ACP, HTTP, and the Telegram gateway.

Build: `make build TAGS=cli` (or any tag set including `cli`; the recommended
full binary is `make build TAGS="http ui scheduler memory cli"`). In builds
without the tag, `coddy cli` explains how to rebuild and bare `coddy` keeps
printing usage.

Launch: bare `coddy` on a terminal (both stdin and stdout must be ttys —
pipes and CI keep the usage contract), explicitly `coddy cli [flags]`, or with
flag-style shortcuts routed to the console: `coddy -c` continues the latest
session in this folder and `coddy -p "..."` runs one non-interactive prompt.
Quitting the console (double ctrl+c, ctrl+d, `/quit`) prints a resume hint
after the terminal is restored:

```
session: sess_1a2b3c4d
continue: coddy cli --session-id sess_1a2b3c4d  (or: coddy -c)
```

## Visual model

The layout replicates the pi coding agent's TUI (pi-mono `b1efcf7d7`,
v0.84.2, MIT, Mario Zechner — see the attribution note below) with colors
from the coddy SPA palette (`external/ui/src/styles.css`). Reference captures
of the pi original live in `docs/assets/pi-tui-reference/`; this document is
the visual contract for the coddy console.

Top to bottom:

- **Header**: `coddy` (bold accent) + dim version; a dim hint line
  (`escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ctrl+o more`);
  `[Context]` (instruction files) and `[Skills]` (loaded skill names).
  `ctrl+o` expands the full hint list and adds `[Rules]` and `[MCP]` sections.
- **Transcript**: user messages in full-width background boxes; assistant
  markdown (headings, bold/italic, inline code, ``` fences with borders,
  `│ ` quotes, lists, box-drawing tables, OSC 8 links); italic gray thinking
  blocks (collapse with `ctrl+t`); tool calls as background-tinted boxes
  (pending → success green tint / error red tint) with a bold title
  (`read <path>`, `$ command`), preview capped at 10 lines, and
  `... (ctrl+o to expand)` reading the full result from
  `sessions/<id>/tool_calls/`.
- **Status**: braille spinner `⠋⠙⠹...` at 80 ms with `Working...` while a turn
  runs.
- **Plan widget**: current todo entries (`✓` done, `◐` active, `○` pending,
  `✗` failed) above the editor.
- **Editor**: multi-line input between full-width `─` rules; wrap-aware
  cursor movement with sticky column; prompt history (up/down at edges, cap
  100); large pastes collapse into `[paste #N +K lines]` markers; scrolled
  content shows `─── ↑ N more ───` borders. Autocomplete: `/` commands on the
  first line, `@` file mentions (workspace walk capped at 50k entries;
  hidden directories, `node_modules`, and `.coddy` are skipped), `tab` forces file completion.
- **Footer**: dim `cwd (git-branch) • title [• plan]`, then
  `↑in ↓out  N.N%/ctx (auto)` left and `(provider) model [• reasoning]` right.

Rendering is pi's inline main-screen model: line-diff against the previous
frame, synchronized output (`ESC[?2026h/l`), per-line SGR + OSC 8 reset, a
zero-width APC cursor marker for IME hardware-cursor placement, 16 ms render
throttle with immediate renders after keystrokes.

## Commands and keys

Slash commands: client-side `/model`, `/mode`, `/resume`, `/new`, `/theme`,
`/hotkeys`, `/quit`; server-driven `/compact`, `/plugin`, and every loaded
skill (from the ACP available-commands catalog). Enter on a slash suggestion
applies and submits in one stroke.

| Key | Action |
|-----|--------|
| enter | send |
| shift+enter / ctrl+j | newline (backslash+enter also splits) |
| escape | interrupt the running turn (`HandleSessionCancel`) |
| ctrl+c | clear editor; twice within 2 s exits |
| ctrl+d | exit when the editor is empty |
| ctrl+l | model selector |
| ctrl+p / ctrl+shift+p | cycle configured models |
| shift+tab | cycle reasoning level (models with `reasoning_levels`) |
| ctrl+o | expand header hints + last tool output |
| ctrl+t | collapse/expand thinking blocks |
| up / down | history at the first/last line; cursor movement otherwise |

Modals replace the editor while open: permission requests (the option list
comes from the agent's `permission.Options`), the question tool (single or
multi-select via space, custom free-text answers), model/mode/theme/session
selectors (`→ ` cursor, type-to-filter, `(i/n)` scroll indicator).

## Flags

`--config --home --cwd --sessions-dir` mirror the other subcommands.
`--session-id <id>` reopens (or creates) that session and replays its
transcript. `-c/--continue` reopens the most recent session recorded for this
folder (errors when none exists; mutually exclusive with `--session-id` and
`--resume`). `--resume` opens the session picker first and creates nothing
until you choose (mutually exclusive with `--session-id`; `--model`,
`--mode`, and `--permission-mode` apply to whichever session the picker
selects). `--model`, `--mode agent|plan`, and
`--permission-mode ask|accept_edits|bypass` apply through the validated
manager config-option API before the UI starts, in every launch mode
(interactive, `--continue`, `--resume`, and `--prompt`). `--theme
dark|light|auto` (auto falls back COLORFGBG → dark). `--plain` disables
terminal queries, modifyOtherKeys, titles, and OSC 8 for deterministic
automation. Logging is forced away from the terminal into
`<home>/logs/cli.log` (`--log-file`, `--log-level`).

## One-shot print mode (`-p/--prompt`)

`coddy -p "..."` (or `coddy cli --prompt "..."`) runs a single agent turn
without a terminal: assistant text streams to stdout, diagnostics go to
stderr, and the process exits non-zero on errors or a cancelled turn. No tty
is required, so it fits scripts and cron. The turn persists as a normal
session, and `-c -p "..."` continues it — tokens, files, and tool state
carry over exactly like the interactive console. Permission requests resolve
non-interactively: `bypass` allows, anything else rejects the call with a
note on stderr. The question tool returns empty answers. `--model`, `--mode`,
`--permission-mode`, `--session-id`, and `--continue` all combine with
`--prompt`; `--resume` does not (it needs the interactive picker).

## Security

All text from outside the renderer — model output, tool previews, titles,
file names, skill and MCP names — is sanitized before display: ESC, C0
controls (except newline and tab), DEL, and C1 controls are stripped, so
model- or tool-supplied escape sequences can never reach the terminal. The
only control sequences in rendered lines are renderer-generated styling.

Permission mode `ask` renders every request as a modal; `bypass` short-circuits
exactly like the ACP `serverRef` path. Project-local MCP servers still go
through the workspace trust gate; a server pending approval stays disconnected
and is visible via `coddy mcp list` (approve with `coddy mcp trust <name>`).

## Testing

- Unit + BDD: `go test -tags=cli ./...`; the happy-path spec is
  `features/cli_tui.feature` run by `external/cli/bdd_cli_tui_test.go`
  (stub runner, fake terminal — no LLM, no pty).
- Live e2e: `./examples/test_cli.sh` drives the real binary in a pty
  (pexpect + pyte, Linux-only) against `neuraldeep/qwen3.8-27b` by default —
  see `examples/README.md`.

## Known v1 divergences from pi

Undo/yank-pop and jump-mode in the editor, kitty keyboard protocol
negotiation, inline images, `!` bash mode (a local exec path would bypass
`run_command` permissions — deferred until it routes through the session tool
path), pi's `/settings` surface, and the session picker's scope/sort/rename
controls. Theme switching rebuilds chrome; already-rendered transcript rows
keep their colors until the next session.

## Attribution

The TUI rendering model and visual design are ported from
[`pi-mono`](https://github.com/badlogic/pi-mono) `packages/tui` and
`packages/coding-agent` interactive mode (MIT License, Copyright (c) 2025
Mario Zechner), commit `b1efcf7d7`. The Go implementation in
`external/cli/tui` is an independent rewrite of the documented behavior.
