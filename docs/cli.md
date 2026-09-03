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
- **Status**: braille spinner `⠋⠙⠹...` at 80 ms with a live status line naming
  the current step while a turn runs - verb plus target plus elapsed counter
  (`Reading README.md · 12s`, `Running npm test · 3s`, `Thinking… · 2s`,
  `Responding`; `Running subagent reviewer · 40s` while a `spawn_agent` call
  is in flight). A plain wait escalates with time: `Waiting for the model` →
  `The model is taking longer than usual` (15 s) → `Still no response from the
  server` (60 s). While a permission or question modal is open the line shows
  `Waiting for your approval` / `Waiting for your answer` with **no** counter
  (nothing is running), and after approval it returns to the gated tool with a
  restarted counter. Phrase table lives in `external/cli/status.go` (Go twin of
  the SPA's `liveStatus.ts`).
- **Plan widget**: current todo entries (`✓` done, `◐` active, `○` pending,
  `✗` failed) above the editor.
- **Editor**: multi-line input between full-width `─` rules (green while the
  buffer holds a `!!` command); wrap-aware
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
| escape | interrupt the running turn (`HandleSessionCancel`), or stop a `!!` command |
| ctrl+c | clear editor; twice within 2 s exits |
| ctrl+d | exit when the editor is empty |
| ctrl+l | model selector |
| ctrl+p / ctrl+shift+p | cycle configured models |
| shift+tab | cycle reasoning level (models with `reasoning_levels`) |
| ctrl+o | expand header hints + last tool output + last `!!` block |
| ctrl+t | collapse/expand thinking blocks |
| up / down | history at the first/last line; cursor movement otherwise |

## Local shell (`!!`)

A submitted line that starts with `!!` is not a prompt. Coddy runs the rest of
it in the workspace with the shell `run_command` uses, and neither the command
nor its output is shown to the model, persisted in the session bundle, or
counted against the context window. This is pi's bash-mode prefix; pi's other
half, `!`, which feeds the output back to the model, is still deferred.

- the prefix counts at the start of the submitted line only; while the buffer
  holds a command the editor rules turn green. To send a prompt that really
  starts with `!!`, escape it once: `\!!careful` reaches the model as
  `!!careful`;
- output streams into its own transcript block: the last 10 lines while
  collapsed, the whole capture on `ctrl+o`, and a closing `exit N` when the
  command failed. Output is decoded and stripped of control sequences exactly
  like tool output, and only the last 256 KiB is kept (the block says how much
  it dropped), so a `tail -f` neither grows the console nor slows it down;
- there is no permission modal, and `ask` / `accept_edits` / `bypass` do not
  apply: the operator who typed the command is the principal, not the model.
  Plan mode restricts the agent's tools, not the terminal in front of you;
- `escape` kills the running command and the process group it leads, and so
  does quitting the console, so nothing keeps printing into the terminal coddy
  just gave back. Work the command deliberately detaches (`cmd &`, `nohup`)
  outlives it exactly as in your own shell: once the command itself exits the
  console has nothing left to stop. A second `escape` gives the console back
  when a kill cannot reap its target at all. There is no timeout and no
  adoption into the background task pool - that pool is agent-visible on
  purpose;
- one at a time: a `!!` line is refused while a turn runs, and while a command
  runs the console refuses prompts, another `!!`, and every modal (`/new`,
  `/resume`, `/mode`, `/theme`, `ctrl+l`), each with a status line saying so -
  none of them queue. A modal would swallow `escape`, which is the only key
  that stops the command;
- the command reads from the null device, not from the terminal: an
  interactive program (`!!python`) sees EOF instead of stealing keystrokes
  from the editor;
- `--remote` refuses it. The session workspace lives on the server, so running
  the command on this machine would silently touch a different tree;
- `-p/--prompt` does not interpret the prefix: a one-shot prompt goes to the
  model verbatim.

The block belongs to the running console only. Reopening the session with
`-c`, `--session-id`, or `/resume` does not replay it, because nothing about a
`!!` command is written to disk.

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

## Remote mode (`--remote`)

`--remote <target>` points the console (interactive and `-p` print runs) at a
remote `coddy http` server instead of running the agent in-process. The
target is a configured remote name (`httpserver.remotes`), a bare
`host:port` (scheme defaults to http), or a full http(s) URL. The bearer
token comes from `--remote-token` or `CODDY_REMOTE_TOKEN`; tokens are
deliberately never read from config.yaml. The same pair of flags works on
`coddy acp`, so an ACP editor can drive a remote coddy too.

Remotely, turns execute on the server in its workspace: the transcript, tool
boxes, thinking, plan updates, token and context stats stream back over SSE;
permission and question modals answer through the server's REST endpoints;
`ctrl+o` fetches full tool output from the server. The model selector lists
the remote catalog (`GET /v1/models`), `/mode` picks the agent or plan
profile per turn, and `/resume`, `-c`, and `--session-id` operate on the
server's session list (the local folder filter does not apply). The
permission mode is governed by the remote server's configuration:
`--permission-mode` and the `/permissions` option are rejected with a clear
error. Reasoning-level cycling is unavailable remotely in v1. Sessions
persist only on the server; the startup banner shows `remote: <url>` and the
exit hint prints a reconnect command with `--remote` included.

## Subagent definitions (`coddy agents`)

Three subcommands manage the subagent definitions the agent may delegate to
(`docs/subagents.md`). They need no build tag, like `coddy mcp` and
`coddy rules`:

```
coddy agents list [--cwd DIR]
coddy agents trust <name> [--cwd DIR]
coddy agents untrust <name> [--cwd DIR]
```

`list` prints the workspace and the effective `subagents.project_trust`, then
the catalog as a table with `NAME`, `SCOPE` (`builtin`, `user`, `project`),
`TRUST` (`trusted` or `needs_approval`), `FLAGS` (`hidden`, `model=…`,
`mode=…`), `DESCRIPTION` and `PATH` (`(embedded)` for built-ins), a `(total N)`
line, and a hint when project definitions await approval. `trust` prints the
effective declaration first (file, model, mode, permission mode, tool lists,
digest, receipt path) and then records a receipt for the file as it is on disk
right now, keyed by the canonical workspace, the name and the file digest, in
`<home>/subagents-trust.json`; a built-in or user-scope name needs no approval
and the command says so. `untrust` withdraws a receipt. `--cwd` defaults to the
process working directory, resolved like `coddy mcp`. Under
`subagents.project_trust: deny` project files are not listed at all, and under
`allow` they need no receipt.

In the console a `spawn_agent` call shows as a tool box like any other, and the
status line reads `Running subagent <name>` with its elapsed counter for as long
as the child runs. A child's permission request, while its spawning turn is
still alive, opens the usual modal in the parent chat with the title prefixed
`[subagent <name>]`. Child sessions (`sub_…` ids) are read-only transcripts:
`-c` never picks one, and a prompt sent to one is refused with a message naming
the parent session.

## Security

All text from outside the renderer — model output, tool previews, titles,
file names, skill and MCP names — is sanitized before display: ESC, C0
controls (except newline and tab), DEL, and C1 controls are stripped, so
model- or tool-supplied escape sequences can never reach the terminal. The
only control sequences in rendered lines are renderer-generated styling.

Permission mode `ask` renders every request as a modal; `bypass` short-circuits
exactly like the ACP `serverRef` path. The `!!` prefix has no permission gate
by design (see **Local shell**): it can only be reached from a submitted
editor buffer, never from model output, so nothing the model writes can start
a command through it. Project-local MCP servers still go
through the workspace trust gate; a server pending approval stays disconnected
and is visible via `coddy mcp list` (approve with `coddy mcp trust <name>`).

## Captures

Two capture sets exist, and they answer different questions.

`docs/assets/screenshot-console-*.png` are photographs of the running console
in a real **Konsole** window (1920 px wide, cropped to the used rows): the
launch line and header with `[Context]` / `[Skills]`
(`screenshot-console-start.png`), the `ctrl+l` model selector
(`screenshot-console-models.png`), and a finished turn with a tool box, a
thinking block, and the footer counters (`screenshot-console-chat.png`). Use
these in README and on the site: they show what a user actually sees in a
terminal emulator.

`docs/assets/cli-tui/` is the deterministic set produced by
`examples/cli/capture.py`, which drives the shared e2e driver and renders each
state from the pyte buffer as `.txt`, styled `.html`, and `.png`. Those are
regression references for colors and cell layout, not marketing images;
regenerate them when the transcript chrome changes.

`docs/assets/pi-tui-reference/` holds captures of the pi original for
comparison, as described under **Visual model**.

## Testing

- Unit + BDD: `go test -tags=cli ./...`; the happy-path spec is
  `features/cli_tui.feature` run by `external/cli/bdd_cli_tui_test.go`
  (stub runner, fake terminal — no LLM, no pty).
- Live e2e: `./examples/test_cli.sh` drives the real binary in a pty
  (pexpect + pyte, Linux-only) against `neuraldeep/qwen3.8-27b` by default —
  see `examples/README.md`.

## Known v1 divergences from pi

Undo/yank-pop and jump-mode in the editor, kitty keyboard protocol
negotiation, inline images, pi's `!` bash mode (the half that feeds command
output back to the model: `!!` ships as described under **Local shell**, while
`!` needs a model-visible transcript record and stays deferred), pi's
`/settings` surface, and the session picker's scope/sort/rename controls. Theme switching rebuilds chrome; already-rendered transcript rows
keep their colors until the next session.

## Attribution

The TUI rendering model and visual design are ported from
[`pi-mono`](https://github.com/badlogic/pi-mono) `packages/tui` and
`packages/coding-agent` interactive mode (MIT License, Copyright (c) 2025
Mario Zechner), commit `b1efcf7d7`. The Go implementation in
`external/cli/tui` is an independent rewrite of the documented behavior.
