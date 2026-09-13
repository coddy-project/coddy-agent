# Plan: the session bus (every surface a client of the session manager)

Status: design record and work plan written 2026-09-12. Not implemented. It
records the target shape agreed for the core - a per-session event bus in
`internal/session`, with every entry point (console, web UI, editor, messenger,
remote client) and every inference executor (prompt turns, plan runs, resumes,
background wakes, subagent children, scheduled jobs) attached to it as a client -
and the work to get there, PR by PR. Nothing in it has been started; PR #213,
the console-only fix that prompted the discussion, was closed without merging in
favour of this design.

## 1. What

Today a permission request or a question from the `question` tool is a
**blocking call on the sender of the surface that started the turn**
(`acp.UpdateSender.RequestPermission` / `RequestQuestion`). Whoever holds that
sender is the only one who can answer, and the design records this on purpose:
`features/serve_turn_mirror.feature` says "only the chat is asked for
permission", `internal/session/turn_mirror.go` says "everything that needs an
answer stays with the surface that has a human".

The operator's model is different, and the report that surfaced it is the one
to keep in mind. A user sat in the console; generation stopped, the token
counters froze, no form appeared. He opened the web UI on the same session and
found a question waiting there. The web UI can show that question because the
`question` tool call is persisted as `in_progress` and the SPA rebuilds the
prompt from its arguments; it cannot answer it, because the answer channel is a
Go channel inside the console process. Whatever the exact trigger in the
console was (PR #213 proved one: a second prompt replacing the first in the
single modal slot), the operator's expectation is the right one:

> Any client can attach to a session, pull what happened before, see what is
> happening now, and if inference stopped on a question or a permission prompt,
> any client of that session sees it and can continue it.

So the target is a **session bus**:

- one process owns a session at a time (the one holding its turn lock, which is
  `coddy serve` in the setup this is for); every human surface is a client of
  that process (the web UI, the console and `coddy acp` through `--remote`, the
  Telegram gateway in-process), and a bare `coddy` or a local `coddy acp` is the
  same code with one client attached;
- a turn publishes to the session's bus, not to the sender that started it;
  any number of consumers subscribe, late ones replay the running turn;
- a permission request or a question is **state of the session**, published to
  every consumer, answerable from any of them; the first answer wins and the
  turn continues everywhere;
- the core does not know the surfaces, and the surfaces do not know each other.

Constraints set for the work:

- no source-level compatibility inside the repository: the sender interface
  is broken on purpose, senders that can no longer be written are deleted;
- full compatibility of what is **on disk**: `config.yaml` needs no change, a
  session bundle written by today's binary loads and continues under the new
  one, and a user who updates sees nothing but the new behaviour;
- `stream: true` and `stream: false` on the OpenAI-shaped HTTP routes keep
  their response shapes: every frame a client receives today is unchanged,
  and one named event is added to the streamed shape.

## 2. Where the code is today (2026-09-12, `main` at 4314296d)

The facts the design rests on, so the implementer does not have to rediscover
them.

**The sender.** `internal/acp/sender.go` declares `UpdateSender` with three
methods: `SendSessionUpdate`, `RequestPermission`, `RequestQuestion`. Twelve
production implementations: the console (`external/cli/sender.go`, channels
`permCh` / `questCh` drained by the UI loop, one modal slot), print mode
(`external/cli/print.go`, rejects with a note), the console's late-bound
sender (`external/cli/run.go`), the HTTP bridge (`external/httpserver/bridge.go`,
`NewSender` interactive for `stream: true`, `NewRelaySender` deliberately
non-interactive for `stream: false`, wake turns and permission resumes), the
turn mirror (`external/httpserver/turn_mirror.go`), a plan-run no-op
(`external/httpserver/coddy_plans.go`), the scheduler's auto-allow
(`external/scheduler/daemon/run.go`), Telegram (`external/gateway/telegram/sender.go`,
auto-allows permissions, posts the first question as text and returns empty
answers), `serverRef` for `coddy acp` (`cmd/coddy/main.go`), the subagent
child sender (`internal/agent/subagent.go`, denies questions), the ACP server
(`internal/acp/server.go`, JSON-RPC `session/request_permission` and the Coddy
extension `session/request_question`) and `serve`'s `defaultSender`
(`internal/serve/runtime.go`, refuses unless bypass). Five of them re-decide
`bypass` on their own (`PermModeBypass` in the console, print, the HTTP bridge,
`serverRef`, `defaultSender`) although the agent already decided whether a
prompt is needed (`internal/agent/react.go`, `requiresPerm` from
`effectivePermMode`, the only place that should).

**Who asks.** Permissions: `internal/agent/react.go:1205`
(`a.server.RequestPermission`, options from `permission.Options`, grants via
`permission.RecordAllowAlways`). Questions: `internal/tools/question.go` through
`tooling.Env.Sender`. A subagent child asks its parent through
`permissionRelay` (`internal/agent/subagent.go`): the parent's session id, the
child's effective mode stamped in `EffectivePermissionMode`, the title prefixed
`[subagent <name>]`, the `allow_always` options dropped, and an **arbiter**
that serialises the prompts of every child of one parent because "the HTTP
pending-permission record can hold exactly one".

**What the HTTP server already shares.** Every agent turn on `/v1/responses`
and `/v1/chat/completions` publishes into a per-session **composer relay**
(`composer_stream_relay.go`): a bounded buffer of whole SSE frames (512 KB),
numbered, replayed to any subscriber of `GET /coddy/sessions/{id}/composer-stream`,
resumable with `Last-Event-ID`, `event: desync` when the window was trimmed.
`stream: true` tees the response writer into it (`teeSSEWriter`), `stream:
false` writes only into it. The `permission` and `question` events travel
through it, so a watching tab sees them. The waits are **process-wide
registries** (`permissionhub.go` keyed by session and tool call id,
`questionhub.go` keyed by session and request id), and `POST
/coddy/sessions/{id}/permission|question` (`coddy_coddy.go`) resolves them from
any HTTP client, `404` when nothing waits. A permission is persisted while it
waits (`internal/session/pending_permission.go`, one record per session,
`pending_permission.json`), and an answer that arrives after a restart resumes
the turn (`permission_resume.go` builds an agent and calls
`agent.ResumeAfterPermission`). Questions are not persisted. There is no
"answered elsewhere" event: a second tab keeps its form, and its answer gets
`404`.

**The turn mirror.** `session.TurnMirror` (`internal/session/turn_mirror.go`)
wraps a surface's sender so its updates also go to the relay; the HTTP server
implements it (`turn_mirror.go`), `serve` installs it (`internal/serve/runtime.go`),
the Telegram bot uses it (`external/gateway/telegram/bot.go`). Gates stay with
the primary sender by design.

**The remote client.** `internal/remote` drives a turn as `POST /v1/responses`
with `stream: true` and reads the SSE (`prompt.go`). `onPermission` and
`onQuestion` call the local surface's blocking sender **inside the SSE reader**,
then `POST` the answer; `onPermission` tolerates `404`/`409` (`isStaleAnswer`),
`onQuestion` does not, so an answer given from another client fails the whole
turn with `question answer: 404` while the server keeps going. While idle the
client is not attached to anything, so a turn started from the web is not
visible in a remote console.

**The console.** `external/cli/app.go` drains `permCh` and `questCh` into one
modal slot; `openModal` replaced whatever was there, which is the deadlock PR
#213 fixed by queueing (closed, not merged).

**Turn bookkeeping already in the manager.** `beginTurn` (turn lock, the
per-turn context cancelled by `finish`), `markTurnActive` and the observers of
`turn_events.go` (`AddTurnObserver`, delivered synchronously on the turn's
goroutine, feeding `GET /coddy/events`), `withTurnRanMarker` on the turn
context, the cross-process cancel poll (`manager_cancel_poll.go`, a file in the
bundle polled every 280 ms) and `TurnLockHeld` (`manager_turn_lock_unix.go`,
the flock probe for "another process runs this session").

**The SPA.** `consumeComposerSse.ts` handles `permission` and `question` on its
own turn stream and on the watch stream alike; `PermissionPromptSection.tsx` and
`QuestionPromptSection.tsx` post the answer and mark the row resolved locally;
`questionPromptSessionStore.ts` shadows prompts in `localStorage`;
`App.tsx` attaches to `composer-stream` on `turn_started` from `GET
/coddy/events` (`serverEvents.ts`). The swarm proxies `composer-stream`,
`/coddy/events` and the REST routes as opaque HTTP (`external/swarm/server.go`).

## 3. Approach

### 3.1 The bus

Three new files in `internal/session`: `bus.go` (topics, log, subscribers),
`gates.go` (the gate registry and policies), `gates_store.go` (persistence).
The types that travel on the wire live in `internal/acp/gate.go`, because
`internal/acp` is already the protocol vocabulary imported by `internal/tooling`
and by every surface.

A **topic is a session**. Its owner is the manager of the process holding the
session's turn lock. A topic has:

- a **log of the running turn**: every value that `SendSessionUpdate` carries
  today (message chunks, tool calls and their updates, plan, token and
  provider usage, memory phases, mode and config-option updates, available
  commands) plus three new kinds, `gate_opened`, `gate_resolved` and the
  service record `desync`. Each record gets a `seq`, monotonic per topic for
  the life of the process. The log is bounded by bytes (512 KB, the relay's
  bound, sized by the record's JSON encoding computed once at publish), trimmed
  from the head by whole records. Its lifetime is the active-turn epoch:
  it opens when the session's active-turn count goes from zero to one
  (`markTurnActive`) and is cleared in the same critical section that
  publishes the `ended` edge, so a subscription taken around that moment
  sees the whole log or none of it, never a torn one. Between turns the
  history is the transcript on disk, as it is today;
- the **pending gates**, state kept apart from the log so a late subscriber
  sees them even after the log was trimmed;
- the **subscribers**.

The interface on the manager:

```go
// producers
func (m *Manager) Publish(sessionID string, update any)
func (m *Manager) Publisher(sessionID string) acp.UpdateSender   // acp.UpdateSender keeps SendSessionUpdate only

// consumers
type Consumer interface { Deliver(sessionID string, seq uint64, update any) }
func (m *Manager) Subscribe(sessionID string, c Consumer, afterSeq uint64) (detach func())
func SenderConsumer(s acp.UpdateSender) Consumer                 // adapter for consumers that ignore seq

// gates; acp.GateKeeper is the subset executors see
func (m *Manager) OpenGate(ctx context.Context, g acp.Gate) (*acp.GateAnswer, error)
func (m *Manager) AnswerGate(sessionID, gateID string, a acp.GateAnswer) error   // ErrNoPendingGate
func (m *Manager) PendingGates(sessionID string) []acp.Gate
```

Rules:

- `Subscribe` enqueues, in this order and atomically under the topic lock so
  nothing can slip between them (records are enqueued under the lock and
  delivered outside it, never through a callback held under it): a `desync` record when `afterSeq` is before
  the retained window (the consumer reloads the transcript; the
  `composer-stream` contract of today, lifted out of the HTTP layer); the
  retained log after `afterSeq` (0 means all of it); the `gate_opened`
  record of every pending gate that the replay did not cover, re-sent
  **with its original `seq`** (a gate remembers the `seq` it was opened
  at), so `id:` stays a real cursor, a client that resumes past that `seq`
  is not sent a gate it already holds, and a client deduplicates by
  `gateId` should it see the record twice; then the live records. A
  subscriber reset after a queue overflow gets the same treatment: `desync`,
  then the pending gates re-sent, then the tail, so a gate whose opening
  record was trimmed can never stay hidden. A late client therefore always sees
  the question the turn is waiting on, whether the log still holds its
  record, was trimmed past it, or holds nothing at all because the gate
  belongs to a background child that outlived the turn.
- A subscription on a topic with no running turn is allowed and lives as long
  as the caller keeps it; that is how the console and the ACP server attach to
  a session once. It replays nothing and receives the records of later turns.
- A consumer is subscribed to a topic at most once: `Subscribe` with a
  consumer already attached returns the existing detach, and the default
  consumer counts as attached to every topic. A surface that is the default
  consumer, or already holds a standing subscription, passes `nil` as the
  turn's sender; today the console's app sender is both `m.server` and the
  per-turn sender, which on a bus would deliver every record twice.
- A record is an immutable snapshot. `Publish` takes ownership of the value
  it is given: the producer never touches it again (the update types are
  plain structs the agent builds fresh per update, which is what makes the
  rule cheap), the record stores the value together with its JSON encoding
  computed once at publish, the HTTP encoder writes that encoding and never
  reads the typed value, and in-process consumers read the value and do
  not mutate it. Delivery under concurrent publishes runs under `-race` in
  the bus tests.
- The bus owns the buffering, not the consumer. `Publish` appends the record
  to the log and to every subscriber's bounded queue (1024 records) under the
  topic lock and returns; it never runs consumer code inline, so a turn can
  never be stalled by a consumer and a consumer can never deadlock against
  its own delivery (today the console has to keep the rule "manager methods
  that emit updates are only ever called from worker goroutines" for that
  reason; the rule goes). Each subscription has one delivery goroutine that
  calls `Deliver` in order; a subscriber whose queue overflows is reset to
  the log tail and gets a `desync` record first, the contract the HTTP
  encoder implements for itself today. A consumer's error is ignored; a
  consumer recovers its own panics.
- Delivery is asynchronous, so the end of a turn waits for it: before
  `HandleSessionPromptWithSender` returns, the manager waits, with a bound of
  two seconds, until every subscriber of the topic has delivered the turn's
  last `seq` or is desynced or detached. That keeps the orderings the surfaces
  rely on: the ACP `session/prompt` response after the last `session/update`,
  the HTTP JSON body after the watchers' frames, the console's `turnDone`
  after the last chunk. A subscriber that cannot keep up within the bound is
  desynced, never waited for again.
- Records about gates are published by the goroutine blocked in `OpenGate`,
  never by the one calling `AnswerGate`: an answer only flips the gate's
  state and wakes the waiter.
- A turn ends with a record of its own. Today the HTTP handler writes the
  error frame, `coddy_meta` (effective model, stop reason) and `[DONE]`
  into the bridge after `HandleSessionPromptWithSender` returned, and the
  relay carries them to the watchers; on the bus those would be written
  after the log is gone. So the manager publishes `turn_finished` (stop
  reason, error, the metadata the turn collected) as the last record of the
  epoch, the drain barrier waits for it, and only then does the log close.
  The HTTP encoder renders it as the frames it writes today; a watcher
  attached at the last moment still sees how the turn ended. Keepalives are
  the encoder's own idle comments, not records.
- `Publish` outside a running turn (a mode change, a config-option update, the
  slash catalog) is delivered to the subscribers and not retained: that is
  state, and a late client reads state through `session/load` or the REST
  routes.
- `m.server`, the surface installed with `SetServer`, stays as the default
  consumer of every topic (the ACP server and the console need every session
  they created). It is delivered through the same machinery, one queue and
  one delivery goroutine per topic, so a JSON-RPC writer stalled by a slow
  editor desyncs that one session's delivery and touches nothing else. It
  needs no `seq`.

**Gates.** `acp.Gate` is `ID`, `SessionID`, `Kind` (`permission`,
`question`; a later kind is a new value, not a new mechanism), exactly one
payload in today's shape (`*PermissionRequestParams` or
`*QuestionRequestParams`), `Origin` (empty for the session's own agent,
`subagent <name>` for a relayed child), `OpenedAt` and `Persist`. The `ID`
is **minted by the manager** (`gate_<n>`, unique per session for the life of
the bundle, the counter persisted like the background task counter is), not
taken from the payload: a `toolCallId` comes from the provider, can contain
anything including path separators, and two children of one parent can
emit the same `call_1`. The minted id travels on the wire: the `permission`
and `question` events carry `gateId` next to the payload, the answer bodies
accept `gateId`, and the SPA, the console and the remote client answer by
it. The payload keeps its `toolCallId` or `requestId` as today and the
manager indexes them per session, so a client that still answers by those
keeps working as long as the id is unambiguous; a legacy id that matches
more than one pending gate is refused with `409` rather than guessed, and
the "two children answered in either order" acceptance runs on `gateId`.
On disk the file name is the minted id, never provider text. `acp.GateAnswer`
is `*PermissionResult` or `*QuestionResult` plus `By`, the client that
answered.

`OpenGate` registers the gate, writes it to disk when `Persist` is set,
publishes `gate_opened`, and waits for `AnswerGate` or `ctx.Done()`. On either
it publishes `gate_resolved` (the gate id, the kind, the outcome, the answer
summary, `By`), removes the record and returns. The first answer wins under the
registry mutex; a later one gets `ErrNoPendingGate`. A cancelled turn, a
deleted or forgotten session resolves its gates as `cancelled` through the
same event, so every surface has one path to take a modal down.

**Gate life cycle.** A gate has two states, pending and resolved, and one
transition, taken under the registry mutex by whichever of these comes
first: an answer (`AnswerGate`), the turn's context ending (cancel from any
surface, the cross-process cancel file, the session being deleted or
forgotten), or the turn's policy. The transition is final: a second answer,
an answer after a cancellation, or a cancellation after an answer all see
the resolved state and report `ErrNoPendingGate`. An answer is validated
before it is allowed to take the transition: an `optionId` that the gate
did not offer, or answers whose shape does not match the questions, are
refused (`ErrInvalidAnswer`, `400` over HTTP) and leave the gate pending,
so a malformed client cannot burn the one transition. Over HTTP the answer
routes therefore answer `204` accepted, `400` invalid answer, `404` nothing
pending, `409` ambiguous legacy id, being resumed already, owned by another
process, or a child session. The waiter publishes exactly one
`gate_resolved`. What happens to the persisted record depends
on why the gate resolved: an answer or a refusal removes it; a cancellation
the operator asked for (`State.SetUserCancelledTurn`, which every surface's
cancel path sets, the cross-process cancel file included) removes it too,
because nothing is left to resume; any other end of the turn - the process
shutting down, a provider error, a crash - leaves the record in place, the
waiter returns `cancelled` to the executor, and the published outcome is
`suspended`, so a connected client shows the gate as resumable rather than
dead. A suspension is not a cancellation to the executor either:
`OpenGate` returns `ErrGateSuspended`, and the agent leaves the loop on it
**without recording anything** for the call, neither the "permission denied
by user" result the permission path records today for every non-approval
(`react.go:1205`) nor an empty question answer, so the tool call is still
in progress in the transcript and `findPendingToolCall` finds it after the
restart. Today that only works when the process dies before the turn
persisted; a graceful shutdown records the denial and the resume then
answers "already has a result". After a restart the record is a pending
gate again: it is in the snapshot every new subscription receives and in
`GET …/gates` as `resumable`, and answering it runs `ResumePendingGate`. Today the record
lingers after a user cancel and is cleared only by an answer, which is one
of the loose ends this replaces. `OpenGate` with an id already pending on the
session fails with `ErrGateExists` rather than replacing it. A persisted
gate is consumed under the same mutex when `ResumePendingGate` picks it
up, so a second answer racing the resume gets `ErrNoPendingGate` (`409`
over HTTP) and nothing runs twice. Gates are sequential with the rest of
the loop: a turn parked in the usage-limit wait (`limit_wait.go`) has no
gate open, and a gate never outlives the ReAct step that opened it.

**Policy belongs to the turn, not to the surface.** `PromptRunOpts.GatePolicy`
takes `Ask` (default), `AutoAllow` and `Refuse`; the manager keeps it on the
turn context the way `withTurnRanMarker` keeps the ran flag. Under `AutoAllow`
and `Refuse` the gate is still published as opened and immediately resolved,
`By` set to `policy:auto_allow` or `policy:refuse`, so a watcher sees what
happened to it. With that, the five `bypass` re-decisions in the senders and
`serve`'s `defaultSender` are deleted: whether a prompt is needed at all is
decided once, in the agent, from the effective mode.

**Persistence.** A directory `pending_gates/` in the session bundle, one file
per gate (`<id>.json`), because a session can hold several at once (a parent's
own question and a background child's permission). The record carries what
`PendingPermissionRecord` carries today (`toolName`, `argsJson`, needed to
resume) plus the kind and the question payload. The legacy `pending_permission.json`
is read once when a session is loaded, as a gate of kind `permission`, and
removed when that gate resolves; the new binary never writes it, an old binary
ignores `pending_gates/`. A relayed child gate is never persisted
(`Persist: false`), as today, because a child cannot be resumed.

**Across processes.** A second process (a separate `coddy serve` next to a bare
`coddy` on the same home) reads `pending_gates/` from disk and reports those
gates with `answerable: false` while `TurnLockHeld` says another process holds
the session. Answering through the file system is out of scope; the setup this
design serves is one process owning the session with the others as its clients.

### 3.2 Clients of the bus, core side: inference executors

Every executor gets `m.Publisher(sessionID)` where it used to get the caller's
sender, and an `acp.GateKeeper` (the manager) where it used to call the sender
for a prompt. `AgentRunner` becomes
`func(ctx, state, prompt, bus TurnBus) (string, error)` with
`type TurnBus interface { acp.UpdateSender; acp.GateKeeper }`, so the runner
closure in `cmd/coddy`, `internal/serve` and `external/cli` hands the agent
both without capturing the manager.

- **Prompt turn** (`HandleSessionPromptWithSender`). The signature stays. The
  caller's `sender` is subscribed to the topic for the duration of the turn
  with `afterSeq: 0` and detached when the turn returns; `nil` means "no
  subscriber of my own". Inside, `a.server` is the publisher and `a.gates` the
  manager. Plan runs (`RunPlan`, `runPlanAdmitted`) take the same path.
- **Resume after a restart.** `runPermissionResume` in `external/httpserver`
  moves into the manager as `ResumePendingGate(ctx, sessionID, gateID, answer)`:
  it admits a turn through `BeginTurn` and calls a `GateResumer` installed by
  the same wiring that installs `AgentRunner`. `AnswerGate` tells a live gate
  from a persisted one itself, so the HTTP endpoint keeps a single call.
  Questions gain `agent.ResumeAfterQuestion`: the answers become the result of
  the pending `question` tool call, then `continueReAct`.
- **Background wake turn** (`external/httpserver/background_http.go`). A
  prompt turn with no subscriber of its own; policy `Ask` (decision 6.2).
- **Subagent child.** Runs on its own topic (the `sub_…` session); the task
  output sink that renders `→ tool` / `✓ tool` lines becomes a subscriber of
  that topic, and a browser could subscribe to it later. The child's gate opens
  on the **parent's** topic through a thin `GateKeeper` wrapper that stamps the
  child's effective mode, prefixes the title, drops the `allow_always`
  options, sets `Persist: false` and `Origin: subagent <name>`. The gate is
  bound to the **child's** context, not to the parent turn: a background
  child that outlives the turn which spawned it keeps its gate pending on the
  parent's topic, where the standing subscribers see it live and a late one
  gets it in the pending snapshot, and it resolves when the child is stopped
  or answered. That drops today's rule that a child's request after the
  parent turn returned is denied without reaching anybody
  (`permissionRelay.Request`, `docs/features/subagents.md`): the bus has a
  place for such a gate, and denying it was a consequence of having none.
  Questions from a child stay an error. The arbiter and its single slot are
  deleted: the registry holds any number of gates. The `Notification` hook
  that fires when a permission prompt is about to wait
  (`internal/agent/hooks.go`, `react.go:1202`) fires at the `OpenGate` call
  site, unchanged.
- **Scheduled job** (`external/scheduler/daemon/run.go`). A prompt turn with
  `GatePolicy: AutoAllow`; `autoAllowSender` is deleted.
- **Print mode** (`coddy -p`). A prompt turn with `GatePolicy: Refuse`; the
  print consumer keeps writing the refusal note to stderr.
- **Compaction** (`agent.CompactSession`). Publishes its one notice record to
  the bus, which is what the console path does today; the silent sender in
  `external/httpserver/coddy_compact.go` goes. Compaction takes the turn lock
  but does not register as an active turn, so the record reaches only the
  standing subscribers, which is today's behaviour in the console; the
  implementer verifies this before deleting the silent sender.

### 3.3 Clients of the bus, surface side: consumers and answerers

- **HTTP server** (`external/httpserver`). For `stream: true` the SSE
  serializer (today's `Sender`, kept as the frame encoder) is subscribed to the
  turn; frames are unchanged and `gate_resolved` is added. For `stream: false`
  nothing is subscribed on the response; the handler waits for the turn's
  result and writes the JSON body. `composer-stream` is the same serializer
  subscribed with `afterSeq` from `Last-Event-ID`; `id:` is the `seq`; the
  relay's own frame buffer is deleted and `composer_stream_relay.go` shrinks
  to the subscriber and writer plumbing. `POST …/permission|question` call
  `AnswerGate`, `By` taken from an optional `X-Coddy-Client` header (the SPA
  sends `web-ui`, the remote console `console`, the remote ACP process `acp`),
  `http` when absent. New `GET …/gates`. `TurnMirror`, `MirrorTurn`,
  `permissionhub.go`, `questionhub.go` and `permission_resume.go` are deleted.
- **Web UI.** On `gate_resolved` a prompt card moves to the resolved state
  with "answered from <By>" or "cancelled" instead of only after its own
  answer; on attach the SPA reads `GET …/gates` and reconciles with the
  shadow in `questionPromptSessionStore.ts`, which stays as the fallback for
  a server without the route. The session list's waiting marker is fed by the
  same state. Copy added to `en` and `ru` alike.
- **Console, local** (`external/cli`). The app's sender becomes a standing
  `Consumer` of its session, subscribed on `new`, `load` and every switch and
  detached from the previous one. `gate_opened` opens the modal; several gates
  queue behind the one on screen with the "N more waiting" line (PR #213's
  logic recast on bus records); `gate_resolved` takes the modal down with a
  dim "answered from <By>" or "cancelled" note. An answer goes through
  `mgr.AnswerGate(sessionID, gateID, answer)` with `By: console`. `permCh`,
  `questCh`, `sender.RequestPermission` and `sender.RequestQuestion` are
  deleted.
- **Console and `coddy acp` through `--remote`** (`internal/remote`).
  `Handler` is a client of the HTTP contract. On its own turn stream the
  `permission` and `question` frames are delivered to the local consumer as
  `gate_opened` **without blocking the SSE reader**; `gate_resolved` is
  delivered as is; the surface's answer becomes a `POST` by `gateId`, and
  `404` means "already answered elsewhere", not a failed turn. While idle the client
  subscribes to `GET /coddy/events` and, on `turn_started` for its session that
  it did not start, attaches to `composer-stream` from `seq` 0 and delivers the
  frames as records: a remote console follows, and can answer, a turn started
  from the browser. The console's `backend` interface gains `AnswerGate` and
  the subscription.
- **ACP server, local** (`internal/acp`). The default consumer of every
  session. `gate_opened` becomes `session/request_permission` or
  `session/request_question` (a map from JSON-RPC id to gate id); the editor's
  response becomes `AnswerGate`; `gate_resolved` drops the map entry, and an
  editor answer arriving after that is discarded on `ErrNoPendingGate` (the
  protocol has no way to withdraw a request, so a standard editor's dialog
  stays until the user closes it). The two new update kinds are forwarded as
  `session/update` like the other Coddy extensions (`token_usage`,
  `provider_usage`, `available_commands_update`), which editors ignore; the
  forwarded `gate_resolved` carries the JSON-RPC id of the outstanding
  request, so an editor that implements the extension can take its dialog
  down when the web answered first.
- **Telegram** (`external/gateway/telegram`). The bot's `Sender` becomes a
  consumer of the sessions its chats run, rendering text and tool markers as
  today; `gate_opened` posts "waiting for an answer in the web UI" with the
  title; the chat does not answer (decision 6.3); buttons in the chat are a
  later phase. Whether a gate exists at all is decided by the session's mode in
  the agent, as everywhere.
- **Swarm** (`external/swarm`). Unchanged: `composer-stream`, `/coddy/events`
  and the REST routes are proxied as they are, and `gate_resolved` passes as
  an opaque SSE frame.
- **Scheduler daemon.** An executor only; no consumer.

### 3.4 Wire contracts

Kept, so the SPA, the remote client and the swarm keep working through the first two PRs:

- SSE `event: permission` and `event: question` on the primary stream and the
  watch stream, with today's payloads; the primary `POST` stream carries no
  `id:`, the watch stream's `id:` is the bus `seq`;
- `POST /coddy/sessions/{id}/permission` and `…/question`, same bodies, `204`,
  `404` when nothing waits, `409` for a child session;
- `GET /coddy/sessions/{id}/composer-stream` with `Last-Event-ID` and `desync`;
- `GET /coddy/events` with `turn_started` / `turn_ended`;
- ACP `session/request_permission`, `session/request_question`, and the
  response shapes `PermissionResult.UnmarshalJSON` already accepts.

Added:

- `gateId` in the payload of `event: permission` and `event: question`, and
  accepted in the bodies of the two answer routes next to the legacy ids;
- SSE `event: gate_resolved` `{sessionId, gateId, kind, outcome, by, answer}`
  on both streams;
- `GET /coddy/sessions/{id}/gates` → `{object: "coddy.gates", gates: [{id,
  kind, sessionId, origin, openedAt, answerable, resumable, permission |
  question}]}`; `answerable` is false when another process holds the turn,
  `resumable` is true for a persisted gate with no live wait;
- optional request header `X-Coddy-Client` on the two answer routes;
- `GET /coddy/events`: `gate_opened` / `gate_resolved`
  `{object: "coddy.gate", sessionId, gateId, kind, phase}` (phase 3), so a
  client can mark a session as waiting without being attached to it.

### 3.5 `stream: true` and `stream: false`

The two flags mean the shape of the caller's own answer and nothing else, and
that stays:

- `stream: true`: the response is the SSE the caller reads today, produced by
  the same encoder from the same records, every existing frame unchanged,
  with `gate_resolved` as the one addition;
- `stream: false`: the response is the JSON body it is today; the turn is on
  the bus for watchers either way, which is already the case;
- the OpenAI direct-model paths (`owned_by` other than `coddy`) have no agent,
  no bus records and no gates, and are not touched;
- `models[].stream`, the transport towards the LLM, is unrelated and untouched.

A gate on a `stream: false` turn behaves as it does today unless the caller
opts in, decision 6.1.

## 4. Compatibility

**Nothing on disk changes shape except the pending-gate record.**

- `config.yaml`: no key added, renamed or removed. The schema, the reference,
  the `configure-coddy` skill and the site copy are untouched.
- Session bundle: `messages`, `tool_calls/`, `assets/`, `plans/`, `todos/`,
  `ui_log.json`, `permission_grants.json`, the turn lock and the cancel file
  are untouched. `pending_gates/` is added; `pending_permission.json` is read
  as a legacy gate and removed after it resolves.
- `serve.json`, the trust receipts, skills, the Telegram session store, the
  scheduler storage: untouched.
- `~/.coddy` layout: untouched.
- Ownership: a process answers only gates it holds live, or persisted
  gates it can claim. The claim is the session's turn lock, the same flock
  `BeginTurn` takes: `ResumePendingGate` acquires it first, then reads and
  removes the record, then runs, and the answer route returns `204` only
  after the lock is held and the record consumed; a lock held elsewhere is
  `409`, and the read-only view above is what such a client shows instead
  of a form. Checking `TurnLockHeld` and then acting would be a race between
  two processes over one bundle; the test for this runs two managers over
  one bundle and proves exactly one resume.
- Downgrade: an older binary ignores `pending_gates/`; a gate pending at the
  moment of a downgrade is lost with the process, which is what happens to a
  pending question today.

**Source compatibility is dropped on purpose.** `acp.UpdateSender` loses two
methods; every type that only existed to answer them is deleted; the gateway
adapter contract in `docs/surfaces/gateway.md` changes accordingly. A fork or an
out-of-tree adapter implementing the old interface stops compiling, which is
the intended signal.

## 5. Alternatives considered

- **Shared gates at the `coddy serve` layer only.** Add `gate_resolved` to the
  relay, make the remote client non-blocking and tolerant of `404`, route the
  mirrored turns' gates through the HTTP registries, persist questions. It
  delivers the operator's model for the chosen setup with a fraction of the
  churn and keeps PR #213 as the local half. Rejected because it leaves two
  homes for the same state (the HTTP registries and the console's channels),
  leaves the manager unaware of prompts on its own sessions, and every later
  surface would rebuild the fan-out. The operator asked for the core to look
  like a bus.
- **Keep the blocking sender methods and add a broadcast layer.** The
  manager would call every subscriber's `RequestPermission` concurrently and
  take the first result. Rejected: it keeps the wrong contract (a request that
  blocks the caller) and cannot represent "resolved elsewhere" to the losers.
- **Answer across processes through the bundle.** A `gate_answer_<id>.json`
  polled by the owner, like the cancel file. Deferred: it is orthogonal to the
  bus and only matters for a bare console next to a separate server, a setup
  `--remote` already covers; the read-only view keeps that setup honest.

## 6. Behaviour changes to decide before the gates PR

Each is a documented behaviour today; the recommendation is what the plan
assumes.

1. **A gate on a `stream: false` turn.** Today `NewRelaySender` auto-rejects a
   permission and errors on a question so "a headless caller is never left
   blocked waiting for a browser that may not be open". Recommended: keep
   `Refuse` as the default for `stream: false`, published as opened and
   resolved so a watcher sees the refusal, and let a caller that does want
   the browser to answer opt in with `metadata: {"gates": "ask"}` on the
   request; dropping the request cancels the turn as today, which cancels
   the gate. `Ask` by default would turn a script run without `bypass` into a
   silent hang in production, which the documented contract exists to
   prevent.
2. **Background wake turns and permission resumes.** Today non-interactive
   (denied unless the server's mode is bypass). Recommended: `Ask`; a woken
   turn is exactly the turn somebody wants to answer from the browser.
3. **Telegram under `ask` or `accept_edits`.** Today the chat auto-allows
   regardless of the mode (a narrowed child excepted). Recommended: the chat
   posts a notice and the web answers; an unattended bot sets `bypass`, which
   `docs/surfaces/gateway.md` already recommends. Alternative: keep auto-allow
   as a per-gateway option.
4. **`question` under `bypass`.** The report's second complaint: "why did it
   ask at all, I run bypass". Out of this plan; a product decision (suppress
   the tool, or auto-answer with no answers) that fits in the agent's toolset
   selection independently of the bus.
5. **`By` on the wire.** Recommended: the optional `X-Coddy-Client` header,
   at most 64 characters of letters, digits, space, dot, dash and underscore,
   anything else replaced by `http` before it is stored; rendered as plain
   text (React escapes it, the console runs it through `tui.SanitizeText`,
   so a terminal never sees a control sequence from it); a label, not an
   identity.
6. **Gate updates to ACP editors.** Recommended: forward `gate_opened` and
   `gate_resolved` as `session/update` like the other extensions, in addition
   to the protocol request. Alternative: keep them internal to the ACP server.

## 7. Files to change

New:

- `internal/acp/gate.go` (`Gate`, `GateKind`, `GateAnswer`, `GateOpenedUpdate`,
  `GateResolvedUpdate`, `DesyncUpdate`, `GateKeeper`, the update type names);
- `internal/session/bus.go`, `bus_test.go` (topic, log, `seq`, subscribers,
  replay, `desync`, retention, lifetime tied to `markTurnActive`);
- `internal/session/gates.go`, `gates_test.go` (registry, `OpenGate`,
  `AnswerGate`, `PendingGates`, first-wins, cancellation, policies,
  `ResumePendingGate`, `GateResumer`);
- `internal/session/gates_store.go`, `gates_store_test.go` (`pending_gates/`,
  the legacy record);
- `internal/agent/gates.go` (`SetGateKeeper`, the subagent gate wrapper,
  `ResumeAfterQuestion`);
- `external/httpserver/gates_http.go` (`GET …/gates`, `X-Coddy-Client`, the
  `gate_resolved` frame);
- `features/session_bus.feature`, `internal/session/bdd_session_bus_test.go`;
- `features/shared_gates.feature` with harnesses in `external/httpserver`
  (a gate answered from a second HTTP client, a mirrored Telegram turn's gate
  answered from the web), `external/cli` (a gate resolved elsewhere takes the
  modal down; two gates queue), `internal/remote` (a `404` on the answer is
  "already answered", the stream continues);
- `examples/httpserver/http_e2e_shared_gate.py`.

Changed:

- `internal/acp/sender.go` (one method), `types.go` (update kind constants),
  `server.go` (gate map, request translation, `AnswerGate`);
- `internal/session/manager.go` (`AgentRunner` and `TurnBus`,
  `HandleSessionPromptWithSender` subscribing the caller, `Publish` where
  `m.server.SendSessionUpdate` is used for turn records, `PromptRunOpts.GatePolicy`,
  `BeginTurn` carrying the policy), `run_plan.go`, `subagent.go`,
  `pending_permission.go` (reduced to the legacy reader), `turn_events.go`
  (the log lifetime hooks), `state.go` (`ForgetLiveSession` and deletion
  resolving gates);
- `internal/tooling/env.go` (`Gates acp.GateKeeper`), `internal/tools/question.go`;
- `internal/agent/react.go` (the permission gate through `a.gates`, the
  publisher as `a.server`), `subagent.go` (relay and arbiter replaced by the
  wrapper, the child sender as a subscriber of the child topic),
  `resume_permission.go`, `compact.go` (no change to what it publishes; the
  HTTP caller changes);
- `cmd/coddy/main.go` (`serverRef` loses the bypass logic and the two
  methods, the runner builds `TurnBus`), `internal/serve/runtime.go`
  (`defaultSender`, `SetTurnMirror`, `MirrorTurn` deleted), `cmd/coddy/serve.go`;
- `external/httpserver/bridge.go` (the encoder as a `Consumer`; `interactive`,
  the registries and the pending record go), `server.go` (subscribing the
  encoder for `stream: true`, nothing for `stream: false`), `composer_stream_relay.go`
  (buffer removed), `composer_stream_http.go` (`afterSeq`), `coddy_coddy.go`
  (`AnswerGate`), `background_http.go`, `coddy_compact.go`, `coddy_plans.go`,
  `serve_stub.go`, `openapi.go`, `events_hub.go` (phase 3);
- `external/cli/sender.go`, `app.go`, `updates.go`, `modals.go`, `backend.go`,
  `run.go`, `print.go`, `status.go` (the blocked phrase keyed on gate events);
- `internal/remote/prompt.go`, `handler.go`, `sse.go`, `rest.go` (`AnswerGate`,
  the idle attach through `/coddy/events` and `composer-stream`);
- `external/gateway/telegram/sender.go`, `bot.go`, `external/gateway/serve.go`;
- `external/scheduler/daemon/run.go`;
- `external/ui/src/ui/chat/consumeComposerSse.ts`, `App.tsx`,
  `PermissionPromptSection.tsx`, `QuestionPromptSection.tsx`,
  `questionPromptSessionStore.ts`, `types.ts`, `sessionRowActivity.ts`,
  `i18n/messages/en.ts`, `ru.ts`; rebuilt embedded assets.

Deleted:

- `external/httpserver/permissionhub.go`, `questionhub.go`, `permission_resume.go`,
  `turn_mirror.go`; `internal/session/turn_mirror.go`;
- the console's `permCh` / `questCh` plumbing, `printSender`'s prompt methods,
  `lateBoundSender`'s prompt methods, `planRunNoopSender`, `autoAllowSender`,
  `defaultSender`, the Telegram sender's prompt methods, `subagentSender`'s
  prompt methods, the arbiter.

## 8. Tests

### 8.1 Specifications

New in `features/`:

- `session_bus.feature`: two consumers subscribed to one session receive the
  same records; a consumer subscribing mid-turn replays the turn so far and
  then follows; a consumer asking from before the retained window is told
  `desync` first; a record published between turns reaches the subscribers
  and is not replayed; a gate opened by a turn is seen by both consumers, the
  second answers, the first sees it resolved with who answered, the turn
  continues; a cancelled turn resolves its gate as cancelled; `AutoAllow` and
  `Refuse` publish an opened and a resolved record; a persisted gate answered
  after a "restart" (a fresh manager over the same bundle) resumes the turn
  (permission runs the tool, question hands the answers to the model); a
  legacy `pending_permission.json` loads as a gate;
- `shared_gates.feature`: the surface-level happy paths listed in section 7.

Changed:

- `serve_turn_mirror.feature` is rewritten as the turns of every surface
  being on the bus: the browser watching a Telegram turn sees its permission
  gate and answers it; the chat sees the resolution. Its "only the chat is
  asked" scenario is deleted;
- `cli_tui.feature`: the queued-prompt scenario of PR #213 recast on bus
  records, plus "a gate answered from another client takes the modal down";
- `remote_client.feature`: a permission and a question answered elsewhere;
  the remote console attaches to a turn started from the browser;
- `subagents.feature`: the arbiter scenario ("never saw two permission
  prompts in flight at once") becomes "two children's gates are both
  pending on the parent and answered in either order";
- `background_permissions.feature`, `question_lenient_args.feature`,
  `acp_permission_answers.feature`, `ask_mode.feature`, `hooks_*.feature`,
  `composer_live_watch.feature`: scenarios unchanged, harnesses moved from a
  scripted sender to a scripted `GateKeeper` or a bus consumer.

### 8.2 Tests that break by construction

Every test double that implements `RequestPermission` / `RequestQuestion` and
every assertion that the agent called them. Extra methods on a double still
compile, so the failures are semantic and each file is visited:

- `internal/agent/react_test.go` (`resumePermissionSender`,
  `recordingPermissionSender`, `promptRefusingSender`, `todoSnapshotSender`),
  `subagent_test.go` (`blockingSender`, `relayAsSender`, the arbiter tests),
  `bdd_subagents_test.go` (`recordingClient`), `bdd_ask_mode_test.go`;
- `internal/tools/question_test.go` (`fakeSender`), `bdd_question_args_test.go`
  (`scriptedQuestionSender`), `plan_write_test.go`, `todo/todo_test.go`;
- `internal/permission/bdd_acp_permission_answers_test.go`,
  `bdd_background_permissions_test.go`;
- `internal/session/manager_test.go`, `manager_usage_test.go`,
  `provider_usage_test.go` (senders passed to the manager);
- `external/httpserver/bridge_test.go` (`TestRequestQuestionSSECompletesWhenPosted`
  and the permission twin), `bridge_relay_sender_test.go` (the
  non-interactive contract), `bdd_serve_mirror_test.go` (`chatSender`),
  `bdd_remote_client_test.go` (`recordingClientSender`), `server_test.go`
  (`noopSender`), `composer_stream_relay_test.go` and
  `composer_stream_resume_test.go` (the buffer moves to the manager);
- `external/cli/sender_test.go`, `chat_test.go`, `bdd_cli_tui_test.go` (the
  `permission` and `question` directives drive the bus instead of the sender);
- `internal/remote/remote_test.go` (`collectSender`, the question round-trip);
- `external/gateway/telegram/sender_test.go`; `cmd/coddy/agents_test.go`.

Also visited: `internal/agent/bdd_hooks_test.go` and `hooks_tool_calls.feature`
(the `Notification` hook on the permission path fires at the new call site),
`external/swarm` (`swarm_mount.feature` re-run with `GET …/gates` and the
`gate_resolved` frame passing through the mount), and the SSE readers of the
Python rigs, which must ignore an event name they do not know.

Python rigs to re-run, unchanged unless noted: `examples/cli/cli_e2e_permissions.py`,
`examples/acp/acp_e2e_*.py` (they still receive `session/request_permission`),
`examples/httpserver/http_e2e_*.py`; new `http_e2e_shared_gate.py`.

## 9. Documentation

- `docs/contributing/architecture.md`: a section on the bus, the two client
  families and the gate life cycle; the sentence on the gateway publishing
  into the relay rewritten;
- `docs/reference/http-api.md` and `external/httpserver/openapi.go`: the two
  answer routes (`X-Coddy-Client`, "answered elsewhere"), `composer-stream`
  (`id:` is the bus `seq`), `GET …/gates`, `gate_resolved`, the `stream: false`
  paragraph on gates, `/coddy/events`;
- `docs/reference/acp-protocol.md`: `gate_opened` / `gate_resolved` as
  extension updates, the late-answer rule;
- `docs/surfaces/web-ui.md` and `DESIGN.md` ("Tool permission gate", the
  prompt cards): the resolved-elsewhere state, the waiting marker source;
- `docs/surfaces/console.md`: modals as bus consumers, remote mode following a
  turn started elsewhere, the "answered from" note;
- `docs/surfaces/gateway.md`: the adapter contract (a consumer, no prompt
  methods), the permissions paragraph (decision 6.3), "the same chat live in
  the browser" gaining "and answerable there";
- `docs/surfaces/editors.md`: what an editor sees when the web answers first;
- `docs/features/subagents.md`: the relay and arbiter paragraphs (lines 172
  to 179 and 234, 246 today), the woken-turn paragraph;
- `docs/features/sessions.md`: the bundle table row for `pending_gates/`;
- `docs/operate/serve.md`: the turn mirror paragraph;
- `AGENTS.md`: the `internal/session` and `external/httpserver` rows;
- `docs/nav.yaml`: this record; `make docs` for the generated pages;
  `make site-docs` after the merge for `llms.txt` and `llms-full.txt`.

## 10. Delivery flow

Four pull requests, each green on `make test` and `make lint` with the tag
matrix on CI, each carrying its specs and its documentation. The cut is by
concern, not by surface, so no compatibility layer is ever written: the first
PR changes how updates flow and leaves the prompt methods untouched, the
second changes the prompts on every surface at once. A cut by surface would
need a shim that keeps `RequestPermission` alive for the surfaces not yet
moved, to be written and then deleted, and was rejected for that reason.

1. **`feat/session-bus`** - updates on the bus, no change to gates. `Publish`,
   `Subscribe`, the log, `seq`, replay, `desync`, the bounded queues and the
   drain barrier; the caller's sender subscribed for the turn. The agent
   still asks for prompts through its sender in this PR, and the initiating
   surface is still the one that answers, so the manager hands the runner a
   sender that publishes updates to the bus and forwards
   `RequestPermission` / `RequestQuestion` to the caller's sender - a
   composite of fifteen lines inside `HandleSessionPromptWithSender`, not a
   shim for surfaces, deleted by PR 2 when prompts move to the manager; the HTTP encoder as a consumer and the
   relay's buffer removed (`composer-stream` and `Last-Event-ID` on the bus
   `seq`); `TurnMirror` and `MirrorTurn` deleted, Telegram and the wake turn
   publishing like everybody; the console and the ACP server as standing
   subscribers. Every sender keeps its two prompt methods and the gates keep
   working exactly as today. Specs: `session_bus.feature` (subscribe, replay,
   desync, the drain barrier), `composer_live_watch.feature` and
   `serve_turn_mirror.feature` green on the new plumbing. Acceptance: a
   browser watching a Telegram turn and a `stream: false` script turn sees
   the same frames as before, with `id:` now the bus `seq`.
2. **`feat/session-gates`** - gates as session state. `acp/gate.go`, the
   registry, the policies, `pending_gates/` with the legacy record; the agent
   and the `question` tool on `GateKeeper`; the subagent wrapper replacing the
   relay and the arbiter; `RequestPermission` / `RequestQuestion` deleted from
   the interface and from every implementation; the HTTP registries and the
   resume moved into the manager, `gate_resolved` on both streams, `GET
   …/gates`, `X-Coddy-Client`; `ResumeAfterQuestion`; the console modal as a
   bus consumer with the queue; the ACP request map; print mode and the
   scheduler on policies; Telegram posting the notice; the SPA card's
   resolved-elsewhere state, copy in both locales, rebuilt assets and
   screenshots; `internal/remote` moved to the gate contract in the same
   PR, because its turn stream calls the local surface's prompt methods
   (`prompt.go`, `onPermission` and `onQuestion`) and would not compile
   without them: the `permission` and `question` frames are delivered to
   the local consumer as `gate_opened` without blocking the reader,
   `gate_resolved` is delivered, the answer is a `POST` by `gateId`, and a
   `404` is "answered elsewhere"; the console's `backend` gains
   `AnswerGate`. Only the idle attach stays for PR 3. Specs:
   `shared_gates.feature`, the rewritten `serve_turn_mirror.feature`,
   `cli_tui.feature`, `subagents.feature`, the answered-elsewhere scenarios
   of `remote_client.feature`. Acceptance: the report's scenario
   (a question waiting while the console shows nothing) impossible by
   construction, and a question answered from the web while the console
   shows it takes the console's modal down.
3. **`feat/remote-bus-client`** - the remote client following turns it did
   not start: the idle attach through `/coddy/events` and `composer-stream`,
   the `/coddy/events` gate frames, the console's `backend` subscription;
   the attach scenarios of `remote_client.feature` and `cli_remote.feature`;
   `docs/surfaces/console.md` (remote mode) and `editors.md`.
4. **`feat/telegram-gate-answers`** (optional) - inline keyboard answers in the
   chat, the gateway becoming a standing subscriber of its sessions so a gate
   from a browser-started turn is announced too.

PR #213 stays closed; its scenario and its four unit tests are the acceptance
tests of the console consumer in PR 2.

## 11. Risks

- **Size of PR 2.** It touches every surface, and cannot be cut further
  without a shim. Mitigation: PR 1 has already moved the plumbing under it,
  the specs are written first, each surface is one commit, and the tag
  matrix on CI walks every build combination.
- **A slow consumer.** It cannot stall the turn: the bus owns the queues and
  desyncs a subscriber that falls behind. What it can do is exhaust the drain
  barrier at turn end, which costs the turn two seconds and the consumer a
  `desync`; the bound and the outcome are stated on `Subscribe` and checked
  by a spec.
- **Memory and the bound.** One bounded log per running session, 512 KB,
  the relay's bound today, measured over the same content (the SSE frames
  carry the full tool-call arguments and result previews the records do),
  so a late subscriber desyncs exactly when it does today and reloads the
  transcript the same way. Storing large tool results by reference to the
  bundle is a later optimisation, not part of this plan. Cleared at turn end.
- **Behaviour changes 6.1 to 6.3** alter documented behaviour for scripts,
  woken turns and unattended bots. They are decided before the gates PR and
  written into the pages listed in section 9 in the same PR.
- **The resume contract.** `Last-Event-ID` and `desync` are covered by
  `composer_stream_resume_test.go` today; those tests move with the buffer and
  must stay green on the manager's log.
- **The ACP editor cannot be told to close a dialog.** Documented; the late
  answer is dropped, nothing runs twice.
- **Swarm passthrough.** A new SSE event and a new route pass as opaque HTTP;
  `swarm_mount.feature` is re-run to prove it.
- **Surfaces that only exist in some builds.** The tag matrix compiles every
  combination, but a semantic regression in the gateway is only exercised
  by the `gateway` jobs; the Telegram scenarios of `shared_gates.feature`
  run under `gateway` alone and under `http,gateway`, where the browser
  answers a chat turn's gate.
- **On-disk legacy.** A pending permission from an old binary must still
  resume under the new one: covered by the legacy scenario in
  `session_bus.feature`.
- **Concurrency.** First-wins and cancellation are exercised under `-race`
  with two answering goroutines in `gates_test.go`; the persisted claim
  with two managers over one bundle in `gates_store_test.go`; delivery
  under concurrent publishes in `bus_test.go`.

## 12. Cross-review (2026-09-12)

Three external agents reviewed the record as a design, each with the plan
inline and, for two of them, the checkout to verify it against: Codex
(`gpt-5.6-sol`, reasoning high, through the `codex-review` plugin), Cursor
Agent (model `auto`) and Coddy (`neuraldeep/qwen3.8-27b`, no tools). All
three answered `CHANGES_REQUESTED` on the first version; every finding was
checked against the code before it changed the text above.

Accepted, and where it landed:

- delivery must not run consumer code on the publishing goroutine (all
  three): the bus now owns per-subscriber bounded queues with one delivery
  goroutine each, `desync` on overflow, and a drain barrier at turn end so
  the ACP `session/prompt` response, the HTTP JSON body and the console's
  `turnDone` still follow the last record (3.1);
- a late subscriber must see the gate even when its record was trimmed
  (Cursor, Codex): `Subscribe` enqueues a snapshot of the pending gates after
  the replay, under the topic lock (3.1);
- gate identity (Codex): ids are minted by the manager, provider ids are
  index keys, file names never carry provider text; `toolCallDir` in
  `internal/session/toolcalls_store.go` has the same exposure today and is
  filed separately (3.1);
- the turn's end is a record (Codex): `turn_finished` closes the epoch and
  the encoder renders today's error, `coddy_meta` and `[DONE]` from it
  (3.1);
- one subscription per consumer and topic, the console and the ACP server
  passing `nil` as the turn sender (Codex) (3.1);
- exactly-once resolution, answers validated before the transition, the
  HTTP error table, refusal when another process owns the session (Codex,
  Coddy) (3.1, 4);
- the persisted record survives a shutdown or a crash but not an
  operator's cancel, the outcome published as `suspended` (Coddy) (3.1);
- a background child's gate is bound to the child, not to the parent turn,
  and stays pending on the parent's topic (Cursor) (3.2);
- `stream: false` keeps `Refuse` by default with a per-request opt-in
  (Cursor, Coddy) (6.1);
- the PRs are cut by concern so no shim is ever written, and persistence,
  `ResumeAfterQuestion` and `GET …/gates` land together (all three) (10);
- `X-Coddy-Client` bounded and sanitised, the `Notification` hook, the
  swarm passthrough of the new route, the gateway-only build, the ACP
  `gate_resolved` carrying the request id (all three) (3.3, 6.5, 8, 11).

Codex reviewed the revised record a second time and again answered
`CHANGES_REQUESTED`, with seven findings, all verified and folded: the
composite sender PR 1 needs while prompts still live on the surfaces, and
the remote client's move into the gates PR, because `internal/remote/prompt.go`
calls the prompt methods and would not compile without them (10); `gateId`
on the wire, since two children answering by the same provider id would
both be refused (3.1, 3.4); the original `seq` on a re-sent `gate_opened`
and the snapshot after an overflow (3.1); `ErrGateSuspended` leaving the
loop without recording a result, since `react.go:1205` records "permission
denied by user" for every non-approval today (3.1); the persisted claim
being the turn lock, taken before the record is consumed (4); records as
immutable snapshots with their encoding computed once (3.1). The review
loop was stopped there rather than run a third time: the remaining
disagreements below are recorded, and the record is a plan for later
implementation, not a merge gate.

Not taken:

- "make every consumer non-blocking by contract" (Cursor, Coddy): moving the
  queues into the bus makes the property structural instead of a rule each
  consumer has to remember;
- cutting the gates PR by surface (Cursor): it needs a shim that keeps
  `RequestPermission` alive for the surfaces not yet moved, which the
  constraints exclude; the cut by concern gives two reviewable PRs without
  it;
- lowering the 512 KB bound or trimming large fields from records (Coddy):
  the bound covers the same content the relay buffers today, so `desync`
  behaves as it does now; storing results by reference is noted as a later
  optimisation.
