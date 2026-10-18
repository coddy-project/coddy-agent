# Operator commands: a command the agent may not run, run by the operator

Status: plan, reviewed (round 1: approve with changes, folded in below). Branch
`feat/operator-command`.

## Problem

A shell command the agent needs is sometimes refused to it by a protection rule: the decisions
safety check (`internal/agent/decisions.go:54-59`, "command rejected as unsafe ...; ask the operator
or use a safer alternative"), a `PreToolUse` hook (`blocked by hook: <reason>`), a toolset without
`run_command`, or a surface that rejects every prompt. Today the model can only print the command
and ask the operator to copy it into a terminal, and whatever the operator sees there never reaches
the session: the agent cannot read the output and continue.

The console already has an operator shell, `!!` (`external/cli/localshell.go`,
`internal/tools/shell/operator.go`), but by design it is invisible to the agent and exists only in
the local console.

## How Claude does it (reference)

- Claude Code's `!` prefix runs a command typed by the user in the user's shell, outside the
  permission rules, and adds the output to the conversation, so the model reads it on the next
  turn. The shell is a setting (Git Bash or PowerShell on Windows).
- The Claude desktop app puts a Run button on shell code blocks the model wrote; the command runs in
  the session's working directory and the output returns to the conversation. There the user may
  edit the command before running it.

Coddy takes the shape of the second (the model proposes, the operator presses Run, the output
returns) with two differences the operator asked for: the call is a **tool**, so the turn waits for
the result and continues in the same turn, and **the operator cannot edit the command**: they run
exactly what is shown, or decline.

## Acceptance criteria (from the request)

1. The shell an operator command runs in is a setting with a default: PowerShell on Windows, bash on
   Linux, macOS and Android; the operator can replace it with another shell.
2. When a forbidden action has to be done by the operator, the surface shows the console call - the
   command, the shell, the folder - with a **Run** button, and once run, the command's output.
3. The output is the tool result: the session reads it and the turn continues.
4. The tool is available in the console TUI (local and `--remote`) and in the web UI.
5. The operator cannot edit the call.
6. Works on Linux, Windows, macOS and Android.

## Design

### The tool: `operator_command`

```json
{
  "command": "journalctl -u nginx --since today",   // required, run exactly as written
  "reason":  "reading the system journal was refused to run_command by the unsafe-command check",  // required
  "cwd":     "optional folder under the session workspace"
}
```

- `internal/tools/shell/operator_tool.go` beside `operator.go`. `RequiresPermission: false`: the
  operator's own Run click is the gate. Its only reader is `react.go:2234`, so the tool never meets
  `permission.AutoApproves`, the allowlist or `bypass` mode; a test pins that in `bypass` with an
  allow-all list the tool still waits for the click.
- The description names the operator shell kind the setting resolves to (`bash`, `pwsh`, `zsh`...),
  so the model writes for that syntax; it changes only when the configuration does. It says when to
  use the tool (a command refused to `run_command` by a policy, or one that needs the operator's own
  account), never as a substitute for an allowed `run_command`, one command per call, no TTY and no
  stdin (`sudo -n`, not `sudo`), and that a decline is final.
- Validation before anything is shown: the command must be non-empty and free of control characters
  other than `\n` and `\t`, of bidirectional controls (U+202A-202E, U+2066-2069, U+200E/F,
  U+061C) and of zero-width characters (U+200B-200D, U+2060, U+FEFF); such a command is refused with
  an error naming the character, so what the operator reads is byte for byte what the shell gets.
  `cwd` resolves under the session workspace like `run_command`'s, then to an absolute path with
  links resolved, which is what the card shows.
- Result text:
  - run: `The operator ran the command in <shell> on <host> (exit code N):\n<output>`, through the
    `tools.output_limits` entry of the tool (default the 500 lines of `run_command`);
  - stopped: `The operator stopped the command (exit code N). Output so far:\n...`;
  - declined: `The operator declined to run the command.` plus the operator's optional note;
  - repeated after a decline in the same turn: refused without asking again,
    `The operator already declined this command in this turn.`;
  - no operator: an error, `operator_command needs an operator at the web UI or the console`.
- Offered in `agent` and `plan`; **not in Ask mode** (which has no shell at all: the tool would
  become its only shell and turn every read into a Run prompt). Excluded for subagents and scheduled
  runs (`subagentMandatoryExclusions`, `internal/agent/subagent.go:91`) and absent from
  `access.NonAdminTurn().AllowedTools`. Not offered in a supervised goal run (`turnWatch` does not
  forward the capability: such a run is meant to go on without a person).
- Refusal hints: the decisions "rejected as unsafe" refusal and the toolset refusal of
  `run_command` gain `If the operator has to run it, call operator_command.`, only when the tool is
  offered on this turn. No hint for a hook block (a hook is the operator's own policy, and hooks
  that match `Bash`/`run_command` do not match the new tool) and none for `permission denied by
  user`.

### Who runs the command, and where

The process that owns the session runs the command, in the session's workspace (or `cwd` under it),
with the configured operator shell, through the process handling of `!!`
(`shell.StartOperatorCommandForShell`: detached process group, `platform.AdaptCommand` for Termux,
the PowerShell UTF-8 wrapper, decoded and sanitised output, 256 KiB tail), with two additions for
the tool:

- Unix: the child gets its own session (`Setsid`, not only `Setpgid`), so it has no controlling
  terminal: a program that wants a TTY fails at once instead of drawing over the console or
  stopping on `SIGTTIN`. Stdin stays closed.
- Windows with `cmd` as the operator shell: `SysProcAttr.CmdLine` carries
  `cmd.exe /d /s /c "<command>"` verbatim, because Go's argument quoting mangles `"` for cmd.

For the web UI and `coddy --remote` that process is the `coddy serve` host, which is also where the
session's files are; the browser never executes anything, and the card names the host. The command
runs as the same OS user as Coddy: it bypasses Coddy's rules, not the operating system's.

### The setting: `tools.operator_command`

```yaml
tools:
  operator_command:
    enable: true      # false: the tool is never offered
    shell: ""         # empty: PowerShell on Windows (pwsh, then powershell), bash elsewhere (bash, then sh)
```

- `shell` is a name on `PATH` or a path: `bash`, `zsh`, `fish`, `sh`, `pwsh`, `powershell`, `cmd`,
  `C:\Program Files\Git\bin\bash.exe`, `/data/data/com.termux/files/usr/bin/zsh`. The kind, hence
  the argv, comes from the executable's base name, case-insensitive and without `.exe`
  (`platform.ResolveShell(spec, goos, lookup)`, done): `pwsh`/`powershell` get the existing
  `-NoProfile -Command` wrapper, `cmd` gets `/c`, anything else `-c`. A spec that does not resolve
  is a `--dry-run` error (a `PATH` lookup depends on the host, so `coddy -t` does not judge it) and
  at run time the tool's error, never a fallback to another shell.
- The console's `!!` uses the same setting, so the operator has one shell for everything they run
  by hand. `run_command` keeps `platform.DetectShell`: its shell is in the cached system prompt.
- Settings UI: a text field under Tools with the detected default as placeholder; schema,
  `UISchemaMap`, `jsondto.go`, `config.example.yaml`, the `configure-coddy` skill, ru descriptions,
  `make site-schema` (an optional new key: safe to publish at once).

### The protocol

An optional interface in `internal/acp`, so a surface that does not implement it is refused by
default:

```go
type OperatorCommandSender interface {
    // OperatorCommandsAvailable reports whether a person can answer on this
    // surface during this turn: the local console, or a web UI or remote
    // console that declared the capability on its prompt request.
    OperatorCommandsAvailable() bool
    // RequestOperatorCommand shows the call and blocks until the operator
    // decides. A "run" decision carries the Stop channel of that request,
    // which stays open until the tool reports the command ended.
    RequestOperatorCommand(ctx, OperatorCommandRequest) (*OperatorCommandDecision, error)
}
```

- `OperatorCommandRequest{SessionID, RequestID, ToolCallID, Command, Reason, CWD, Shell, ShellPath,
  Host}`; `RequestID` is `oc_` plus 128 random bits.
- `OperatorCommandDecision{Action: run|decline, Note, Stop <-chan struct{}, Done func()}`: after
  `run` the hub entry stays registered in the state `running`, a `stop` answer closes `Stop`, and
  the tool calls `Done` when the command ended, which removes the entry. States: `waiting` →
  `running` → `ended`, or `waiting` → `declined`; turn cancel from any state ends it.
- The answer never carries the command: a surface only says run, decline or stop for a request id,
  and the tool runs the command it holds.
- `acp.OperatorCommandsOf(sender)` finds the interface through wrappers. Every wrapper in the tree
  decides explicitly and is listed in a test that walks the real chain:
  - forwards: `mirroredSender` (to `primary`, `external/httpserver/turn_mirror.go`),
    `lateBoundSender` (`external/cli/run.go:448`), the manager's turn sender, the wake sender of
    `cmd/coddy/acp_wake.go` only if its inner sender has a person (it does not: deny);
  - denies: `turnWatch` (supervised goal runs), the subagent relay, the goal verifier, the scheduler
    and serve default senders, `coddy -p`, the ACP server, Telegram, Pachca, the plans sender.

Streaming after `run`: `tool_call_update` frames, `status: in_progress`, on every state change and
at most every 500 ms while the output changed, each with `_meta.coddy.operatorCommand
{requestId, phase: running|ended|stopped|declined, shell, host, cwd, exitCode, droppedBytes}` and
a small window of the output (the last 40 lines, at most 4 KiB), so a follower that joins mid-run
is current after one frame and the composer relay's 512 KiB buffer
(`composer_stream_relay.go:16`) is never drained by one command. The final frame is the normal tool
result with the whole limited output. Turn cancel terminates the process group (grace 3 s) and the
call ends `cancelled`.

### Surfaces

- **HTTP** (`external/httpserver`): the SPA and the remote console set
  `metadata.coddy_operator_commands: "1"` on `POST /v1/responses`; the `Sender` reports the
  capability only for an interactive stream that carried it, so an OpenAI-compatible client or an
  older `coddy --remote` is never offered the tool. SSE `event: operator_command` with the request;
  `POST /coddy/sessions/{id}/operator-command` body `{requestId, action: run|decline|stop, note?}`,
  strict decoding (an unknown field such as `command` is a 400), 204, 404 when no such request
  exists, 409 when the action does not fit the state (a second `run`, `stop` before `run`, `run`
  after `decline`). Hub like `questionhub.go`, first answer wins across followers of the composer
  relay. The waiting and running state is published on `/coddy/events` the way
  `publishQuestionPending` does, so History and other tabs show it. `openapi.go`,
  `docs/reference/http-api.md`.
- **Web UI** (`external/ui/src/ui`): SSE `operator_command` creates an `operator_command_prompt`
  item bound to the tool call (`consumeComposerSse.ts`, `App.tsx`, a store in sessionStorage like
  `questionPromptSessionStore.ts`, so a reload while waiting shows it again). `OperatorCommandCard`:
  header "Run it yourself", shell, host and folder, the agent's reason labelled as such, the whole
  command in the read-only `ShellPreview` block (wrapped, never truncated, copy allowed, no input,
  no contenteditable), buttons **Run** and **Decline**, neither focused on arrival; while running,
  the output window with **Stop**; then exit code and output, and the card stays as the tool call's
  record in History. `DESIGN.md` section "Operator command card"; i18n keys in `en` and `ru`.
- **Console TUI** (`external/cli`): `sender.go` implements the interface; a gate modal like
  `permissionModal` (`modals.go:24`): header "Run this command yourself?", shell and folder, the
  agent's reason, the whole command wrapped (rendered text, not an editor). The selection starts on
  **Decline**; Run is the second row (or `r`), esc declines, so an Enter typed before the modal
  appeared never runs anything. After Run the tool's box (`toolBox`, `chat.go:108`) shows the
  streamed output; esc while it runs sends stop (the turn continues), a second esc interrupts the
  turn, as `!!` does.
- **Remote console** (`internal/remote/prompt.go`): declares the capability, `case
  "operator_command"` hands the request to the local modal and posts the decision; esc posts stop.
  The modal names the server host the command runs on.
- **Not offered**: ACP editors, Telegram and Pachca, `coddy -p`, the scheduler, the serve default
  sender, the goal verifier, supervised goal runs, subagents.

### Prompt cache

The tool list depends on the surface: a session run from the web and then from Telegram sends a
different `tools` array. Web, console and remote console all offer it, so the common case is
unchanged; `make test-cache` must stay green, and a `TestPromptCache*` case pins that two turns on
the same surface keep the same tools array. The refusal hint changes a tool result, which is
history, not the prefix.

## Safety

- Default-deny: only senders that implement the interface and report a person can request a run.
- The command shown is the command run: the server holds it, the answer carries only an action, and
  a command with characters that would display differently is refused.
- No auto-run in any mode; no "always allow"; no remembered grant; a decline holds for the turn.
- Hooks still apply: a `PreToolUse` hook may block `operator_command` itself.
- Run is never the default control.
- The reason and the command are escaped as text on every surface; ANSI sequences in output are
  stripped (`SanitizeOutput`).

## Tests (red first)

- `features/operator_command.feature`, harness in `internal/agent` over a real `session.Manager`: a
  hook blocks `run_command`, the model calls `operator_command`, the operator presses Run (or
  Decline), the next model request carries the output (or the decline), and only a surface with a
  person is offered the tool.
- `features/web_operator_command.feature`, harness in `external/httpserver`: the capability flag,
  SSE `operator_command`, `POST .../operator-command {run}`, streamed frames, the final result.
- Console: a `cli` harness scenario (modal starts on Decline, Run, output in the tool box).
- Unit: `platform.ResolveShell` per goos (done); validation of control, bidi and zero-width
  characters; decline; decline remembered for the turn; stop; turn cancel kills the group; second
  run 409; stop before run 409; a body with `command` 400; no capability flag → not offered; the
  wrapper chain test; subagent and non-admin exclusion; bypass + allow-all still waits; `Setsid`
  (a child has no controlling terminal); `cmd` CmdLine on Windows; schema/struct parity; `!!` uses
  the configured shell; `--dry-run` reports an unknown shell.
- Vitest: the card has no editable element and no autofocus; Run, Decline and Stop post the right
  body; a reload restores the waiting card; i18n parity.
- Windows: `internal/tools/shell` and `internal/platform` run on the Windows runner (PowerShell
  default); Android: `make check-android`.

## Model check

A Promela model of one request: two followers answering (run, decline, stop, repeated), the command
exit, a turn cancel and a client reload, with the properties: the command starts at most once and
only after `run`; `stop` has effect only on a running command; the tool call ends exactly once and
always ends; after a cancel no process is left. A Petri net of the same for the token view. Results
under `docs/plans/operator-commands-models/`.

## Documentation

`docs/features/operator-commands.md` (+ `docs/ru/...`), both `nav.yaml`, `docs/reference/tools.md`
row, `docs/surfaces/console.md` (`!!` uses the setting; the modal), `docs/surfaces/web-ui.md`,
`docs/reference/http-api.md`, `docs/reference/config.md` via `make docs`, `DESIGN.md`, screenshots
of the card (waiting, running, done) and the TUI modal.

## Review round 1 (decisions)

Three reviewers (claude opus, gpt-6.1-sol, gemma-4-31b), all answered. Taken: capability declared by
the client instead of "any stream" (an OpenAI client would hang the turn), a Stop channel that
outlives the decision, an explicit forward-or-deny for every sender wrapper, refusal of commands
that display differently from what runs, `Setsid` and no TTY, the shell named in the tool
description, small frames with `requestId` and phase plus `/coddy/events`, Decline as the default in
the TUI and no focus on the web, no hint after a hook block, a decline remembered for the turn,
`cmd` CmdLine, the host on the card, the shell lookup in `--dry-run`. Answers to the open questions:
Ask mode does not offer the tool; no default focus on Run; closed stdin for this version; the hint
only for policy refusals; `run_command` keeps its own shell. Rejected with evidence: a non-admin
follower pressing Run (one auth tier; Mini App sign-in is admin-only), two prompts racing in one
batch (tool calls run one after another), persisted running frames (progress is not persisted).
Supervised goal runs: not offered (decided here).
