# ACP provider: a local ACP agent as a model

Status: implemented on `feat/acp-provider`. Reference: `acpbox` (an OpenAI-compatible gateway in front of
one ACP agent process). The plan was cross-reviewed before implementation (three reviewers, verdict
"needs rework"); the design below is the one after that review, and the last section lists what the
review changed.

## Goal

A provider row of a new type, `acp`, runs a local [Agent Client Protocol](https://agentclientprotocol.com)
agent - `claude-agent-acp`, `codex-acp`, `opencode acp`, `coddy acp`, `zcode-acp-server` or any other
program that speaks ACP on stdio - and serves it as a model of Coddy: a `models[]` row names it, a
session runs on it, every surface shows its answer streaming.

```yaml
providers:
  - name: claude
    type: acp
    acp:
      command: claude-agent-acp
      modes: {plan: plan, ask: plan}
models:
  - model: claude/default
```

The external agent is a whole agent, not a model: it brings its own system prompt, its own tools and
its own sandbox, runs them in the session's workspace, and answers with text. Coddy's loop sees a
model that never calls a Coddy tool: one provider call per user turn, the answer ends the turn.

Probed on the workstation (2026-10-19) with a one-line prompt; each agent answered:

| Agent | Command | Auth | Models | Modes |
|---|---|---|---|---|
| Claude | `claude-agent-acp` 0.93 | the Claude CLI login | `configOptions` (model) | `default`, `acceptEdits`, `plan`, `auto`, `bypassPermissions` |
| Codex | `codex-acp` 2.2 | `authenticate` `api-key` (OPENAI_API_KEY) or `chat-gpt`; `session/new` answers -32000 without it | `models` and `configOptions` | `read-only`, `workspace-write`, `agent`, `agent-full-access` |
| OpenCode | `opencode acp` 1.19 | its own config | `configOptions` (model) | (config option) |
| Coddy | `coddy acp` | its own config | `configOptions` (model, values `provider/model`) | `agent`, `plan`, `ask` |
| ZCode | `zcode-acp-server` 0.72 over `zcode app-server` | its own provider file | `configOptions` (values like `neuraldeep\qwen3.8-27b`) | `plan`, `build`, `edit`, `guarded`, `yolo` (default) |

## Configuration

```yaml
providers:
  - name: claude
    type: acp
    proxy: ""                 # empty = inherit; none / a URL go to the agent's environment
    timeout_ms: 0             # when positive, bounds each prompt
    acp:
      command: claude-agent-acp   # required: program, PATH lookup
      args: []
      env: {}                     # added to the inherited environment
      cwd: ""                     # fixed folder for the process and every session; default: the session's workspace
      auth_method: ""             # ACP authenticate methodId called after initialize (codex: api-key)
      permissions: ask            # ask | allow | deny: the agent's session/request_permission
      modes:                      # ACP mode per Coddy mode; plan and ask must be set to use those modes
        agent: ""
        plan: ""
        ask: ""
      idle_timeout_ms: 0          # stop the process this long after its last call; 0 = 60 minutes
models:
  - model: claude/default         # "default": the agent's own model; anything else selects that model id
```

- `config.ProviderConfig.ACP *ACPProviderConfig` (`yaml:"acp"`, json tags of its own so `ProviderJSON` keeps
  converting directly); `AllowedLLMProviderTypes` gains `acp`; `Validate` requires `acp.command`, checks
  `permissions` and `idle_timeout_ms`, and requires `acp.cwd` to be absolute (or `~`, or `${CWD}`).
- `TakesAPIKey` is false for `acp`; `api_base` and `api_key` are ignored and hidden in the form.
- Values were environment-expanded by the loader; `${CWD}` stays a placeholder and is the session's
  workspace (the agent is then started once per workspace, the MCP pool's `WorkspaceBound` rule).
- `ResolvedLLM.ACP` and `llm.ProviderInput.ACP` carry the block (llm already imports config). Sites that build
  a `ProviderInput`: `agent.llmProviderInput`, `session/goal_judge.go`, `httpserver/server.go`
  (`defaultProviderFromAgentModel`, `defaultMakeLLMFromYAML`), `dryrun/providers.go`,
  `providers_models_http.go`.
- `--dry-run` warns about an `acp` row without `modes.plan` / `modes.ask` (those modes refuse to run on it).

## The provider (internal/llm/acp*.go)

### Process pool

A package-level pool keeps one agent process per *spec key*: row name, command, args, env, process cwd,
proxy and auth method, plus the session workspace when any of them names `${CWD}`. `NewProvider` runs per
turn and the agent lives across turns and sessions, so the process cannot belong to a provider instance.

- Started lazily (single-flight per key): `exec.Cmd` through `platform.AdaptCommand`, its own process group,
  kill-on-close job, stderr kept in a 4 KiB ring for error messages. Then `initialize` (protocolVersion 1,
  `fs` and `terminal` capabilities off, `clientInfo` coddy + version; a different protocolVersion is a start
  error) and, when `auth_method` is set, `authenticate`.
- One reader goroutine routes newline-delimited JSON-RPC (a line is bounded at 32 MiB): responses by id,
  `session/update` to the prompt of that `sessionId` (and to the session's known state, whether or not a
  prompt runs), `session/request_permission` to that prompt (answered `cancelled` when no prompt runs), any
  other request from the agent `-32601`, other notifications ignored, non-JSON lines logged and skipped. A read
  error (an over-long line) kills the process group first, then reaps it.
- Stopped after `idle_timeout_ms` (default 60 minutes) without a call, on shutdown (`llm.StopACPAgents`, called
  next to stopping the MCP servers by `coddy serve`, `coddy acp` and the console), and right away once it
  drains when it does not advertise `session/close` and has opened 64 sessions. A process that exits fails
  every waiting call with an error carrying the stderr tail; the next call starts a new one, and every binding
  of the dead process goes with it.
- Recursion guard: the child gets `CODDY_ACP_DEPTH=<own depth + 1>`; a Coddy whose depth is 3 refuses to
  start an agent.
- Proxy: empty or `inherit` leaves the environment as is; `none` removes `HTTP(S)_PROXY`, `ALL_PROXY` and
  `NO_PROXY` (both cases); a URL sets `HTTPS_PROXY` and `HTTP_PROXY`, sets `NO_PROXY` to the loopback
  addresses and removes `ALL_PROXY`, so the agent's own local traffic (zcode-acp-server to its app server)
  stays direct. Whether the agent honours the variables is its business; the docs say so.

### Who may call it

An `acp` model answers inside a session only. The provider refuses (`ACPError` kind `unsupported`):

- a call with no conversation on its context - compaction, titles and tags, the prompt rewrite, the goal
  judge, `/v1/chat/completions` and `/v1/responses` direct completions. Those paths keep away from `acp`
  models on their own: compaction skips them in its chain and names `compaction.model` when nothing is left;
  the describe call and the prompt rewrite take the first model that is not `acp`; the goal check names
  `supervisor.model`; a direct completion on an `acp` model answers 400;
- the memory subagent: a child session of its own, so it would pass the checks below, but it would take the
  memory task for the user's request; it is skipped on an `acp` model (naming `memory.model`), and `acp`
  models are left out of its fallback chain;
- a restricted turn (a messenger sender who is not an admin: `TurnRestriction.AllowedTools != nil`): the
  agent's own tools are not held by the restriction;
- a session in `plan` or `ask` mode whose `acp.modes.<mode>` is empty: the agent would run in its default
  mode, which may write without asking.

### Conversations

Coddy sends the whole history on every call; an ACP session keeps its own. A Coddy session is bound to one
ACP session of the process.

- The agent loop puts `llm.Conversation{SessionID, CWD, Mode, PermissionMode, Restricted, AskPermission}`
  on the context of its own `Stream` call only (`llm.WithConversation`).
- The binding records what the ACP session holds: a digest per message sent (role, text, pictures, tool calls,
  tool call id), and after each prompt that completed with text an *own-reply slot* matching zero or one
  assistant message without tool calls (Coddy stores the answer trimmed, folded or not at all; the text is not
  compared). Ephemeral messages - the turn-context block and the recovery nudges the loop never persists,
  marked `llm.Message.Ephemeral` - are sent but never recorded or matched.
- A call drops the system message and the ephemeral messages, matches the record against the history from the
  front, and:
  - record is a proper prefix: sends what follows on the bound session;
  - record covers the whole history (an exact repeat): after a completed prompt it is a regenerate - a new
    session; after a prompt whose outcome is uncertain (cancelled, exited or failed after the agent sent
    updates) it fails with an error saying so, never sending the message twice;
  - anything else (rewind, edit, compaction, dead process): `session/close` on the old session, a new one, the
    whole history as its first prompt.
- Outcomes: a non-error result records the messages and an armed slot; an `rpc` error before any update drops
  the binding (the agent refused the prompt, a new session starts next time); an error, cancellation or exit
  after updates records the messages, leaves the slot unarmed and marks the binding uncertain.
- One prompt at a time per binding: the lock is a semaphore a call waits on, bounded by its own context. No
  call ever forks a second ACP session for a busy conversation.
- Prompt content: a single user message goes as its own text and image blocks; several messages go as a plain
  transcript (`## User`, `## Assistant`, `## Assistant called the tool <name>`, `## Tool result`) with image
  blocks between the parts when the agent takes images, the last user message last; ephemeral messages follow
  as their own text blocks.

### Model, mode and reasoning

The values `session/new` reported are the session's defaults. Before every prompt the session is brought to
the wanted state, sending only what differs from the last known state (kept current by `config_option_update`,
`current_mode_update` and every `set_config_option` result):

- model: the row's model, or the initial one for `default`; through the `model` config option, else
  `session/set_model`; an unknown model fails naming what the agent offers;
- mode: `acp.modes[<Coddy mode>]`, or the initial mode when empty (agent mode only); through the `mode` config
  option, else `session/set_mode`;
- reasoning: the session's level when an option of category `thought_level` offers it, else the initial value.

A reused session that cannot be brought to the wanted state is replaced by a new one.

### Streaming and the answer

- `agent_message_chunk` -> text; `agent_thought_chunk` -> reasoning; other content kinds become a short note.
- The agent's tool calls are reasoning lines numbered per prompt (`[tool 3] Read README.md`, `[tool 3 failed]`),
  so the loop guard never sees five identical lines; they are never Coddy tool calls.
- The result: every stop reason but `cancelled` is `end_turn`; `max_tokens`, `max_turn_requests` and `refusal`
  add a visible line saying why the agent stopped. A prompt that completed with no text answers
  `(the agent finished without a message)`, so the loop's empty-answer recoveries never re-issue a prompt the
  agent already ran. `usage` fills the token counts for accounting.
- `cancelled` with the caller's context alive is an `ACPError` of kind `cancelled` (not retryable).
- Cancellation: `session/cancel`, every pending permission request answered `cancelled`, up to 10 s for the
  agent's response. When it does not come, the prompt is abandoned: its late updates are dropped, its session
  closed, the binding reset, and the conversation lock stays held by a watcher that kills the process if no
  response arrives within 20 s more - so a retry never runs next to a prompt still at work.
- A prompt that sends nothing for 30 minutes (time spent waiting on a person excluded) is treated the same way.
- `timeout_ms`, when positive, bounds the prompt.
- The agent's context is its own: the loop records no provider input count and runs no automatic compaction
  on an `acp` model, and arms no first-token timer for it (process start, handshake and replay would count
  against it).

### Errors

`*ACPError` with a kind: `config`, `unsupported`, `start`, `exited`, `rpc`, `auth` (-32000 on any method,
naming the agent's `authMethods` and `acp.auth_method`), `select` (a model or mode the agent does not offer),
`cancelled`, `stalled`. `isRetryableLLMError` and `IsTransientProviderError` decide `ACPError` by kind before
reading any text: only a transient `start` failure (not a missing or non-executable program) and `exited`
before any update are retryable.

### Permissions

`acp.permissions` decides what the agent's `session/request_permission` gets:

- `ask` (default): the request goes to the person through `Conversation.AskPermission`, which the loop
  implements with `UpdateSender.RequestPermission`, stamped with the session's permission mode
  (`EffectivePermissionMode`) so every surface relays it live and none stores it as a resumable Coddy call.
  The prompt is built from the request merged with the tool call's state from earlier updates (title, kind,
  content, raw input) and offers Coddy's own options - `allow`, `allow_always` when the agent offers it,
  `reject`. The answer maps back: `allow` to the agent's first `allow_once` (else `allow_always`), `reject`,
  a dismissed prompt or no answer to its first `reject_once` (else `reject_always`, else `cancelled`).
  Under the `bypass` permission mode the request is allowed without asking, under `accept_edits` a request of
  kind `edit` is.
- `allow`: the first `allow_once` (else `allow_always`).
- `deny`: the first `reject_once` (else `reject_always`, else `cancelled`).
- In `plan` and `ask` mode every request is refused, whatever the setting (the mode mapping above is the real
  guarantee; this is the second layer).
- `cancelled` is sent only when the turn is cancelled or the agent offered no reject option.
- A tool row: before asking, the loop announces a `tool_call` (pending, kind of the request) on the session, and
  updates it to `completed` or `cancelled` after the answer, so an editor that only renders permission prompts
  for calls it has seen shows this one.

## Other surfaces of the type

- `llm.ListModels` for `acp`: the pooled process, `session/new` in `acp.cwd` or Coddy's working directory, the
  model values (config option of category `model`, else `models`), `session/close` when the agent can;
  `default` first. `--dry-run` probes the row through it.
- `POST /coddy/providers/models` for an `acp` row uses the saved row's `acp` block; an unsaved row answers 400.
  This is a rule of the form, not a security boundary (the same client can save the row).
- `provider_label.go`: an `acp` row's errors name the command.
- Settings form: the `acp` block is shown on `acp` rows only; `api_base`, `api_key` and `api_key_command` are
  hidden there.
- Usage panel, context-window listing, sign-in routes: none for `acp`.

## Tests

- `internal/llm/acptest`: a scripted ACP agent, the test binary re-executed (the `hooktest` pattern); it logs
  every message it receives.
- `features/acp_provider.feature` with `internal/llm/bdd_acp_provider_test.go` (happy paths) and unit tests for
  the edges in `internal/llm` and `internal/agent` (the loop: one session over turns, an empty answer, a
  permission through `RequestPermission`, plan mode without a mapping, a restricted turn, helpers refused).
- Live check (not in CI): `examples/acp/provider_e2e.sh` runs `coddy -p` on an `acp` row for each agent found,
  one agent after another: an answer in agent mode, the next turn of the same conversation (`coddy -c`), an
  answer in ask mode, each passing on exit status 0 and the word on standard output. Two sessions answering at
  once on one agent process was checked by hand on the stand (coddy, opencode and codex hold it, ZCode does
  not: `process: per_session`).

## Documentation

`docs/features/acp-provider.md` (nav, Russian translation), the configuration guide, the environment variables
reference (`CODDY_ACP_DEPTH`), the README model line, the architecture backends list, the `configure-coddy`
skill. The page says that the agent inherits Coddy's environment (an `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`
there may take precedence over the agent's own login), that a `coddy acp` child should get a `CODDY_HOME` of
its own through `acp.env`, and which mode each agent needs for plan and ask.

## Out of scope

- Offering Coddy's tools to the agent (as an MCP server it could call).
- `session/load` / `session/resume` after a Coddy restart or an idle stop: the next call replays the transcript
  into a new session.
- The agent's `fs/*` and `terminal/*` client methods: refused, the agent uses its own tools.

## What the plan review changed

Reviewers: claude-opus, coddy on gpt-6.1-sol, coddy on gemma-4-31b; 3 of 3 answered. Accepted and folded in
above: read-only modes and restricted turns fail closed; helpers never reach an agent; a message the agent ran
is never sent twice (outcome states, the visible line for an empty answer, no first-token timer); no forked
session for a busy conversation and the abandonment of a prompt that ignores its cancel; the agent's context
kept out of Coddy's compaction; persisted-never nudges marked ephemeral; the permission bridge speaking the
surfaces' vocabulary and stamped; `reject` rather than `cancelled` for refusals; the record advancing only on a
known outcome and the slot armed only by the agent's own completed answer; `ACPError` decided by kind; the
protocol checks (absolute cwd, protocol version, reader errors); session leaks; a silence bound; numbered tool
lines; stop reasons without the output-limit nudge; images in a replayed history; a longer idle default; the
proxy variables. Rejected: the transcript replay "breaking" the protocol (the provider never returns tool
calls), refusing an explicit proxy on an `acp` row, and an id collision with a Coddy pending call.

## What the review of the implementation changed

The same three reviewers read the implementation in four parts (provider core, wiring and the Settings form,
tests, documentation with its translation). Folded in:

- the provider core: one writer goroutine for the agent's stdin, so no write blocks a cancellation, the
  silence bound or the reader (a write stuck past its caller's context stops the agent); the agent's start
  runs under no caller's cancellation and every caller waits for it under its own; only content (text,
  thought, a tool call, a plan, a permission request) marks a prompt as acted on, not the agent's status
  updates; the reader reaps the agent with `WaitDelay` and ends its process group, so a child holding its
  stderr neither keeps a dead agent "alive" nor outlives it; bindings unused for the idle period are closed
  and forgotten; the uncertain mark survives the loss of its binding (pool level); a permission request
  queued behind a prompt's response is answered cancelled; an answer outside Coddy's own option ids is a
  refusal; a current model or mode the agent leaves out of its list still counts; a cancellation while a
  reused session is being brought to its state does not discard it; a completed answer from an agent that
  exited right after is kept; a picture-only message sends no empty text block; the message digest leaves out
  what the loop rewrites (an evicted tool result's text, older pictures);
- the wiring: the memory subagent never runs on an `acp` model; `acp.env` values are secrets for
  `config_get`; an approval with no option id is an approval; the models route copies the saved block; the
  prompt rewrite borrows another model; a row of another type does not keep a seeded `acp` block; the dry-run
  probe of an `acp` row gets 60 s to start the agent;
- the documentation: the accuracy fixes (plan and ask need their modes, the web UI's own labels on the
  permission buttons, what an unconfirmed cancellation does to a shared agent, "whatever sandbox it has") and
  the Russian translation's typography, glossary and calques.
- the tests: cancellation is triggered by the first streamed text instead of a timer, the retry tests run
  with the wrapper's retries on and count the prompts sent, and the loop's exemptions (first-token timer,
  compaction, goal check, direct completions) each have a test; that last test found a streamed direct
  completion answering 200 with an error frame, now a 400 before any session or stream is opened.

A second round read the provider core whole and folded in:

- a call whose context already ended hands the agent nothing, and one whose context ends after the hand-off
  gives the line a short grace (`acpWriteGrace`) before the agent is taken for one that stopped reading: an
  ordinary Stop used to stop the agent every conversation shares; every write, a reply in the background
  included, has an overall bound (`acpWriteTimeout`), and a failed write retires the agent;
- a prompt that fails after the agent acted is marked uncertain in the pool whether or not the process is
  alive, since its binding can go later (a sweep, an idle stop, a crash); what the agent reported counts on
  every way out of the cancel path and when the prompt's write fails;
- an error answer read just before the agent exited is that error, not a retryable exit;
- a killed agent stays in the pool, marked dying, until it is gone: a caller waits rather than starting a
  second agent next to a prompt that may still be at work; `stop` signals nothing once the agent is reaped,
  and on Windows the reaped pid is never signalled (the kill-on-close job ends the tree);
- a binding a sweep released between its lookup and its lock is looked up again; the reader pushes into a
  prompt's sink under the lock that detaches it; the mode copy in `modes` and in the mode config option move
  together; `StopACPAgents` cancels starts still in their handshake; "Reject" keeps the agent's option kind;
  a transcript names each picture once.

Capturing the documentation's screenshots on a real `coddy acp` row found two faults outside the provider,
both fixed here:

- `GET /v1/models` marks the session profiles `owned_by: coddy` and a model row `owned_by` its provider, and
  the web UI and the remote console took every `coddy` row for a profile: the models of a provider row named
  `coddy` (the natural name for a `coddy acp` agent) were missing from the composer's selector. Both now tell
  a profile by its id (`agent`, `plan`, `ask`), as the server does.
- An answer whose text and reasoning interleave in one turn (an agent thinking between two parts of its
  answer) was shown twice at the end of the turn in the web UI: the persisted message replaced the live parts
  under the id of the first one, the end of the turn looked for its answer under the id the turn was sent
  with, did not find it, and wrote the server's answer into the last part again. The stream now reports every
  part's id, and the end of the turn looks under all of them.
- The same answers showed a fault of the provider itself: an agent sends the parts of an answer it
  interrupted to think or to run a tool as chunks of one message, and the stored answer ran the last sentence
  of one part into the first of the next ("...the file.Created..."). Text that resumes after a thought or a
  tool call now starts a paragraph of its own, the same in the stream as in the stored message.

## On the protocol's Go SDK

The first version spoke the protocol through code of its own: a reader goroutine, a map of pending requests,
the routing of responses, notifications and requests, and a declaration of every message shape. That is now
`github.com/coder/acp-go-sdk` (v0.13.5: Apache-2.0, no dependencies of its own), so the protocol is the SDK's
and the provider keeps what is Coddy's.

- The SDK's `Connection` is the JSON-RPC peer over the agent's stdio: ids, pending requests, the agent's
  requests answered from a handler on goroutines of their own. Every request Coddy sends is the SDK's type
  (`initialize`, `authenticate`, `session/new`, `session/prompt`, `session/cancel`, `session/close`,
  `session/set_mode`, `session/set_config_option`), and so are the prompt result and Coddy's answer to a
  permission request.
- Decoded by the provider itself, leniently: the initialize result when it does not fit the SDK's type (a
  capability of another shape, `close: true`), a session's state (an option of a type the SDK's schema does
  not know fails its whole response there, and the older `models` with `session/set_model` are not in it),
  `session/update` (Coddy as the agent streams reasoning as a message chunk of type `reasoning`) and the
  permission request's tool call (its content and raw input are only shown).
- Order. The SDK hands notifications to one queue and the agent's requests each to a goroutine, so a
  permission request could reach its prompt ahead of the tool call it asks about, or after the prompt ended.
  The agent's stdout reaches the SDK through a router that reads every line first, in the order the agent
  wrote it: a `session/update` goes to the prompt running on its session, a `session/request_permission` is
  bound to that prompt then (the SDK's handler takes the binding by the request's params and answers from
  it), and the answer of a prompt in flight is kept. A line the SDK reads, the router reads too: an escaped
  `\/` in the method, CRLF, a last line with no newline.
- Order the other way. `startPrompt` returns once the prompt's line is out, so a `session/cancel` sent
  after it reaches the agent after the prompt; a caller that gives up before the writer has the line sends
  nothing (the writer refuses it). The prompt itself is sent under no caller's context, so a prompt that
  ignores its cancellation still yields its answer to the conversation that waits for it.
- Answers an exit follows. The SDK hands a response over only after the notifications before it were
  handled, and gives the response up when the connection ends meanwhile. The prompt's answer is then the
  one the router kept. A request's error is classified by the error itself (the SDK's "peer disconnected"
  is the connection's end), never by whether the agent exited since, so an error or an authentication
  answer followed by the agent's exit stays that answer.
- Writes. The SDK writes without a context. Its writer is the provider's: one goroutine writes to the
  agent's stdin, every write is bounded (`acpWriteTimeout`, taken when the process starts), and a write that
  fails or runs out its bound stops the agent. A call returns when its context ends; a write that still
  hangs after `acpWriteGrace` stops the agent. While a cancelled prompt waits for its end, a write that
  hangs for longer than the grace (the cancellation itself, a permission answer) stops the agent too.
- A line from the agent is bounded at 10 MiB, the SDK's limit (it was 32 MiB): a longer one ends the
  connection, the agent is terminated at once and the error says why. The reason an agent ended is the most
  telling one: a line too long, an agent that stopped reading, Coddy stopping it, its exit status, then a
  write that failed on the way out.
- The process, its group, its stderr, the pool, the conversations and the provider's decisions stay as they
  were.

The move was reviewed by the same three reviewers, who found the order of a cancellation against its
prompt, answers lost to the agent's exit, a cancelled call waiting out the write bound, a person's answer
landing after the prompt was cancelled, permission requests bound late, the stuck-write check running once,
an escaped method, a strict initialize, the reason of a too-long line, the reason of an exit and a last
line with no newline. Each is fixed as described above; `internal/llm/acp_sdk_test.go` holds a test for each but
the repeated stuck-write check and the order of the exit reasons.
