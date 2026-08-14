# Plan: interactive console TUI (`coddy` with no arguments)

Status: draft for cross-review (codex + cursor), then implementation.
Owner: feat/cli-tui branch.

## 1. Goal

Add a fourth surface to coddy: an interactive terminal UI, visually replicating
the pi coding agent's TUI (pi-mono `b1efcf7d7`, v0.84.2) as closely as
practical ("pixel-perfect" for layout, borders, prefixes, spacing, spinner,
markdown styling), with colors taken from coddy's own SPA palette. Bare `coddy`
in a terminal starts it, so newcomers get a chat instead of a usage dump.

Reference captures: `docs/assets/pi-tui-reference/` (PNG + plain-text screen
dumps of every target state, taken from pi 0.84.2 against
`neuraldeep/qwen3.8-27b`).

Everything the TUI does must reuse existing coddy machinery: `session.Manager`,
`agent.NewAgent(...).Run`, `acp.UpdateSender` updates, `permission.Options`,
config options (`mode` / `model` / `permission_mode`), skills slash catalog,
`/compact` and `/plugin` built-ins, session store snapshots and replay. No new
agent-side behavior.

## 2. Placement and build wiring

Decision follows the repo's existing optional-surface pattern (http/gateway):

- New package **`external/cli`** behind **`//go:build cli`**, TUI framework in
  subpackage **`external/cli/tui`** (also `//go:build cli` on every file).
- **`cmd/coddy/cli.go`** (`//go:build cli`) + **`cmd/coddy/cli_stub.go`**
  (`//go:build !cli`) exposing `runCLI(args []string) error`. Stub returns
  "interactive console is not built in (rebuild with: go build -tags=cli)".
- Dispatch in `cmd/coddy/main.go`:
  - explicit subcommand `coddy cli [flags]` added to the switch and usage text;
  - bare `coddy` (no args): when stdin **and** stdout are terminals, run
    `runCLI(nil)`; otherwise keep today's behavior (usage to stderr, exit 1) so
    scripts and pipes never hang. In `!cli` builds bare `coddy` keeps printing
    usage (plus one hint line naming the `cli` tag) — the lean build contract
    from AGENTS.md stays intact.
  - TTY check uses `golang.org/x/term.IsTerminal` (promoted from go.sum;
    zero-download, no new third-party dependency).
- Import cycle break mirrors `httpserver.CommandDeps`: `external/cli.Run(args,
  cli.CommandDeps{EnsureHome, OpenStore})` receives the two `cmd/coddy` helpers
  it needs; everything else (`config`, `session`, `agent`, `acp`, `skills`,
  `permission`) is imported directly, same as the gateway does.
- Standard flags on the `cli` subcommand: `--config`, `--home`, `--cwd`,
  `--sessions-dir`, `--session-id` (reopen a session), `--resume` (open the
  session picker first), `--model`, `--mode agent|plan`,
  `--permission-mode ask|accept_edits|bypass`, `--theme dark|light|auto`,
  `--log-level/--log-file`, `config.SkillsAutoDiscoveryFlagName`,
  `config.ProjectTrustFlagName`, `--plain` (see e2e), `--no-alt-hints`
  omitted — keep flag set minimal, extend later.
- Logger: before `logger.New`, force output away from stderr (file under
  `<home>/logs/cli.log` unless `--log-file` given). Stderr writes would corrupt
  the inline TUI.
- Makefile: `TAGS="cli"` builds it; `make test` gains `go test -tags=cli ./...`
  and `go test -tags="cli scheduler memory" ./...` rows; `check-windows` gains
  the same two combos; recommended full build becomes
  `TAGS="http ui scheduler memory cli"` (Dockerfile, README,
  `examples/build_coddy.sh`).

Go dependency policy: **no new third-party modules.** Rendering is hand-rolled
ANSI (port of pi-tui's model). Promote to direct requires only:
`golang.org/x/term` (raw mode, size, IsTerminal), `github.com/rivo/uniseg`
(grapheme clusters), `github.com/mattn/go-runewidth` (cell widths) — all
already in go.sum/go.mod as indirects.

## 3. `external/cli/tui` — framework port (pi-tui main-screen subset)

Port pi-tui's **regular (inline) mode** only; alt-screen/fullscreen mode is out
of scope for v1. Byte-level conventions copied from pi:

- `Component` interface: `Render(width int) []string` (each line's visible
  width ≤ width), optional `HandleInput([]byte)` via type assertion,
  `Invalidate()`. `Container` concatenates children.
- Renderer (`mainscreen.go`): every frame wrapped in synchronized output
  `\x1b[?2026h … \x1b[?2026l`; per-line suffix `SEGMENT_RESET =
  "\x1b[0m\x1b]8;;\x07"`; cursor marker APC `"\x1b_pi:c\x07"` scanned in the
  bottom `rows` lines, stripped, hardware cursor positioned (hidden unless
  enabled); line-based diff: find first/last changed line, `\r` + `\x1b[2K` +
  rewrite only the changed range, relative `\x1b[nA/nB` movement, `\r\n` scroll
  at bottom; full clear `\x1b[2J\x1b[H\x1b[3J` on width change, height change,
  shrink (`clearOnShrink`), or change above the viewport; 16 ms render
  throttle, immediate render after keyboard input.
- Terminal (`terminal.go`): `x/term.MakeRaw` on stdin, bracketed paste
  `\x1b[?2004h/l`, `SIGWINCH` resize (Windows: poll size), modifyOtherKeys
  push `\x1b[>4;2m` on start / `\x1b[>4;0m` on stop (Kitty protocol
  negotiation deferred; parser still accepts CSI-u sequences so terminals in
  that mode work), cursor hide/show, title set. Windows: enable VT input and
  output modes via `internal/platform` console helpers (extend that package —
  it already owns Windows console handling).
- Input splitting (`stdinbuf.go`): port of pi's StdinBuffer — reassemble CSI /
  SS3 / OSC / DCS / APC / bracketed-paste bodies from chunked reads, lone-ESC
  timeout 10 ms (100 ms over SSH).
- Keys (`keys.go`): `MatchesKey(data, "ctrl+c")` covering letters, digits,
  arrows, home/end/pgup/pgdn, delete/backspace/tab/enter/escape, f-keys,
  ctrl/alt/shift modifiers in legacy, modifyOtherKeys (`\x1b[27;m;ku` and
  `\x1b[k;mu`) and Kitty (`CSI k;m u`) encodings.
- Width utils (`width.go`): `VisibleWidth` (ANSI-aware: CSI final in
  `m G K H J`, OSC, APC skipped; tab = 3 cols; grapheme clusters via uniseg;
  East-Asian wide = 2), `TruncateToWidth(text, w, ellipsis="...")`,
  `WrapTextWithANSI` (re-applies active SGR at continuation starts, trims line
  ends), `ApplyBackgroundToLine` (pad to width, wrap with bg).
- Widgets, each a byte-faithful port:
  - `Text` (paddingX/Y, optional bg fn; empty text → no lines);
  - `TruncatedText`, `Spacer` (empty strings), `Box` (left pad + bg);
  - `DynamicBorder` — `strings.Repeat("─", width)` through a color fn;
  - `Loader` — braille frames `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`, 80 ms, renders
    `["", " <frame> <message>"]`;
  - `SelectList` — `→ ` selected prefix, two-column label+description when
    width > 40 (primary col clamp 12..32 for slash menu), windowed scroll with
    dim `  (i/n)` info line, filter = prefix match;
  - `Editor` — multi-line, grapheme-aware wrapping with CJK break points,
    full-width `─` top/bottom borders (scrolled state: `─── ↑ N more ───…`),
    reverse-video fake cursor `\x1b[7m…\x1b[0m`, sticky visual column, history
    (cap 100, up on first line / down on last), kill ops (ctrl+w/u/k, alt+d),
    word moves, large-paste collapse to `[paste #N +K lines]` atomic markers,
    autocomplete hook (slash commands + `@` file paths via filesystem walk),
    max visible lines `max(5, rows*3/10)` with scroll borders. Undo/yank-pop
    deferred (documented gap).
  - `Markdown` — headings (h1 bold+underline+color, h2 bold+color, h3+ `### `
    prefix), bold/italic/strike, inline code, fenced blocks with
    `` ```lang `` border lines + 2-space indent, blockquote `│ ` prefix,
    `─`×min(width,80) hr, lists (`- ` / `N. ` / `[x] `, 4-space nesting),
    box-drawing tables, OSC 8 hyperlinks when the terminal supports them.
    Parser: hand-rolled block/inline scanner sufficient for the above (no new
    dependency); LaTeX and mermaid out of scope.
- Theme (`theme.go`): pi's token roles (`accent border borderAccent
  borderMuted success error warning muted dim text thinkingText selectedBg
  userMessageBg toolPendingBg toolSuccessBg toolErrorBg toolTitle toolOutput
  md* syntax* thinking* bashMode`), truecolor `\x1b[38;2;r;g;bm` with 256-color
  quantization fallback (6×6×6 cube + gray ramp) when `COLORTERM` absent;
  `Fg()` resets with `\x1b[39m`, `Bg()` with `\x1b[49m`. Bold/italic/underline
  emitted directly.
- Syntax highlighting in code fences: v1 ships a small keyword/string/comment
  highlighter for go/python/js/ts/json/yaml/bash and falls back to plain
  `mdCodeBlock` color otherwise (pi uses highlight.js; a Go port of that is
  out of scope — documented divergence).

### Coddy color scheme (dark / light)

Structure and roles stay pi's; values come from coddy's SPA tokens
(`external/ui/src/styles.css` default theme) with tool/status tints kept close
to pi where coddy has no equivalent:

| Role | dark | light | source |
|------|------|-------|--------|
| accent | `#9333ea` | `#7c3aed` | `--accent` |
| border | `#a585c9` | `#7c3aed` | `--coddy-hero-accent` |
| borderAccent | `#c4b5fd` | `#6d28d9` | context ring / link-action |
| borderMuted | `#3f3f46` | `#d4d4d8` | zinc scale |
| text | `#ffffff` | `#18181b` | `--text` |
| muted | `#9ca3af` | `#52525b` | `--muted` |
| dim | `#6b7280` | `#71717a` | zinc scale |
| success | `#3fb950` | `#2ea043` | diff-add |
| error | `#f85149` | `#cf222e` | diff-del |
| warning | `#d29922` | `#9a6700` | GitHub-ish amber |
| selectedBg | `#2d2d2d` | `#e4e4e7` | `--bubble-user` |
| userMessageBg | `#2d2d2d` | `#e4e4e7` | `--bubble-user` |
| toolPendingBg | `#26222e` | `#ececf4` | violet-tinted pi analog |
| toolSuccessBg | `#22301f`→keep pi `#283228`-style tint | `#e8f0e8` | pi analog |
| toolErrorBg | `#3c2828` | `#f0e8e8` | pi values |
| mdHeading | `#c4b5fd` | `#6d28d9` | violet scale |
| mdLink | `#a78bfa` | `#7c3aed` | `--coddy-link` |
| mdCode | `#9333ea`-tinted `#c4a7e7` | `#6d28d9` | accent family |
| thinkingText | `#9ca3af` | `#52525b` | muted |
| bashMode | `#3fb950` | `#2ea043` | success |

`--theme auto` (default): OSC 11 background query with 500 ms timeout, fall
back to `COLORFGBG`, else dark. Exact final values live in
`external/cli/theme_dark.go` / `theme_light.go` and get tuned against the
reference PNGs during implementation.

## 4. `external/cli` — the interactive app

Wiring (copies `cmd/coddy/gateway.go` + ACP main):

```go
store   := deps.OpenStore(sessionsRoot, cfg)
sender  := cliapp.NewSender(app)           // implements acp.UpdateSender
runner  := func(ctx, st, prompt, snd) { return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt) }
mgr     := session.NewManager(cfg, sender, runner, log, paths.CWD, store)
res, _  := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: paths.CWD})
mgr.HandleSessionReady(res.SessionID)      // slash catalog + deferred replay
```

The TUI sender is the **manager's** sender, so mode/config/slash-catalog
updates and `session/load` transcript replay arrive without extra plumbing.
Prompts go through `mgr.HandleSessionPromptWithSender(ctx, params, sender,
nil)` on a worker goroutine; `session.ErrSessionTurnBusy` renders a status
line. All sender callbacks are serialized into the UI goroutine via a channel
(updates may arrive from the turn goroutine).

Update rendering map (struct → visual):

| Update | Rendering |
|---|---|
| `MessageChunkUpdate` text | streaming assistant `Markdown` (grow in place) |
| `MessageChunkUpdate` reasoning | italic `thinkingText` block, collapsible with ctrl+t ("Thinking..." when collapsed) |
| `ToolCallUpdate` (pending) | `Spacer` + Box `toolPendingBg`: bold tool title line (`read path`, `$ command`, etc.) |
| `ToolCallStatusUpdate` in_progress | same box, args preview |
| completed / failed | box bg → `toolSuccessBg` / `toolErrorBg`, preview from `coddy.toolResultPreview` meta, collapsed to 10 lines + `... (N more lines, ctrl+o to expand)` |
| cancelled | box bg error + `cancelled` note |
| `PlanUpdate` | todo widget above editor: `✓`/`◐`/`○` list, dim completed (pi plan-mode style) |
| `TokenUsageUpdate` / `UsageUpdate` | footer stats (`↑in ↓out` and `N.N%/262k`) |
| `ModeUpdate` / `ConfigOptionUpdate` | footer right segment + selector state |
| `AvailableCommandsUpdate` | slash autocomplete catalog |
| `MemoryPhaseUpdate` (memory tag) | dim status line while recall/persist runs |

Layout (top→bottom, pi structure): header (logo `coddy v<version>` bold accent
+ dim version; compact hint line; ctrl+o expands full hints + `[Context]`
instruction files, `[Skills]` names, `[Rules]` count, `[MCP]` servers) →
chat container → pending/status (spinner `⠴ Working...`, esc hint) → plan
widget → editor (bordered) → footer (line 1: dim cwd `(git-branch)` •
session title; line 2: `↑tok ↓tok  N.N%/ctx (auto)` left, `(provider) model •
mode[ • reasoning]` right).

Modals (replace editor input focus, pi-style): permission request
(`RequestPermission` → SelectList of `params.Options` under an accent
`DynamicBorder`, tool title + `PromptBody` above; honors bypass short-circuit
like `serverRef`), question tool (`RequestQuestion` → one SelectList per
question, multi-select via space), model selector (ctrl+l or `/model`),
session picker (`/resume` or `--resume`; rows from
`store.ListSnapshots(cwd, false)`: title, relative time, id prefix; enter →
`HandleSessionLoad` + transcript replay), mode picker (`/mode`), theme picker
(`/theme`).

Client-side slash commands (dispatched before `HandleSessionPrompt`, pi
parity): `/model`, `/mode`, `/resume`, `/new`, `/theme`, `/hotkeys`, `/quit`.
Server-driven: `/compact`, `/plugin`, `/<skill>` pass through as prompt text
(the agent intercepts). The autocomplete list merges both sets.

Key bindings (fixed v1, pi defaults): enter submit, shift+enter/ctrl+j
newline, escape interrupt (→ `HandleSessionCancel`), ctrl+c clear then exit,
ctrl+d exit on empty, ctrl+l model selector, ctrl+p / ctrl+shift+p cycle
model, shift+tab cycle reasoning level (only when the model declares
`reasoning_levels`), ctrl+o expand last tool output + header hints, ctrl+t
toggle thinking, up/down history at edges, `!cmd` bash mode (local shell run,
streamed into a `bashMode`-bordered block; output is NOT sent to the model in
v1), `@path` autocomplete inserts file mentions (hydrated by the manager).

## 5. Tests

### Unit (tag `cli`, standard `go test`)

`external/cli/tui`: renderer diff decisions (fake terminal capturing writes —
port of pi's virtual-terminal idea with a plain write recorder + expected
escape sequences), width/wrap/truncate tables (CJK, emoji, ANSI), editor ops
(insert, wrap, history, kill ops, paste markers), keys matrix, markdown golden
lines (stripped and styled), select-list windows, theme fg/bg emission and 256
fallback. `external/cli`: sender update → component state transitions with a
stub screen; footer formatting; slash dispatch; permission modal option
plumbing. Test names follow repo sentence style.

### BDD happy path (`features/cli_tui.feature`, harness `external/cli/bdd_cli_tui_test.go`)

Stub-runner scenarios (no LLM, in-memory fake terminal 100×35):

1. Bare start renders header, editor borders, footer with default model id.
2. Submitted prompt renders user block, streamed chunks render markdown,
   footer shows token usage.
3. Tool call renders pending→success box with preview and expand hint.
4. Permission mode ask: run_command asks, arrow+enter allows, turn continues
   (uses `permission.Options` path).
5. Escape mid-turn cancels: `HandleSessionCancel` fires, status shows
   interrupt, editor usable again.
6. `/model` switch updates footer and `SetSelectedModelID` persists.
7. Resume: second start with `--session-id` replays transcript rows.

Harness: `//go:build cli`, `Paths: ../../features/cli_tui.feature`,
`Strict: true`, stub `AgentRunner` emitting scripted updates (same trick as
httpserver BDD), fake `Terminal` interface implementation.

### E2E (`examples/cli/`, live `neuraldeep/qwen3.8-27b`)

New runner `examples/test_cli.sh` → `examples/cli/test_cli.sh`. Python driver
`examples/cli/cli_tui_driver.py`: pexpect (pty) + pyte (screen emulation),
`wait_for_text` / `wait_quiet` / `send_keys` / screen dumps on failure —
the same pipeline already used to capture the reference PNGs. New dev-only
Python deps (pexpect, pyte) documented in `examples/README.md`; runner
bootstraps a `.venv` like other example scripts and **skips gracefully**
when they are missing. Model comes from `MODEL` env (default
`neuraldeep/qwen3.8-27b`); config `examples/cli/config.demo.yaml` defines the
`neuraldeep` provider with empty `api_key` so the conventional
`NEURALDEEP_API_KEY` env var resolves it (`config.EffectiveAPIKey`); the
runner sources `~/.coddy/.env` when the var is unset. Assertions favor on-disk
session artifacts (identical to ACP twins) plus screen greps.

Feature parity matrix (ACP + HTTP e2e set → CLI):

| Existing script(s) | CLI twin | Notes |
|---|---|---|
| acp_smoke_gateway / http_smoke_gateway | `cli_smoke.py` | boot, header, prompt, reply text on screen, session dir created, clean exit |
| acp/http_e2e_models | `cli_e2e_models.py` | ctrl+l selector lists YAML models; switch; footer + `session.json` reflect it |
| acp/http_e2e_web | `cli_e2e_web.py` | websearch+webfetch tool boxes; tool_calls/*/meta.json |
| acp/http_e2e_todo | `cli_e2e_todo.py` | plan widget visible; todo backlog drains (disk todos) |
| acp/http_e2e_skills_slash | `cli_e2e_skills_slash.py` | fixture skill in slash menu; DEMO token in reply |
| acp/http_e2e_rules | `cli_e2e_rules.py` | glob + mention rule tokens |
| acp/http_e2e_memory | `cli_e2e_memory.py` | tags include `memory`; recall + persist |
| acp/http_e2e_background | `cli_e2e_background.py` | background task meta/output on disk; tool boxes |
| acp/http_e2e_toolcalls_persist | `cli_e2e_toolcalls_persist.py` | args.json/result.md/meta.json |
| acp/http_e2e_compact | `cli_e2e_compact.py` | `/compact` → compaction_summary row + confirmation |
| acp/http_e2e_plan_files | `cli_e2e_plan_files.py` | plan mode via `/mode`, plan file marker, run plan |
| acp/http_e2e_scheduler_agent | `cli_e2e_scheduler_agent.py` | scheduler tools through CLI prompt (tags scheduler) |
| — (CLI-unique) | `cli_e2e_permissions.py` | `--permission-mode ask`: modal appears, allow via keys, tool runs |
| — (CLI-unique) | `cli_e2e_resume.py` | exchange, quit, relaunch `--resume`, transcript replayed |
| http_e2e_scheduler_api | n/a | REST CRUD surface, no CLI equivalent (curl-only) |
| http_e2e_remote | n/a | remote auth REST surface |
| http_e2e_background_reap | n/a | kill -9 recovery already covered by ACP-less harness; CLI adds nothing |
| composer watch / workspace REST / mcp REST | n/a | HTTP-only surfaces |

### Regression

`make test` (now including the two `cli` rows) and `make lint` green;
`make check-windows` + `make lint-windows` because `internal/platform` gains
console helpers and new tagged files exist.

## 6. Docs and follow-ups

- `docs/cli.md` — user guide + visual contract (the TUI equivalent of
  DESIGN.md sections): layout, key map, theming, reference gallery links.
- README: quick-start section, build tags table row.
- `AGENTS.md`: `external/cli` row in the repo map + build notes.
- `docs/architecture.md`: fourth client box.
- `examples/README.md`: cli harness section + python deps.
- No YAML config schema changes in v1 (flags/env only) → no
  `config.schema.json` sync needed. If a `cli:` config block appears later it
  follows workflow step 7.
- CHANGELOG entry if repo keeps one (it does not — skip).

## 7. Milestones (implementation order, red→green each)

1. `external/cli/tui`: width utils + theme + Text/Spacer/Container + renderer
   with fake-terminal unit tests (red first).
2. Terminal raw mode + stdin buffer + keys.
3. Editor + SelectList + Loader + Markdown (+DynamicBorder, Box).
4. `external/cli` app shell: wiring, header, editor submit → stub runner
   render loop; `features/cli_tui.feature` scenarios 1–2 green.
5. Sender: chunks, tool boxes, plan widget, footer; scenarios 3, 5 green.
6. Permission + question modals (scenario 4), model/mode/session selectors
   (scenarios 6–7), client slash commands, bash mode.
7. cmd wiring (`cli.go`/stub, bare-`coddy` TTY check), Makefile matrix,
   Windows console helpers, check-windows clean.
8. Pixel pass: compare against `docs/assets/pi-tui-reference/*.txt` states;
   fix spacing/prefix/border divergences.
9. examples/cli e2e suite against neuraldeep/qwen3.8-27b; wire runner.
10. Docs; `make test`; `make lint`; screenshots of the new surface for the PR
    (captured with the same pexpect+pyte pipeline).

## 8. Risks / open questions for reviewers

1. **Scope of editor internals**: undo stack, kill-ring yank/pop, jump-mode
   are deferred; is that acceptable for "pixel parity" (visual yes,
   behavioral partial)?
2. **Kitty keyboard protocol**: v1 ships legacy + modifyOtherKeys parsing
   only; shift+enter falls back to ctrl+j on terminals that cannot report it.
   Acceptable?
3. **Syntax highlighting**: keyword-level highlighter vs pi's highlight.js.
   Visual difference inside code fences.
4. **`!` bash mode**: local-only execution, output not fed to the model
   (pi feeds it as context). Follow-up could append it to the next prompt.
5. **Bare `coddy` TTY heuristic**: usage-on-pipe keeps CI/scripts safe, but
   `coddy < file` in a terminal still starts nothing — fine?
6. **Windows**: compile-verified + platform console helpers, but no CI
   runtime test for the TUI itself (existing windows-latest job covers
   platform/bgtask/shell only). Accept as v1 constraint?
7. **e2e runtime cost**: 14 live-model scripts × qwen3.8-27b. Runner supports
   `CLI_E2E_ONLY=<stem>` filtering like a dev knob. Accept full set in the
   default runner, or trim to a core subset by default?
