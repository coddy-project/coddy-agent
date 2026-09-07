# Plan: interactive console TUI (`coddy` with no arguments)

Status: shipped in main from `feat/cli-tui` (plan v2 + two post-implementation
cross-review rounds: codex gpt-5.6-sol xhigh, cursor grok-4.6); first release
tag **0.9.66**. Documented v1 divergences live in `docs/cli.md`, which is the
current reference; this file stays as the design record.
Review deltas from v1 are marked inline as `[rev]`.

## 1. Goal

Add a fourth surface to coddy: an interactive terminal UI, replicating the pi
coding agent's TUI (pi-mono `b1efcf7d7`, v0.84.2, MIT — Mario Zechner) in
layout, borders, prefixes, spacing, spinner, and markdown styling, with colors
from coddy's SPA palette. Bare `coddy` on a TTY starts it.

`[rev]` "Pixel parity" is **visual chrome parity**: geometry, borders,
prefixes (`→ `, `│ `, `(i/n)`), spinner frames, tool-box structure, editor
rules, footer layout. It is *not* transcript-equality with the pi reference
dumps (those contain pi-specific content: pi version line, pi skills, pi slash
catalog). Undo/yank-pop, kitty protocol negotiation, `!` bash mode, images,
and pi's `/settings`, session-picker scope/sort/rename controls are **out of
scope for v1** (documented divergences, follow-ups). Attribution: package doc
comment + `docs/cli.md` credit pi-tui (MIT, commit `b1efcf7d7`).

Reference captures: `docs/assets/pi-tui-reference/` (PNG + `.txt`, committed).
The `.txt` dumps are the layout oracle for chrome only `[rev]`.

Everything reuses existing coddy machinery: `session.Manager`,
`agent.NewAgent(...).Run`, `acp.UpdateSender`, `permission.Options` (already
applied by the agent — the TUI renders `params.Options` as given `[rev]`),
config options (`mode`/`model`/`permission_mode`), skills slash catalog,
`/compact` + `/plugin` built-ins, session snapshots and replay.

## 2. Placement and build wiring

- Package **`external/cli`** (app) + **`external/cli/tui`** (framework),
  every file `//go:build cli`.
- **`cmd/coddy/cli.go`** (`//go:build cli`) + **`cmd/coddy/cli_stub.go`**
  (`//go:build !cli`): `runCLI(args []string) error`; stub explains the tag.
  `[rev]` The TTY check lives in tagged files too: `cliIsInteractive() bool`
  in `cli.go` uses `x/term.IsTerminal`; the `!cli` stub returns false with
  **zero new imports**, so the lean default build gains no dependency.
- Dispatch: explicit `coddy cli [flags]` case + bare `coddy`:
  `if len(args) == 0 { if cliIsInteractive() { err = runCLI(nil) } else
  { printUsage(os.Stderr); os.Exit(1) } }`. `[rev]` `runCLI` re-checks TTY
  itself and fails with a clear message before touching raw mode (covers
  `coddy cli < /dev/null`, redirected stdout, CI).
- `[rev]` Dependency facts: `golang.org/x/term` is **absent from go.mod**
  (present only in go.sum) — an explicit `go get golang.org/x/term` is
  required. `rivo/uniseg` + `mattn/go-runewidth` are indirect and only get
  promoted. All three resolve without network beyond the module cache.
- Import-cycle break: `cli.CommandDeps{EnsureHome, OpenStore}` `[rev]` (two
  fields; `NewServerRef` is HTTP-specific and not copied).
- Flags: `--config --home --cwd --sessions-dir --session-id --resume --model
  --mode agent|plan --permission-mode ask|accept_edits|bypass --theme
  dark|light|auto --plain --log-level --log-file`, plus
  `config.SkillsAutoDiscoveryFlagName`, `config.ProjectTrustFlagName`.
- `[rev]` Flags are **applied**, not just parsed:
  - `--session-id` → `mgr.SetPreferredSessionID(id)` before `HandleSessionNew`
    (reopen semantics identical to ACP `cmd/coddy/main.go:231`);
  - `--resume` → **no session is created**; the picker runs first, then
    either `HandleSessionLoad` (choice) or `HandleSessionNew` (esc → new);
  - `--model` → `HandleSessionSetConfigOption{ConfigID:"model"}` (validated
    against YAML models by the manager);
  - `--mode` → `HandleSessionSetMode`;
  - `--permission-mode` → `HandleSessionSetConfigOption{ConfigID:
    "permission_mode"}`;
  - every error from these calls is fatal **before** raw mode starts; no
    `res, _ :=` discards `[rev]`.
- `[rev]` Logger: the CLI forces `cfg.Logger` outputs to exactly `["file"]`
  (default path `<home>/logs/cli.log`), overriding YAML stdout/stderr choices
  — `ApplyOverrides` with `Output="file"` + file path, plus a hard fallback
  that drops stdout/stderr from `cfg.Logger.Outputs` if YAML sneaks them in.
  Unit-tested before raw mode.
- `--plain` `[rev]` (specified): no modifyOtherKeys push, no OSC title, no
  OSC 11 theme query, `clearOnShrink=false`, no OSC 8 hyperlinks; raw mode,
  diff rendering, bracketed paste stay. Purpose: deterministic e2e through
  pyte and dumb terminals.
- Makefile `[rev]`:
  - `test`: add `go test -tags=cli ./...`, `go test -tags="cli scheduler
    memory" ./...`, `go test -tags="http cli" ./...`, and
    `go test -tags="http scheduler ui memory cli" ./...` (release set).
  - `lint`: append a tagged pass over the new surface:
    `golangci-lint run --build-tags cli ./external/cli/... ./cmd/coddy/...`;
    same addition in `lint-windows`.
  - `check-windows`: add `-tags=cli` and `-tags="cli scheduler memory"`
    build+vet rows.
  - `FULL_TAGS` → `http ui scheduler memory cli`.
- `[rev]` Release surfaces: append `cli` (keeping `gateway` where present) in
  `Dockerfile`, `.github/workflows/release-binaries.yaml`,
  `.github/workflows/docker-build-push.yaml`, `examples/build_coddy.sh`
  (default TAGS `http scheduler memory cli`), README, `docs/build.md`.
- `[rev]` CI windows job: add `go test -tags=cli ./external/cli/...` to the
  `windows-latest` matrix (fake-terminal unit + lifecycle tests are
  OS-agnostic; raw-tty paths are guarded and skipped without a console).

## 3. `external/cli/tui` — framework port (pi-tui main-screen subset)

As v1 (Component/Container, line-diff renderer with synchronized output,
`SEGMENT_RESET`, APC cursor marker, 16 ms throttle, StdinBuffer, keys,
Text/TruncatedText/Spacer/Box/DynamicBorder/Loader/SelectList/Editor/Markdown,
truecolor+256 theme engine), with review deltas:

- `[rev]` **Untrusted-text boundary (blocker fix).** All text that enters
  components from outside the renderer — LLM chunks, tool previews, titles,
  file names, session titles, skills, rules, MCP names, config values — is
  sanitized first: `SanitizeText()` strips ESC (0x1b), C0 controls except
  `\n`/`\t`, DEL, and C1 bytes. Only renderer-generated styling (theme fg/bg,
  bold/italic, OSC 8 built by the markdown renderer from validated URLs)
  exists in rendered lines. `internal/platform/ansi.go` precedent noted; the
  sanitizer lives in `tui` and is applied by every app-side component
  constructor/setter. Unit tests include hostile inputs (OSC 52 clipboard,
  title changes, cursor moves, mode switches, fake chrome).
- `[rev]` Width parser semantics kept byte-compatible with pi (`m G K H J`
  CSI finals + OSC + APC are invisible; other CSI count as text). This is
  safe *because* app text is sanitized — no foreign CSI can reach lines.
  Documented in the package doc.
- `[rev]` **Terminal restoration ownership (blocker fix).** One
  `App.Shutdown()` path restores: modifyOtherKeys off, bracketed paste off,
  cursor show, raw-mode restore, title reset — invoked via (a) normal exit,
  (b) `defer` + `recover` around the UI loop (panic re-raised after restore),
  (c) `signal.NotifyContext(SIGINT, SIGTERM)`, (d) init-failure unwind
  (errors after `MakeRaw` restore before returning). `SIGTSTP`/`SIGCONT`
  suspend is a documented v1 gap. Lifecycle unit tests use the fake terminal
  and assert the exact restore byte sequence in every path.
- `[rev]` `clearOnShrink` defaults **off** in the coddy app (matches pi's
  shipped setting default; avoids `\x1b[3J` scrollback destruction in normal
  operation). Width/height changes still full-redraw.
- `[rev]` OSC 11 auto-theme: the reply arrives on stdin; the app installs an
  input filter (pi-style input listener) that consumes `\x1b]11;...` replies
  before editor dispatch, with a 500 ms fallback to `COLORFGBG` → dark. Only
  when `--theme auto` and not `--plain`. Typed input during the probe is
  buffered normally (filter passes everything else through). e2e always pins
  `--theme dark`.
- Theme values: as v1 table, with decisions fixed `[rev]`:
  `toolSuccessBg` dark `#243024` / light `#e8f0e8`; `borderAccent` dark
  `#c4b5fd` sourced from `--coddy-context-ring-inner` family (not
  link-action). Final values live in `external/cli/theme_dark.go` /
  `theme_light.go` `[rev]` (app package, wired into `tui.Theme` maps).

## 4. `external/cli` — the interactive app

### Wiring and lifecycle `[rev]` (reworked after review)

One `Sender` (implements `acp.UpdateSender`) is created **before** the
manager and passed as the manager's server sender *and* used for every
`HandleSessionPromptWithSender` call — mode/config/slash-catalog updates,
context-usage, and replay all reach the UI through the same object.

Concurrency contract `[rev]`:
- `Sender.SendSessionUpdate` posts to a **buffered channel** (size 1024)
  drained by the UI goroutine; if the buffer is full the post blocks — that
  is acceptable because every manager call that can emit updates
  (`HandleSessionLoad`, `HandleSessionPromptWithSender`,
  `HandleSessionReady`, `HandleSessionNew`) is **always invoked from worker
  goroutines, never from the UI goroutine**. The UI goroutine only renders,
  handles input, and spawns workers. This rule makes replay deadlock
  impossible and is asserted by a test that loads a large session while the
  UI loop is paused.
- `RequestPermission` / `RequestQuestion` block their worker on a reply
  channel; the UI shows the modal and replies. Ctx cancellation returns
  `Outcome:"cancelled"`.
- Updates carry `SessionID`; the app drops updates whose id differs from the
  active session (stale turns after `/new`, `/resume`) `[rev]`.
- Session switch: cancel active turn (`HandleSessionCancel`), wait for the
  worker to return, then `ForgetLiveSession(oldID)` on `/new` `[rev]`; MCP
  clients of the abandoned state close via the manager's usual paths (noted:
  `State.CloseAll` exists if leak tests show otherwise).

Startup sequences `[rev]`:
- default: `HandleSessionNew` (with preferred id when `--session-id`) →
  apply `--model/--mode/--permission-mode` → `HandleSessionReady(id)`.
- `--resume`: picker over `HandleSessionList` `[rev]` (manager API, not raw
  store) → `HandleSessionLoad` → `HandleSessionReady(id)` `[rev]` (ACP
  invokes readiness after load as well — `internal/acp/server.go:157,168`).
- Errors from any of these render to stderr and exit non-zero **before** raw
  mode; after raw mode they funnel through Shutdown.

### Update rendering `[rev]` (corrected map)

| Update | Rendering |
|---|---|
| `MessageChunkUpdate` `agent_message_chunk` + text | streaming assistant Markdown |
| `MessageChunkUpdate` `agent_message_chunk` + reasoning | italic thinking block (ctrl+t collapses) |
| `MessageChunkUpdate` `user_message_chunk` `[rev]` | user Box (replay path; live echo is local — see below) |
| `ToolCallUpdate` pending | Box `toolPendingBg`; title = tool name; kind glyph |
| `ToolCallStatusUpdate` in_progress | args preview from raw `InputJSON` `[rev]` (title heuristic: `read`/`write` show `path` arg, `run_command` shows `$ command`, else tool name — parsed from args JSON, sanitized) |
| completed / failed | bg → success/error; preview from `coddy.toolResultPreview` meta (**server truncates at 19 lines** `[rev]`); collapsed to 10 lines client-side with `... (ctrl+o to expand)` |
| ctrl+o expand `[rev]` | reads the full text via `session.ReadToolCallResult(sessionDir, toolCallID)`; missing file → keep preview + dim note |
| cancelled | error bg + `cancelled` note |
| `PlanUpdate` | todo widget above editor: `✓` completed, `◐` in_progress, `○` pending, `✗` failed `[rev]` |
| `TokenUsageUpdate` | footer; the TUI **accumulates** per-turn `Input`/`Output` sums itself; `TotalTokens` taken from the update `[rev]` |
| `UsageUpdate` | footer context percent |
| `ModeUpdate` / `ConfigOptionUpdate` | footer + selector state |
| `AvailableCommandsUpdate` | slash catalog |
| `MemoryPhaseUpdate` + `MemoryMessageChunkUpdate` `[rev]` | dim status line + collapsible memory block (memory builds) |

Live user echo `[rev]`: on submit the app appends the user Box locally
(manager does not emit it live); replayed `user_message_chunk` renders the
same component; the two never coexist because replay only happens on load.

Prompt errors `[rev]`: non-busy errors from `HandleSessionPromptWithSender`
render as an error status line; the editor regains focus with the submitted
text recoverable from history (`up`).

### Layout, commands, keys

As v1 (header/chat/status/plan-widget/editor/footer) with review deltas:
- Header `[rev]` per pi dumps: line `coddy` bold accent + dim version;
  compact hint line; dim onboarding line; `[Context]` (instruction files) and
  `[Skills]` (names) at startup; ctrl+o toggles the expanded hint list adding
  `[Rules]` count and `[MCP]` servers. Divergence from pi's expanded view
  (skill paths, "Tool output: expanded") documented in docs/cli.md.
- Footer `[rev]` per pi dumps: line 1 dim `cwd (git-branch) • title`
  (branch omitted when not a git repo; title omitted until set; ` • plan`
  badge appended in plan mode — coddy-specific divergence); line 2 left
  `↑in ↓out  N.N%/262k` (arrows appear once non-zero), right
  `(provider) model[ • reasoning]`.
- Slash commands `[rev]` (explicit list): server-driven `/compact`, `/plugin`,
  `/<skill>`; client-side `/model`, `/mode`, `/resume`, `/new`, `/theme`,
  `/hotkeys`, `/quit`. pi's remaining 25+ commands are out of scope; the
  autocomplete two-column layout is identical.
- Session picker `[rev]`: simplified v1 — accent borders, title rows
  (`HandleSessionList` entries: title, relative time, id prefix), search
  input filtering by title substring. pi's scope/sort/rename/delete toggles
  are follow-ups; divergence documented.
- Keys: as v1 minus `!` bash mode `[rev]` (dropped from v1 scope: it would
  bypass `run_command` permission machinery; follow-up must route through the
  session tool path). shift+tab reasoning cycle validates against
  `cfg.FindModelEntry(model).ReasoningLevels` client-side and persists via
  `State.SetSelectedReasoning` `[rev]` (documented: no manager config-option
  API for reasoning exists).
- `@path` autocomplete `[rev]`: own walker capped at 50k entries skipping
  `.git`, `node_modules`, `.coddy` (mirrors
  `external/httpserver/workspace_files.go` constants; extracting a shared
  collector is a noted follow-up).
- MCP trust `[rev]`: `HandleSessionNew` already routes project servers
  through `TrustGate`; when a server needs approval the TUI shows a dim
  status hint naming `coddy mcp trust <name>` (no interactive approval UI in
  v1; documented).

## 5. Tests

### Unit (as v1, plus `[rev]`)

- Sanitizer hostile-input suite (OSC 52, title, cursor, mode switches).
- Terminal lifecycle restore-sequence tests for every exit path.
- Logger-isolation test (outputs forced to file even when YAML says stderr).
- Update-channel test: large replay with paused UI loop (no deadlock).
- Stale-update filtering; token accumulation; failed-plan glyph; tool title
  heuristic; ctrl+o disk read (missing file case); question custom answer.
- Dispatch tests in `cmd/coddy` `[rev]`: bare TTY → runCLI, bare non-TTY →
  usage, `coddy cli` non-TTY → clear error, `!cli` stub message.

### BDD (`features/cli_tui.feature` — written FIRST `[rev]`)

Scenarios (stub runner, fake terminal): the seven from v1 **plus** `[rev]`:
8. Replayed transcript renders user rows as user boxes (discriminator).
9. Question tool with custom answer: free-text editor path returns the typed
   answer.
10. Prompt error (non-busy) renders an error line and the editor recovers.
11. `/new` forgets the old live session and stale updates are dropped.
Model-switch scenario asserts through `HandleSessionSetConfigOption` `[rev]`.
Edge cases stay in unit tests per repo workflow.

### E2E (`examples/cli/`, live `neuraldeep/qwen3.8-27b`)

`[rev]` Contract fixed before scripts are written:
- Runner `examples/cli/test_cli.sh` (+ `examples/test_cli.sh` shim) builds
  with `TAGS="cli scheduler memory"` via `examples/build_coddy.sh` override.
- Config: **shared** `examples/config.demo.yaml` gains the `neuraldeep`
  provider (empty `api_key` → conventional `NEURALDEEP_API_KEY`) and the
  `neuraldeep/qwen3.8-27b` model row `[rev]` (additive; ACP/HTTP defaults
  unchanged). CLI runner default `MODEL=neuraldeep/qwen3.8-27b` (user
  requirement). If `NEURALDEEP_API_KEY` is unset and `~/.coddy/.env` defines
  it, the runner copies **that single line** into the temp
  `$CODDY_HOME/.env` `[rev]` (the mechanism config already loads; no global
  env sourcing).
- Python deps: `examples/cli/requirements.txt` (pexpect, pyte, pinned).
  Missing deps → runner **fails** with install instructions `[rev]` (no
  silent skip). Linux-only, stated in `examples/README.md`.
- Every script: temp `CODDY_HOME` + workdir, fixed 100×35 pty, `--theme
  dark --plain` `[rev]`, cleanup trap killing the child, unique session ids.
- Assertions: disk artifacts (session dirs, tool_calls, todos, plans,
  memory, scheduler files — same as ACP twins) are authoritative; screen
  greps only on deterministic chrome (`coddy v`, `Working...` `[rev]` not
  braille frames, borders, fixture tokens like `DEMO_SKILL_TOKEN`).
- Full set (user requirement) runs by default; `CLI_E2E_ONLY=<stem>` and
  `CLI_E2E_CORE=1` (smoke, models, permissions, resume, compact,
  toolcalls_persist) exist for iteration; runtime cost documented `[rev]`.
- `cli_e2e_permissions.py` forces determinism with a prompt that instructs
  an exact `run_command` invocation; if the model declines, the script
  retries once, then fails with the transcript `[rev]`.
- Parity matrix as v1 (12 twins + permissions + resume; REST-only surfaces
  n/a).

## 6. Docs

As v1 plus `[rev]`: `docs/build.md` tag table, release workflow notes,
`docs/assets/pi-tui-reference/README.md` pointer to `docs/cli.md` as the
console visual contract, `examples/README.md` python deps + Linux-only note,
PR screenshots: PNG renders of coddy's own TUI states (same
pexpect+pyte+chromium pipeline as the reference set — scripted in
`examples/cli/capture.py` `[rev]`) covering startup, chat+tool, permission
modal, plan widget, selectors, both themes.

No YAML config changes in v1. Rules files are untouched, so no rules sync is
triggered `[rev]`.

## 7. Milestones `[rev]` (reordered: spec first, vertical slice early)

1. **Spec + skeleton**: `features/cli_tui.feature` (all 11 scenarios),
   `external/cli/bdd_cli_tui_test.go` harness (red), `cmd/coddy` dispatch
   tests (red), `cli.go`/`cli_stub.go`, Makefile tags (test/lint/
   check-windows rows), `go get golang.org/x/term`. Everything compiles under
   every tag combo from day one.
2. **Vertical slice**: fake terminal + minimal renderer (full-frame, no diff
   yet) + minimal editor line + Sender + wiring: create session, submit
   prompt to stub runner, render streamed chunk, clean shutdown. BDD 1–2
   green. Terminal interface frozen here.
3. **Renderer parity**: line-diff, synchronized output, cursor marker,
   clear strategies, throttle; width/wrap/truncate utils; sanitizer;
   restoration lifecycle; unit suites green.
4. **Real terminal layer**: raw mode, StdinBuffer, keys, resize
   (`terminal_unix.go` / `terminal_windows.go` from the start), logger
   isolation; check-windows green.
5. **Widgets**: Editor (full), SelectList, Loader, Markdown, Box, borders;
   goldens against chrome fragments of the reference dumps.
6. **App features**: tool boxes (+disk expand), plan widget, thinking,
   footer, header, permission + question modals (incl. custom answer),
   selectors (model/mode/session/theme), client slash commands, replay,
   stale-update guard, token accumulation. BDD 3–11 green.
7. **Live e2e**: config.demo additions, driver, capture.py, 14 scripts,
   runner; run full suite against neuraldeep/qwen3.8-27b.
8. **Chrome pixel pass**: compare coddy states against reference chrome;
   capture coddy PNG set for the PR.
9. **Docs + full regression**: docs listed above; `make test`, `make lint`
   (with the new tagged pass), `make check-windows`, `make lint-windows`.

## 8. Resolved review items (log)

- x/term go.mod status corrected (cursor#1). PNGs are committed; finding
  about their absence was a false positive (cursor#2, verified via
  `git ls-files`).
- Footer/header/tool-title/preview-truncation corrected to match code and
  dumps (cursor#3–6, codex#7–9,11).
- Reasoning API reality documented (cursor#7, codex#30).
- Sender unification + no-premature-New + Ready-after-Load (cursor#13–15,
  codex#3–6).
- `!` bash mode deferred (cursor#16, codex#28).
- Logger forced to file (cursor#17, codex#31).
- Oracle narrowed to chrome (cursor#18, codex#43).
- Slash list + picker scope pinned; `/settings` explicitly out (cursor#19–20,
  codex#15).
- `/new` forget + stale updates (cursor#21, codex#25).
- Memory chunk updates (cursor#23, codex#8).
- MCP trust hint (cursor#24).
- `--plain` specified (cursor#25, codex#34).
- e2e config/env/skip policy fixed (cursor#27–28,33, codex#48–49).
- Matrix/lint/CI/release tags (cursor#29–31, codex#35–38).
- docs/build.md + reference README pointer (cursor#32, codex#39).
- Escape-injection sanitizer (codex#18).
- Restoration ownership (codex#19).
- TTY inside runCLI (codex#20).
- Scrollback-preserving shrink default (codex#22).
- OSC 11 input filter (codex#23).
- Deadlock-free channel contract (codex#24).
- User echo + discriminator (codex#7,26).
- Question custom answers (codex#10).
- Walk caps for `@` (codex#29).
- License/provenance (codex#33).
- Milestone reorder: spec-first + vertical slice (codex#53–57, cursor#51–55).
