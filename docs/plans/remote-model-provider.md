# Plan: models shared by a remote Coddy (provider type `coddy`)

Status: design record; phase 1 is implemented on this branch (config, llm client and wire, server routes, agent and session wiring, dry-run, docs; see 6b), phases 2-4 are not. Branch `feat/remote-model-provider`, based on
`upstream/main` (`coddy-project/coddy-agent`). Written after a code survey of this repository and a web survey of LLM gateways, protocols and Go libraries (2026-10-07). Facts
about this repository were checked against the code; facts about third-party projects (versions,
licences, terms of service) were collected by research subagents and not re-verified, and the
unverified ones are marked **[unverified]**.

## 1. Goal

A local Coddy lists, among its models, models that live on a remote host that also runs
`coddy serve`. The whole harness stays local: system prompt, rules, tool definitions, message
history and the tool-call loop. Only one inference call per model turn crosses the wire, and the
remote Coddy answers it with the provider it is configured for.

Requirements from the operator:

1. **Every provider type works**, `openai`, `anthropic`, `neuraldeep`, `codex` and `devin`
   included, as long as the remote operator marks the model as **shared** for other Coddys (and, for a
   subscription login, acknowledges that explicitly, 4.1).
2. **Both reachabilities**: directly (`https://host:port`) and through a swarm relay mount
   (`https://relay/swarm/nodes/<node>`), whether the node is reached by the relay directly or over
   its reverse tunnel.

This is a *remote model*, not a *remote agent*. `--remote`, `POST /v1/responses` with the
`agent` / `plan` / `ask` profiles, ACP over HTTP and A2A all run the loop on the remote side, which
is the opposite of the goal. MCP sampling goes the wrong way (a server borrowing the client's
model) and is reported deprecated in the 2026-07-28 spec **[unverified]**.

## 2. What the repository gives today

- **Direct completion** (`POST /v1/chat/completions` with `model` = a `models[].model` selector,
  `external/httpserver/server_direct.go`): one `llm.Provider` call, no ReAct loop, the caller's
  tools and history pass through. A local `type: openai` provider with `api_base` on a remote
  `/v1`, or on `<relay>/swarm/nodes/<node>/v1`, already works as a remote model.
- **What that path loses**, which is the reason for this plan:
  - `ReasoningSignature` is dropped in both directions (`directAssistantMessage`,
    `openAIMessagesToLLM`), so Anthropic thinking blocks and the Codex reasoning carrier do not
    survive a hop.
  - `usage` reports prompt and completion tokens and, since pull request #468 (merged into
    `upstream/main`: `features/openai_nonstream_usage.feature`, cached tokens on direct usage
    streams), the cached prompt tokens; reasoning tokens are not reported, and the client side of a
    hop still reads only prompt, completion and total.
  - Errors lose structure: the provider status and `Retry-After` are in the body or header only in
    a blocking answer; a stream error arrives in-band under HTTP 200; the quota reset time never
    reaches `agent.wait_for_limit_reset`.
  - `max_context_tokens`, `multimodal` and `reasoning_levels` from `/v1/models` are not read by the
    client, so the context window is always the 128000 default and the reasoning levels of
    `remote/codex/gpt-5.5` are not detected (detection looks at the API model id).
  - Reasoning level `off` is sent as `none` and the remote refuses it with 400.
  - Every call resolves a session on the remote: SessionStart hooks run, the whole history is
    written to the remote disk, and without `X-Coddy-Session-ID` each step is a new session.
- **Swarm transport needs no new code.** `mountedPrefixes` allows `/v1/` and `/coddy/`, the mount
  is an `httputil.ReverseProxy` with `FlushInterval: -1`, the tunnel is prior-knowledge HTTP/2 over
  a hijacked connection (`external/swarm/mount.go`, `internal/swarm/tunnel.go`).
- **The relay replaces the caller's credential with the node's token**
  (`rewriteFor`, `external/swarm/mount.go`) and uses the same token for its own fan-out calls, the
  aggregated session list among them. A node behind a mount cannot tell clients apart and cannot
  enforce a per-client scope. See 4.5 and 4.5a.
- **`llm.Provider` is two methods** (`Complete`, `Stream`) over `[]Message` and
  `[]ToolDefinition` (`internal/llm/provider.go`). The agent already projects everything the
  provider needs (rules, images, turn context) into those two arguments, so they are a complete
  description of a model call.

## 3. What the market does, and what fits

| Approach | Examples | Verdict |
|---|---|---|
| Native passthrough of the upstream wire | Bifrost `/openai_passthrough`, `/anthropic_passthrough` (Go, Apache-2.0); GoModel (Go, MIT, 0.x) | Best fidelity for API-key providers. The remote has to know each upstream's headers and auth, and `codex` / `devin` OAuth and RPC backends cannot be reproduced by a plain reverse proxy; the local `codex` and `devin` clients also ignore `api_base`. |
| Translating facade | LiteLLM, Agent Router (formerly Envoy AI Gateway), CLIProxyAPI | Lossy: re-serialising drops reasoning signatures and `redacted_thinking`, rewrites call ids, breaks prompt-cache prefixes. LiteLLM had a PyPI supply-chain compromise in 2026-03 **[unverified]**. |
| Subscription bridges | CLIProxyAPI, copilot-api, claude-code-router | Terms-of-service risk, see 6. |
| Agent protocols | ACP, A2A, MCP | Remote agent, not remote model. |
| **Message-level wire between two Coddys** | this plan | Fidelity equals the local one by construction; works for every provider type; no new dependency. |

The decisive argument for the last row: a local Coddy already sends every history through
`llm.Message` for every provider type. Whatever that type fails to carry is already lost locally,
so a wire that carries `llm.Message`, `llm.ToolDefinition`, `llm.StreamChunk` and `llm.Response`
field for field makes a remote model behave exactly like a local one, and the remote side calls
its own `llm.Provider` of whatever type the model is configured with.

Wire-format facts that drive it: Anthropic thinking and `redacted_thinking` blocks and their
signatures must come back byte-identical; OpenAI Responses with `store=false` needs the encrypted
reasoning items replayed; OpenAI prompt-cache routing is sticky by prefix hash, so rotating
accounts or keys kills it; and Responses stream events echo the whole `instructions` and `tools`,
hundreds of kilobytes for a harness, so an SSE reader must lift the 64 KiB line limit.

## 4. Design

### 4.1 Sharing: `models[].shared_as`

A new key on a `models[]` row of the **remote** config: `shared_as: "<alias>"` offers that row to
other Coddys under the alias, and an empty or absent key keeps the row private. One key is both the
switch and the name, so there is no "shared without a name" state. Only shared rows are listed and
served by the new routes. The existing direct-completion path keeps its behaviour and is **not**
made safer by this key (see 4.5).

- **The alias is the only name that leaves the host.** The listing `id`, the `model` of a request,
  the usage route and every error name the alias, never the `provider/model` selector. An alias
  matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` (no slash, so a local selector `<provider>/<alias>`
  splits cleanly) and is unique across `models[]`; the loader and `coddy -t` report a clash naming
  both rows with `file:line:col`.
- **Subscription acknowledgement is mandatory.** A row whose credential is a subscription login
  needs `shared_subscription_ack: true` next to `shared_as`: provider types `codex` and `devin`
  always, and `neuraldeep` when the provider row has no explicit `api_key` (the stored hub login
  fills in, `neuralDeepEffectiveKey`). Without it the configuration is **refused at load** (an error,
  not a warning) with a message that says sharing a login hands its quota to every holder of a
  shared-model token and may breach the vendor's terms; `coddy -t` reports the row with
  `file:line:col`, and Settings shows the same text beside the checkbox. The key has no effect
  without `shared_as`.
- A row whose provider is of type `coddy` cannot be shared in v1 (see 4.7).
- Surfaces: `internal/config` structs, `config.schema.json`, `UISchemaMap`, the Settings model row
  (alias field and acknowledgement checkbox), both i18n dictionaries, `config.example.yaml`,
  `configure-coddy/SKILL.md`, `docs/reference/config.md` through `make docs`, `make site-schema`.
  Optional keys are safe to publish to the site at once.

### 4.1a Concurrency: `httpserver.shared_models.max_streams`

At most **5** shared-model calls run at once per *key* (default 5, configurable). The key is a hash
of the bearer the request presented, any token class, or `anonymous` when auth is off; the count
covers all aliases of one key. A further call is refused at once with `429`, `kind: busy` and
`Retry-After: 1`, before anything is sent to the provider.

- **The slot is taken right after authentication and before the body is read**, so a busy remote
  answers without reading up to 32 MiB, and a `Content-Length` above the limit answers `413`
  before any read. A `400`, `404` or `413` releases the slot the moment it is answered. The slot
  is released by `defer` once `provider.Stream` has returned: on completion, on a provider error,
  on a client disconnect, and when the body read deadline P expires. P is **30 s** (set with `http.NewResponseController`), below the smallest idle limit of a hop (4.2). The count of a key is one value under a mutex (or a compare-and-swap loop): the test against the limit and the increment are **one atomic step**, otherwise concurrent calls overshoot the limit. A slot is released **exactly once** per successful take, by one release function guarded by a flag (or `sync.Once`) that the `400`/`404`/`413` paths and the `defer` both call, and the body buffer is dropped no later than the release. *(model `m1-slots`)*
- Through a relay the node sees one credential, so the limit then covers every client of that relay
  together (see 4.5).
- A blocking mode would need a separate rule for the limit and has no heartbeat, so the wire has none (4.2).
- **`httpserver.shared_models.max_call_ms`** bounds one call of a row with `stream: false`, from the start of the provider call to its result: default **1800000** (30 minutes), at most **28800000** (8 hours, the operator's ceiling): a larger value is refused at load, and `0` means the 8-hour ceiling, never "no bound", so a hung blocking row always releases its slot. It is on by default because a hung blocking row would otherwise hold its slot until the client disconnects, and a client that receives only heartbeats never disconnects. A streamed row is bounded by S per gap (4.2) and is not cut by this key. On expiry the call ends as `error{kind: upstream, cause: timeout}`. `coddy -t` warns about a `max_call_ms` above 1800000 together with a shared `stream: false` row that has no `timeout_ms`. The value is to be checked against the longest non-streamed completions of the supported providers before release. *(decided by quorum, 6a)*
- The client sends `Expect: 100-continue`, so a refused call does not upload the history; where a
  hop strips it, the cost of a retry is a re-upload of the history and the documentation says so.

### 4.1b Waiting for a slot: `busy_wait_ms`

The client waits out `busy`; the server never queues. Two keys, **the provider's wins over the global one**:

- `providers[].busy_wait_ms`: the total time a call to this provider waits for a free slot. A value
  above zero applies; zero or absent falls back to the global key.
- `agent.shared_busy_wait_ms`: an **optional** integer, the global default: absent means 30000, an
  explicit `0` means no waiting. The effective budget is the provider's value when it is above zero,
  otherwise the global one (so a provider above zero waits even when the global key is `0`, and "no
  waiting for one provider only" is written as global `0` plus a positive value on the others).

**The wait lives inside `provider.Stream` of the `coddy` type** (decided by
`remote-model-provider-models/m2-busy-wait.md`), so every caller waits: the agent loop, compaction,
the direct completion routes, prompt enhancement and children, because `Complete` is `Stream`. The
call sends the request and on `busy` sleeps and sends again under one monotonic deadline
`start + budget` that covers the requests, the sleeps and the jitter of the admission only: once the remote answers `200` it no longer applies. The deadline belongs to one
`Stream` call of the outer wrapper and travels in the context (as `RetryAllowance` does), so a
transport failure after a successful wait does not start a new wait; otherwise a call could wait
(`llm_retry_max` + 1) times `busy_wait_ms`. Each sleep is the larger of `Retry-After` (at least a second) and a backoff that doubles from 1 s to a ceiling of 5 s, gets about 20% jitter **upward only**, and the last sleep is cut to what is left;
one wait is at most 9 requests at 30 s. A budget of zero fails at the first `busy`, after one
request. A slot that frees inside the budget is taken at the next request, at the latest the one made
at the deadline.

The spent wait fails the call with a typed `*coddyBusyError` of status zero, and that error is
**terminal on every layer**: `httpStatusFromError` returns 0, `isRetryableLLMError` returns false by a
typed branch (not by what its message contains), `IsTransientProviderError` is false (the agent's
provider recovery must not re-issue it), `UpstreamStatus` is 0. A restart by the wrapper or by the
recovery path would multiply the wait. The wait does not count against `agent.llm_retry_max`, does not
touch the retry allowance and is cut by Stop within the same tick (every sleep selects on the context).

**The agent's first-token timer is not armed for a `coddy` row** (decided by quorum, 6a item 1). The
remote owns model progress: it applies its stall guard S from the start of the provider call and
bounds a blocking row with `httpserver.shared_models.max_call_ms` (4.1a); the client keeps the
byte-level liveness guard I and the request bound R of 4.2. So the wait for a slot is never cut by a
timer, `busy_wait_ms` has no relation to `agent.llm_first_token_timeout_ms`, and `llm.WithFirstTokenArm`
does not exist. The agent decides this with a field of its own on the transport (`firstTokenGuard`,
false for a `coddy` row), not by clearing `streaming`, which also drives the reasoning clock;
`ProviderInput.CallBudget` stays unset for a `coddy` row, so `retry_budget_ms` carries only what remains
of the retry budget under `agent.wait_for_limit_reset`. The context carries one callback, with which the
wait reports its countdown; the agent shows it the way a limit wait is shown. Stop cancels the wait, the
upload and the stream through the context. The upload between the slot and the `200` is bounded by P
(4.1a) on the server and by R on the client.

Bounds (model `m2`, read for a row with no first-token timer): one wait is at most 9 requests; a `coddy`
row has no first-token retry, so a call makes one wait; the deadline bounds **admission only**: once the
remote answers `200` it no longer applies, and the stream ends by its `final` or `error` frame, by S or
`max_call_ms` on the remote, by I on the client, or by Stop. A slot that frees inside the budget is taken
at the latest by the request made at the deadline **when no other caller takes it first**; under
contention the wait can end in `busy` although slots were free in between.

### 4.2 Server (`//go:build http`, `external/httpserver`)

Three routes under `/coddy/`, which a relay mount already carries. They are always registered. A request is judged by the live authentication policy: `authGate` takes **one** snapshot of it per request (`authPolicyNow()`), stores it in the request context, and the handler of the three routes reads that snapshot and never re-reads the configuration, so a hot reload takes effect on the next request without a restart and a request that straddles a reload is answered by one policy, never a mixture of the two. *(model `m4-auth-relay`)*

- `GET /coddy/llm/models` answers `{protocol, data[]}`, shared rows only. A row carries the alias as
  `id`, `revision`, `max_context_tokens`, `multimodal`, `reasoning_levels`, `reasoning_default` and
  `allow_reasoning_off`. `revision` is an opaque string that changes whenever anything a client can
  observe about the row changes: any of those fields, or the model the alias points at. It is derived
  with a keyed hash so that the upstream model id cannot be recovered from it, and it is the same before
  and after a restart of the remote while nothing changed. There is no `stream` field: the wire is
  always a stream.
- `GET /coddy/llm/models/{alias}/usage` (**phase 2**) answers a **projection** of the usage the
  remote already builds, never that document: `{supported, account_wide, stale, windows[{id, label,
  used_percent, resets_at, exhausted}], blocked, blockers, retry_in_s}`. The provider name, type,
  plan, key name, wallet amounts and every list of upstream model ids are excluded
  (`acp.ProviderUsageUpdate` carries all of them); a block that concerns a model is reported only
  for the alias of a shared row, under the alias. An unknown or unshared alias answers 404 and
  names no provider. The values are the account's, so every holder of a shared-model token learns
  how much quota is left.
- `POST /coddy/llm/completions` takes `{protocol, model, messages[], tools[], options}` and answers
  **SSE only**. `protocol` is an integer compared strictly; a mismatch answers `invalid` and names
  both versions. `model` is the alias; a `provider/model` selector is a 404.

`options` carries only what the client sets explicitly (absent means "as the row is configured"):
`max_tokens`, `temperature`, `reasoning_effort`, `retry_budget_ms`. An omitted `reasoning_effort`
means the row's `reasoning_default` on the remote, and `off` is accepted only when the row has
`allow_reasoning_off`. `max_tokens` is the smaller of the client's value and the row's ceiling.
`RequestOptions.Validate(providerType)` runs on the server and its `400` names the alias, so a
client that never sets `temperature` never trips it for a codex row. `retry_budget_ms` is the
caller's remaining call budget (the first-token timer, the rest of the retry budget under
`agent.wait_for_limit_reset`); the server puts it into the provider it builds, so a remote wrapper
turns a long named pause into `error{kind: quota, reset_at}` at once instead of sleeping past the caller's timer.

`options.expected_revision` is the `revision` of the row in the listing the client last read; absent means no check. When it differs from the row's current revision the call is answered `400` `kind: invalid`, `code: stale_revision`, with the current `revision`, before the provider is built: no upstream request is made. The remote's own validation of `reasoning_effort` (`off` without `allow_reasoning_off`, a level the row does not list) runs after that check and stays as the second line of defence. *(model `m6-capabilities`)*

The DTO mirrors the part of `llm.Message` a provider sees: role, content, image parts (data URL,
MIME type, name), reasoning, reasoning signature, tool calls, tool call id, and
`llm.ToolDefinition`. These stay **local**: `Artifacts`, `ReasoningDurationMs`, `Model`,
`CreatedAt`, `PlanDocument`, `CompactionSummary`, `BackgroundWake`, and the absolute paths
`ImagePart.FilePath` and `ThumbnailPath` (a local path must not reach another host). `Rules` is
folded into `Content` and cleared by the agent before the call (`internal/agent/rules_activation.go`)
and nothing in `internal/llm` reads it, so it is not a wire field either.

**Reasoning signatures** leave the remote inside an *envelope* that carries a tag derived (like `revision`) only from the model the alias points at, not from its capabilities. On the way in the remote opens the envelope; a signature whose tag is not the current model's, or that has no envelope, is **dropped** from the message before the provider is built (the Anthropic provider already omits a thinking block without a signature), and it is never an error. The remote model receives a valid signature unchanged. An image part for a row that does not list `multimodal` is passed to the provider as it is; the provider's own refusal comes back as `invalid`. The envelope is part of the first wire version: a signature format cannot be changed under stored histories later. *(model `m6-capabilities`)*

**Frames.** One line per frame, `data: {"type":"chunk"|"final"|"error", ...}`, with no `event:`
field; the client reuses the existing SSE scanner (`newSSEScanner`, already 32 MiB per line) and
refuses a frame above 16 MiB. The response carries `Cache-Control: no-cache` and
`X-Accel-Buffering: no` and is flushed after every frame.

The stream is a state machine:

- `chunk`: a notification mirroring `llm.StreamChunk` field for field. Chunks are progress only, but
  the client keeps what they built, because a provider that fails mid-stream returns a partial
  `*Response` with its error and drops the tool calls it had not finished.
- `final`: the authoritative `llm.Response` exactly as `Provider.Stream` returned it. The client
  returns it as the result of its own `Stream` and never appends it to what the chunks built or runs
  a tool call twice. There are no reasoning-token counts in v1, because `llm.Response` has none.
- `error`: `{status, kind, message, retry_after_s, reset_at, emitted, cause}`, `kind` one of `busy`,
  `rate`, `quota`, `upstream`, `invalid`, `auth`; `emitted` says whether any chunk went out before
  it; `cause` is `status`, `stall`, `truncated` or `timeout`. A stall **after** the first chunk is a terminal
  `error{kind: upstream, emitted: true, cause: stall}` too, and neither the remote nor the client's wrapper repeats the call after output (the agent's provider recovery may continue the step after a pause with the kept partial answer, as for a local model, 4.3). `cause` is meaningful for kind `upstream` only (`status` for the other kinds); `timeout` is the `max_call_ms` of a blocking row. `status` is informational: the client decides by `kind` and `cause`. `emitted` counts chunk frames; heartbeat comments are not chunks; the client keeps its own count of chunks passed to `onChunk` and never trusts the frame's field.
- `message` is built **locally from `kind` and `status`** ("upstream provider returned 502"), never
  from `err.Error()` and never by substituting names in upstream text: `labelledProvider` prefixes
  every provider error with `provider "<name>" (<api_base>)`, and an upstream text can carry a
  dated snapshot id, a URL, an organisation id or a quotation of the request. What is useful in an
  `invalid` error (a context limit, say) travels as parsed numeric fields.
- Exactly **one terminal event**, `final` or `error`; anything after it is discarded; EOF without
  one is a truncated stream and a transport failure.
- **Three timers, three jobs** *(model `m5-guards`)*. *Model progress* is the remote's: it applies its
  `StreamIdleTimeout` S to a streamed row **from the moment the provider call starts**, so an upstream
  that accepts the request and never answers ends as `error{upstream, cause: stall, emitted: false}`
  after S (the standard byte guard arms only on the first byte read, so the handler arms it
  explicitly); a `stream: false` row is not guarded by S, and its call is bounded by
  `httpserver.shared_models.max_call_ms` (4.1a), so that a hung blocking row cannot hold a slot for
  ever. *Liveness of the remote and of the path* is the client's: the byte-level guard of
  `NewProvider`'s HTTP client, armed by the first body byte, counting **every** byte, comments included
  and never only event frames, firing when no byte arrives for I. The remote writes the comment `: hb`
  and a blank line as the first body bytes, right after the headers, and then so that no two bytes of
  the response are more than H = 15 s apart. Constraints, repeated in the config reference and checked by `coddy -t`: `H + J < I`, where J, the
  jitter of the byte gap, is assumed below H, so `I >= 2H` gives it; `coddy -t` warns when `I = 0` or
  `0 < I < 2H` for a `coddy` row (`I = 0` switches the client's liveness guard off, and the model shows a
  dead remote is then not detected); `H + J < L` and the body-read deadline `< L` for every hop of idle
  limit L (nginx `proxy_read_timeout` 60 s, Cloudflare 100-125 s; the plan states `H <= L_min / 3` with
  `L_min = 60 s`, which lets two beats be late); a dead remote is cut `I - H - J + 1` to `I` after the
  moment it died (exactly `I` after its last byte) and only after the first body byte; before the
  headers the call is bounded by a request bound R = P + 15 s counted from the start of the upload (a
  timer of the provider that cancels the request: Go's `ResponseHeaderTimeout` starts only after the
  body is written). The remote flushes the headers and the first heartbeat right after it has read and
  checked the body, before the provider call starts. *The agent's first-token timer* is not armed for a
  `coddy` row (4.1b), and the provider applies `StreamIdleTimeout` to a `coddy` row whatever
  `models[].stream` says: the wire is always a stream.

The handler is **stateless**: no `resolveSession`, no SessionStart hooks, no rules, nothing written
to disk, no turn lock. It builds the provider through the factory the direct path uses
(`makeLLMFromYAML`), so `providers[].proxy`, resilient wrapping and credential resolution are the
remote's own. The client's disconnect cancels the upstream call. The log records alias, kind,
status, duration and a request id, never content; a raw upstream text appears only at debug level
and may quote the request. The server has no `WriteTimeout`; each write gets its own deadline of about 60 s, refreshed before the write, so a write that blocks (the peer's window and the socket buffers are full) is cut, the upstream call is cancelled and the slot is released within the write deadline W (about 60 s). A peer that vanishes without a trace while only heartbeats are written does not block a write, so neither deadline frees its slot. The slot is then held until the call ends: by its `final` or `error` frame, by S per gap, by `max_call_ms` for a blocking row, or by the kernel giving up on the unacknowledged connection (a direct listener is plain HTTP/1.1 over TCP, so only TCP's own retransmission timeout and keepalive apply; the swarm tunnel already pings at 30 s read-idle and 15 s timeout). **Phase 2** adds a probe of the call's connection that cancels the call (a user timeout on the direct listener's sockets where the platform has one), with a bound of 45 s from the peer's disappearance **on the leg the node sees**; through a relay the node's peer is the relay, and the relay's own downstream probe is hardening (phase 3). A call that never returns holds its slot until the client disconnects: a row with `stream: false` is not stall-guarded and `providers[].timeout_ms` is 0 by default, hence `max_call_ms`, above the longest legitimate call, which cancels the provider context into `error{kind: upstream, cause: timeout}` (4.1a); `coddy -t` warns about a shared row with `stream: false` and no `timeout_ms` while the effective `max_call_ms` is above 1800000 (an explicit `0` is 8 hours). *(models `m1-slots`, `m5-guards`)* Proposed constants, to be confirmed: request body at most 32 MiB, on the client
one SSE event at most 16 MiB; past either the call fails with a non-retryable error, since the
remote is an untrusted host as far as the client's memory goes.

A reflection test walks the exported fields of `llm.Message`, `llm.ImagePart`,
`llm.ToolDefinition`, `llm.StreamChunk` and `llm.Response` and fails when one is neither in the
DTO nor in the local-only lists above.

`openapi.go`, `docs/reference/http-api.md` and the specs in `features/` change in the same pull
request.

### 4.3 Client (`internal/llm/coddy.go`)

A provider type `coddy` implementing `Provider`:

- `api_base` is the origin of the remote `coddy serve`, or the relay mount
  `https://relay/swarm/nodes/<node>`; the type appends `/coddy/llm/...`. `api_key` is the remote's
  token (a shared-model token is enough), or the relay's client token through a mount.
  `providers[].proxy` applies as for every provider, through `HTTPClientForProviderProxy`
  (`TestProviderProxyGuard` covers the new file by scanning `internal/llm`). `coddy -t` and
  `--dry-run` warn when `api_base` is `http://` and the host is not loopback, because the token and
  the whole history would travel in clear.
- `Complete` is `Stream` with the chunks dropped, and a local `models[].stream: false` does not change the wire. For a `coddy` row the provider applies `StreamIdleTimeout` whatever `models[].stream` says.
- Local `models[]` rows are `<provider>/<alias>`. An alias says nothing about the model, so the
  capabilities of a row (context window, multimodal, reasoning levels, default level, `off`) come from
  the remote listing, never from the id heuristics of `config/reasoning.go`. A key set in the local YAML
  still wins, key by key, and a refresh never writes a key: the listing lives in the manager's cache, not
  in the row. **Decided by model `m6-capabilities` (variant D):**
  - `llm.ModelEntry` carries `revision`, `multimodal`, `reasoning_levels`, `reasoning_default` and
    `allow_reasoning_off` next to `ContextWindow`; the cache of `session/context_window.go` keeps the
    whole record and `coddy` joins `providerListsContextWindows`. The reasoning methods of `*config.Config`
    consult one resolver, and every read of `models[].multimodal` goes through one method. `multimodal` and
    `allow_reasoning_off` become tri-state so that an absent key asks the listing and a written `false`
    still means no. The Settings picker adds a `coddy` row without copying any of the three values (a
    window copied into `max_context_tokens` would pin the row).
  - Refresh: a fetch starts when a reader finds the entry older than its TTL (this only keeps the picker
    fresh), and at once when a request is answered `stale_revision` or another capability `invalid`. Stale
    values keep serving while it runs, but a reader that follows such an answer waits for it, for at most
    `ContextWindowWait`.
  - First request: the client sends `options.expected_revision` from its cache entry. After
    `stale_revision` it refreshes and sends the request once more; a second `stale_revision` ends the
    call. The provider rebuilds only what it owns: a `reasoning_effort` that the refreshed row no longer
    lists falls back by the same rule as `EffectiveReasoning` (applied by the provider to the refreshed
    entry), and image parts are dropped from the retried request when the refreshed row is not multimodal
    (new code in `coddy.go`; today the drop happens at the HTTP intake, `dropImageParts`). The history is
    not re-projected: a context window that shrank applies from the next turn, and a request still too
    large for the provider ends as `invalid` with the numbers parsed from its answer. *(the retry after
    refresh is not modeled)*
  - The default level travels as an explicit `reasoning_effort` (the session falls back to it), so a
    withdrawn default is a stale view like any other and is covered by the same check.
  - A local key that disagrees with the listing is refused by the remote for as long as it stays: no
    refresh can fix it. `coddy -t --dry-run` warns for every local key that differs from the listing.
- `ListModels` for the type reads `/coddy/llm/models`, so the Settings model picker works.
- Account usage (phase 2): a `coddy` case in `fetchProviderUsage` reads the projection of 4.2.
- **Classification and retries.** A response with a coddy `kind` follows the table. Anything else
  is classified by status, in this order: `401` and `403` are `auth`; `408`, `502`, `503` and `504`
  **before any output** are a transient transport failure and are retried (a relay answering `502`
  because the node is unreachable is exactly that: `writeHopError` in `mount.go`); a `404` with a
  hop-error body is `invalid`; everything else is non-retryable. A plain-text `401` from `authGate`
  and an HTML page from an intermediate proxy fall under the same rules. A `GET` of the listing that
  answers `200` with HTML (a remote built with the SPA and without the routes) is a clear "remote does not offer shared models" error. The same holds for the completions route: a `200` whose `Content-Type` is not `text/event-stream` is that error and is not retried (the byte guard only wraps event streams). A status is meaningful only before output: after the `200` of a stream an error frame follows its `kind`, never its `status`.

| `kind` | HTTP | Local retry | Surfaces as |
|---|---|---|---|
| `busy` | 429 | yes, only inside the provider's own wait (4.1b), never by the wrapper; once spent, no | busy error, typed, status zero, not transient |
| `rate` | 429 | no, the remote already retried | rate error with `retry_after_s` |
| `quota` | 429 | no | `*QuotaResetError` with `reset_at`, so `agent.wait_for_limit_reset` works through a hop |
| `upstream` | 502 | no, the remote already retried | upstream error, a stall included |
| `invalid` | 400, 404, 413 | no | the remote's message |
| `auth` | 401, 403 | no | "the remote refused the credential"; through a mount the client cannot tell the node's token from the relay's |
| transport | none | yes while nothing was emitted, no after | the existing `emitted` contract of `resilient.go` |

The agent relies on the **types** of `llm` errors: `IsStreamTruncated` and `IsStreamStalled` keep the
partial answer after a cut stream, `IsTransientProviderError` starts the recovery that re-issues a step,
and the resilient wrapper decides retries with `isRetryableLLMError` and `httpStatusFromError`. Those
predicates must **not** be fed `*streamStalledError`, `*streamTruncatedError`,
`*streamServerError{code, emitted}` or a status error built from the remote's answer: the wrapper
retries a stall or a truncation while `emitted` is false (the remote has already retried them), retries
a `429` / `502` by its status, retries a bare `*streamStalledError` even after output (it has no
`emitted` field; only `*streamTransportError{emitted: true}` around it refuses) and retries a plain
`429` / `500`. The client builds **one** error type,
`coddyAPIError{kind, cause, emitted, status}`, registered in all five predicates (`IsStreamTruncated`:
cause `truncated`, or kind `upstream` after output; `IsStreamStalled`: cause `stall`;
`IsTransientProviderError`: kind `upstream`; `UpstreamStatus`; `UpstreamRetryAfter`) and treated by
`isRetryableLLMError` / `httpStatusFromError` as status zero and **never retryable**, for every kind and
for every HTTP status except `408`, `502`, `503`, `504` before output. The only failures retried locally
are those statuses (a relay answer of that kind, a refused or cut connection) and a stream that ends
without a terminal event, both only while no chunk was emitted; they are built as the existing
transport errors (`*streamTruncatedError{emitted}`). On a failure after a chunk `Stream` returns the
partial `*Response` next to the error: all text and reasoning, and the tool calls whose `tool_call`
chunk arrived (the agent records them as interrupted; a tool call runs only from a `final`); calls that were only named or streamed are dropped. An `error{upstream, cause: timeout}` (the `max_call_ms` of a blocking row) is not transient and is retried by none of the five predicates, because the agent's provider recovery would otherwise repeat the call twice, each time up to `max_call_ms`; a partial answer after output is kept as for a truncated stream. Without this both ends retry and one `Stream` call costs the
remote up to (`llm_retry_max` + 1) x (remote retries + 1) = 16 upstream requests for a plain
`error{upstream}`. With it a deterministic upstream failure costs remote retries + 1 = 4 per call; 16
remains the worst case only for repeated transport failures (the connection cut after the remote
retried). The agent's provider recovery repeats the call up to twice (3 calls, 48 requests in the
worst case, the same multiplier as for a local model). *(model `m3-stream`)*

- A relay rebuilt on a config change cuts the streams in flight (`docs/operate/swarm.md`): a
  transport failure, same rule.

Registration points: `NewProvider` switch (`provider.go`), `config.AllowedLLMProviderTypes`,
`config.schema.json` and `UISchemaMap` enum, the SPA provider form and i18n, `provider_label.go`,
`request_options.go`, `dryrun/providers.go` (a `--dry-run` probe of the remote listing),
`config/reasoning.go`, `session/provider_usage*.go`, `context_window.go`, `docs/reference/config.md`,
`configure-coddy/SKILL.md`. `features/provider_proxy.feature` gains the request classes completion,
list and usage.

### 4.4 Discovery

v1: one `providers[]` row per remote or mount; the models are picked from the remote listing in
Settings. v2: a row pointing at a relay root enumerates `GET /swarm/nodes`, asks each online agent
node for its shared list through its mount, and offers `<node>/<model>`. A model shared by several
nodes can then fail over; the relay has no automatic failover today.

### 4.5 Authentication and scope

`shared_as` is a naming and listing mechanism, **not an access boundary**. The bearer of `coddy
serve` opens the whole API (`isProtectedPattern` protects every `/v1/*` and `/coddy/*` route with
the same token): with it a client can call `POST /v1/chat/completions` with the selector of *any*
`models[]` row, shared or not, and `POST /v1/responses` with the `agent` profile runs tools,
`run_command` included, on the remote's machine. "Sharing a model" with that token is sharing the
host. So:

- **An LLM-only token class, in phase 1.** `httpserver.shared_models.tokens` is a list of
  credentials (`${ENV}` references like the other credentials, write-only in Settings). `authGate`
  admits such a token only on `GET /coddy/llm/models`, `GET /coddy/llm/models/{alias}/usage` and
  `POST /coddy/llm/completions`, and refuses it everywhere else like an unknown token. The main
  token still works on every route.
- **Fail-closed.** Any shared-model token turns the gate **on**. `authPolicyNow` keeps the shared-model
  tokens in a field of their own (`sharedTokens`), **never** in `tokens`: `enabled = len(tokens) > 0 ||
  login.enabled || len(sharedTokens) > 0` (today `enabled` is `len(tokens) > 0 || login.enabled`, which
  would leave a share-only node with no main token and no login open on every route), `acceptBearer(tokens,
  ...)` stays class-blind for the main class only, and every other consumer of `tokens` (the
  media-capability signing key `workspaceSigningKey` uses `tokens[0]`, `coddyAuthMeGet`,
  `decorateConfigDocument`) ignores `sharedTokens`: a shared token must never become key material or
  count as "a token is configured" for the UI. The start-up check `authOn := tokenOn || loginOn` learns
  it too, for its two log warnings. A node with only shared-model tokens refuses every protected route
  except the three LLM routes to every caller, anonymous included; what stays public is what is public on
  any node (the static shell and the sign-in routes, and `/docs`, `/openapi.*` under `public_docs`), and
  `coddy -t` says that its API is closed and the web UI cannot sign in.
- **No token is of two classes.** The check compares the shared-model tokens with every main-class token,
  the ones in the configuration **and** the ones given by `--auth-token` / `CODDY_HTTP_TOKEN` (added after
  load by `SetExtraAuthTokens`, which the loader cannot see), and with the swarm tokens (configured,
  `--swarm-auth-token`, `CODDY_SWARM_TOKEN`, the generated one); it runs at start-up, at every
  configuration install (hot reload, `PUT /coddy/config`) and in `coddy -t`, which sees the environment.
  A refused candidate leaves the running configuration. If a duplicate ever reaches the gate the token is
  treated as shared (least privilege): a missed duplicate then cannot grant the whole API. *(model
  `m4-auth-relay`)*
- **The shared routes require authentication, whatever the listen address.** Whenever any row has `shared_as`
  the routes need a credential, whatever the listen address (a loopback listener behind a TLS reverse
  proxy, or a node on `127.0.0.1` that joined a relay by tunnel, which sends no `Authorization` when
  it has no token, is as exposed as any other). The only exception is the explicit `httpserver.allow_insecure` of an operator who already publishes the whole API open; it changes nothing once any credential exists.
  The routes are registered always and the handler decides by the live policy: `403` with
  `kind: auth` and a plain explanation. The refusal is `403 kind: auth` while no credential of any class is configured; as soon as one is (a main token, the login or a shared-model token) an anonymous caller gets the gate's `401`. The condition "no credential is configured" is one function used by the gate and by the handler; the handler never evaluates it from the start-up state or from the listen address. `coddy -t` and `--dry-run` report `shared_as` without authentication as an error.
- A node that exists to share models gives its consumers, and its relay, a shared-model token (4.5a).
- Through a relay the client holds a relay client token and the relay replaces it with the node's
  token, so the node sees one caller: per-client quotas cannot be kept on the node, and the stream
  limit counts the relay's clients together. The slot is held while the request body is read, so up to `max_streams` clients that send slowly can pin a credential for the length of the body deadline, repeatedly, and the credential's other clients get `busy` meanwhile; through a relay these are all clients of the relay. The limit protects the node and its provider, not clients from each other. Per-client scopes enforced by the relay's path gate (`mountAllows`) stay in phase 3, with an mTLS option on the direct listener.

### 4.5a What a relay does with a node's token (read from the code)

- The token travels in the registration document (`RegisterRequest.Token`) for **both** transports
  and the relay keeps it in its registry. It is used for: the mount (`rewriteFor` sets
  `Authorization: Bearer <token>`), the aggregated session list (a `GET` of the node's sessions
  route, `external/swarm/sessions.go`), and the topology of **child relays** only.
- A tunnel node serves its full `Handler()`, which is `corsMiddleware(authGate(mux))`, over the
  tunnel, so the node's own gate judges tunnel requests exactly as it judges direct ones.
- A node that joins with no token makes the relay send no `Authorization` at all: with node auth on,
  every mounted call is a `401`; with node auth off, the node is open to every relay client on every route except the three LLM routes, which still answer `403 kind: auth` when a row has `shared_as` and `allow_insecure` is not set; sharing through a relay therefore needs a token on the node. For an
  agent with an empty `swarm.join[].token` there is no fallback to the relay's own token (that is for
  relays only).
- **Joining with a shared-model token works through the relay for both transports and needs no relay
  change**, with `swarm.join[].token` set to it (a node pinned from the relay side uses
  `swarm.upstreams[].token`). The mount carries the three LLM routes. The relay's session aggregation
  gets a `401` from that node and shows it as a warning, `<node>: 401 Unauthorized`, which the swarm view lists (the same warning comes from a node with auth on and an empty or stale or rotated token; an unreachable node warns with the transport error and an expired lease with `offline since`); topology is unaffected, since only child relays are asked for it; opening the node as
  an environment in the web UI is refused, which is the point. Silencing the warning with a node
  label can follow.
- One join token means one privilege. A node that must be driven through the relay **and** share
  models through it has to join with a token that does both, so every relay client then gets that
  privilege until the relay has per-client scopes (phase 3).
- The relay client token a consumer puts into the `api_key` of a `coddy` row opens the mounts of
  **every** node of that relay (`mountedPrefixes` carries `/v1/` and `/coddy/` for all of them),
  each with the token that node joined with. The shared-model token class therefore protects only
  the nodes that joined with one. Share-only nodes belong on a **dedicated relay**, and the
  documentation says so.
- A re-registration with an empty `Token` does not erase the old one (`registry.go`). The stale token
  therefore survives the removal of `swarm.join[].token` for as long as the lease is renewed: a direct
  node renews on every heartbeat and a tunnel node keeps one lease for the whole connection, so for a
  node that stays joined it never expires on its own. It is dropped only when the node is silent for
  longer than the offline grace (ttl + grace, 180 s by default) and registers again, or at once by
  rotating to a different non-empty token, by `DELETE /swarm/nodes/<name>` on the relay (the node's next
  registration is a fresh lease with the new, empty token), or by restarting the relay. **Decided by quorum (phase 3, hardening; a relay change in `external/swarm/registry.go`): a registration or renewal by the lease's owner replaces the token with the one it carries, an empty token included** (compatibility with nodes of older versions to be checked first). Until then, to cut a node off the relay, evict it; do not only edit its configuration. *(model `m4-auth-relay`)*

### 4.6 Prompt cache and determinism

The cache of the providers rests on a byte-stable prefix. The local agent already keeps it stable,
the DTO is encoded deterministically (struct fields in order, tool schemas as sorted maps) and the
remote serialises it with the provider's own client. Two tests, because byte equality is not
reachable everywhere (a Devin request body carries a clock-derived `requestID` and random cascade
and trajectory ids, a Codex request a random `session_id` header):

1. **Projection equality, every provider type.** The `[]Message` and `[]ToolDefinition` the remote's provider receives are deep-equal to what a local provider of the same type receives for the same history: the local-only fields cleared, a signature in envelope form opened to the raw one (signatures, call ids, tool schemas, order).
2. **Body equality** for `openai`, `anthropic`, `neuraldeep` and the Codex body with an injected id
   source and clock: the body the remote's provider sends upstream through a hop is byte for byte the
   body a local provider of the same type sends. Devin is compared after normalising its ids.

**Open:** a local Codex or Devin provider is built once per run and keeps a stable `session_id` or
`cascadeID`; through a stateless hop the remote builds one per call, and whether that changes the
backend's cache affinity cannot be read from the code. A measurement of `cached_tokens` on two
consecutive hop calls with one history, against a local run, closes it. If affinity drops, the
client sends an opaque `conversation_key` and the remote derives the ids from it deterministically
(an HMAC), staying stateless.

### 4.7 Loops

No hop header in v1. The guard is that a row whose provider is of type `coddy` cannot be shared, so
two Coddys cannot lend each other's lent models. A loop through a `type: openai` provider that
points at another Coddy's `/v1` is **not** caught, and the documentation says so.

## 5. Phases

The executable specification is `features/remote_model_provider.feature` (direct) and
`features/remote_model_provider_relay.feature` (through a relay, `-tags http,swarm`); scenarios
tagged `@phase2` belong to step 2.

0. **Today, no code**: `type: openai`, `api_base: https://remote/v1` or
   `https://relay/swarm/nodes/<node>/v1`, with the losses listed in 2 and the open API of 4.5.
1. **Wire, client and access**: `models[].shared_as` and the subscription acknowledgement, the
   shared-model token class, fail-closed authentication and the unconditional requirement, the stream limit, `busy_wait_ms` and `max_call_ms`, `GET /coddy/llm/models` and `POST /coddy/llm/completions`, the DTO and its
   coverage test, the `coddy` type with typed errors, `ListModels`, the context window from the listing, `revision`, `expected_revision`, `stale_revision` and the signature envelope on the wire and the client's refresh on `stale_revision` (the wire shape is strict by `protocol`, and an envelope cannot be introduced under stored histories later), a `coddy` row that declares `multimodal` and `reasoning_levels` in its own keys, direct and mount, docs, schema. Every untagged scenario.
2. **Metadata and limits**: reasoning levels, default and `off`, and **multimodal** from the listing (the resolver, the three multimodal reads, the tri-state keys, the `--dry-run` disagreement warning), the usage projection route and its client and the liveness probe of a vanished peer (45 s from its disappearance on the leg the node sees). The `@phase2` scenarios.
3. **Hardening**: relay-side per-client scopes, mTLS, rate limits, audit counters, a node label to
   silence the relay's session warning.
4. **Relay discovery** and multi-node failover.

## 5a. Tests that are not scenarios

The spec holds the happy paths; the rest is unit tests next to the code, as the workflow rule asks.

| Area | Cases |
|---|---|
| Wire DTO | reflection test over `llm.Message`, `llm.ImagePart`, `llm.ToolDefinition`, `llm.StreamChunk`, `llm.Response`; `protocol` strict equality, the error names both versions; body limit 413 before any read; client SSE frame limit is non-retryable |
| Config | alias pattern and uniqueness (both rows named, `file:line:col` in `coddy -t`); a row of type `coddy` cannot be shared; `shared_as` on `codex`, `devin` and a login-backed `neuraldeep` row is refused without `shared_subscription_ack`, accepted with it, and the key does nothing without `shared_as`; `shared_as` without authentication is an error unless `allow_insecure`; a shared-model token equal to a main or swarm token is refused; `httpserver.shared_models.max_streams` default 5, negative refused; `busy_wait_ms` precedence (provider above zero wins, zero falls back, global absent means 30000, global explicit zero means no waiting; tested row by row), `httpserver.shared_models.max_call_ms` (default 1800000, `0` is the 8-hour ceiling, above 28800000 or negative refused), `coddy -t` warns when `I = 0` or `0 < I < 2H` for a `coddy` row and for a shared `stream: false` row with no `timeout_ms` while the effective `max_call_ms` is above 1800000; `http://` on a non-loopback `api_base` warns |
| Auth | a shared-model token passes the three routes and is refused on every other route, `/v1/chat/completions` and `/v1/responses` included; the main token passes everywhere; **only shared tokens configured: the gate is on and `/coddy/sessions` is refused for every caller, anonymous included**; the routes answer `403 kind: auth` on an open node, also on a loopback listener, also after a hot reload that removes the last credential; `provider/model` as `model` is a 404; a shared token equal to `--auth-token` or `CODDY_HTTP_TOKEN` or to a swarm token is refused at start-up and on reload, and a duplicate that reaches the gate is treated as shared; the handler answers by the gate's snapshot, never by a second read of the configuration; `public_docs` on a share-only node leaves `/docs` open and nothing else; the workspace capability key does not change when shared-model tokens are added or removed (a minted capability stays valid); the first request after a hot reload that removed the last credential answers 403 on a loopback listener; an anonymous caller gets `403 kind: auth` only while no credential exists and the gate's `401` once one does |
| Server | unknown or unshared alias answers 404 `kind: invalid` and names no selector or provider; the limit key is the bearer hash and counts all aliases; the slot is taken before the body is read and `busy` answers without reading it; a `400`, `404` or `413` releases it when it answers; a client disconnect, a provider failure and a body read timeout each release it; omitted `reasoning_effort` applies the row's `reasoning_default`; `off` without `allow_reasoning_off` is refused; client `max_tokens` above the row's ceiling is cut; `temperature` unset never trips `Validate("codex")`; `retry_budget_ms` turns a pause longer than the budget into `error{quota, reset_at}` at once; the error `message` never contains the provider name, `api_base`, selector or upstream model id, and an `invalid` error carries parsed numbers only; a stall before and after the first chunk becomes `upstream` with the right `emitted` and `cause`; a row with `stream: false` that generates for six minutes is not cut, at the provider and at the agent level; heartbeat comments are at most 15 s apart, the first one right after the headers; a stalled writer is cut by the per-write deadline; exactly one terminal event, later events dropped; usage projection excludes provider, type, plan, key name, wallet and every model id, and drops a block that concerns an unshared model; N concurrent calls above the limit held by a barrier never exceed it (race detector on); each exit path (complete, provider error, disconnect, body timeout, 4xx, write timeout) leaves the count at zero and a second release cannot make it negative; a stalled non-reader and a stalled slow-body client are each released by their deadline; an upstream that never answers ends as `error{upstream, stall}` after S (streamed) or as `error{upstream, cause: timeout}` after `max_call_ms` (blocking) and is not re-issued by the agent's recovery; a vanished peer frees the slot within the probe bound (phase 2, direct leg);  a stale revision is answered `stale_revision` before the provider is built; a signature of another model is dropped, never an error; the usage projection of a block names the alias only |
| Client | the error table of 4.3 case by case, and the status-ordered classification of a plain-text `401`, a relay `502` before output (retried), a relay `404` hop error and an HTML `200`; `busy` waits within one monotonic deadline without spending `llm_retry_max`; no first-token timer is armed for a `coddy` row: a remote whose first token takes longer than `llm_first_token_timeout_ms` and a blocking row silent for six minutes are not cut by the agent, and `CallBudget` is unset; a `coddy` row with local `stream: false` still has the idle guard; a peer that stops reading the upload is cut by R; budget zero fails at the first `busy` after one request; jitter never goes below `Retry-After`, the last sleep is cut; the exhausted error is not restarted by the wrapper, not re-issued by the agent's provider recovery, status zero in the five predicates; a slot freed just before the deadline is taken by the last request; Stop cancels a sleeping wait at once; `Complete` waits too; a `cause: stall, emitted: true` frame is not retried (typed with `emitted`, never a bare `streamStalledError`);  a stream ending without a terminal event is a transport failure retried only while nothing was emitted; the partial `*Response` and the typed error survive a cut or a stall after output, and the recovery path of the agent is taken; `final` is not appended to the chunk accumulator and no tool call runs twice; remote backoff longer than the client idle timeout does not trip the client guard; `Complete` equals `Stream`; the URL is built right for a base with and without a path and a trailing slash; a remote without the routes gives a clear message; after `stale_revision` an unlisted level falls back and images are dropped for a row that is no longer multimodal, and a shrunk context window is not applied to the retried request |
| Transport | `TestProviderProxyGuard` still green; `features/provider_proxy.feature` gains the request classes completion, list and usage for the new type |
| Cache | the two tests of 4.6 |
| Dry run | `--dry-run` reports an unreachable remote, a refused token and a protocol mismatch per provider row |
| Relay | a node with an empty join token gets no `Authorization`; a re-registration with an empty token keeps the old one and a heartbeat with an empty token does not drop it (until the phase 3 change; then the owner's registration erases it); `DELETE /swarm/nodes/<name>` followed by the node's next registration leaves the entry without a token; the sessions warning for a share-only join and for an empty join token on an authenticated node |
| SPA | the alias field and the acknowledgement checkbox of a model row, tri-state `multimodal` and `allow_reasoning_off`, the shared-model token field, `messagesParity.test.ts` for the new strings |

The two scenarios about the stream limit stay in the specification although the convention would
send refusals to unit tests: they are the only executable record of two decisions (five at once, the
relay as one caller), and the table above holds the unit cases of the limit.

## 6. Risks and open questions

- **Terms of service for shared subscription logins.** Anthropic's published policy does not allow
  routing requests through Free / Pro / Max credentials on behalf of others, and an Anthropic row
  needs an API key **[unverified]**. For OpenAI Codex no explicit prohibition was found, only
  secondary sources about tolerance of third-party harnesses **[unverified]**. The mandatory
  `shared_subscription_ack` puts the decision on the remote's operator in writing; checking the
  vendor's terms is still theirs, and ours before release.
- **Privacy.** The whole harness, file contents and tool output included, passes through the remote
  host. The route persists and logs no content; retention and zero-data-retention are those of the
  remote's provider account. Account usage is account-wide, and the projection of 4.2 is what every
  token holder sees of it.
- **Signatures and alias reassignment.** A reasoning signature is valid only for the model that issued it. The signature envelope of 4.2 makes the remote drop signatures of another model; a `revision` alone does not (model `m6-capabilities`: with a fresh view and an old signature every call is refused by the provider).
- **Version skew.** `protocol` in the listing and in the request lets either side refuse a mismatch.
- **Overlap.** Pull request #468 (usage on non-streaming completions, cached tokens, Anthropic cache
  usage) is merged; the new route reports the same numbers from `llm.Response` and does not depend
  on the Chat Completions serialisation.
- **Open:** the body limit of 32 MiB was set from memory of provider limits, not re-checked; the
  frame limit of 16 MiB, the 5 s sleep ceiling, the 30 s body deadline and the 60 s write deadline and the 15 s heartbeat
  are proposed values; the Codex and Devin cache affinity of 4.6.

## 6a. Modeling results

Six models of the agreed specification were checked with the `model-check` engine (explicit state,
`mcd 0.2.0`; untimed, time as an explicit discrete clock where it matters; constants scaled down and
the scale argument given in each report). Models and reports are in
`docs/plans/remote-model-provider-models/`; a result is a statement about the model and its
assumptions, not about code that does not exist yet.

| Model | What it checks | Result |
|---|---|---|
| `m1-slots` | the per-key limit, slot before the body, one release on every exit path | limit, "slot count equals holders", "busy only when full", key isolation and "a stalled peer is released by a deadline" **verified**; the old design (slot after the body) **violates** the body-memory bound; a call that never returns holds its slot (spec gap, `max_call_ms`) |
| `m2-busy-wait` | where the busy wait lives | **decided** (4.1b): inside `provider.Stream`, budget uncapped, no first-token timer for a `coddy` row (decided by quorum, item 1); the typed-error-in-the-agent-loop variant **rejected** (other callers do not wait, three traps); the naive variant and "429 on the exhausted error" **violate** |
| `m3-stream` | the frame state machine and the client | all 19 properties **verified** for the spec client; the first wording of the typed-errors paragraph was **wrong** (stall retried after output, `429`/`502` retried by status, 16 requests): replaced in 4.3 |
| `m4-auth-relay` | the token classes, hot reload, the relay's use of a node's token | the new policy **verified**, the old one (`enabled = main \|\| login`) **violates** four properties; found: duplicate tokens through `--auth-token` are invisible to the loader, a request straddling a reload, `workspaceSigningKey` must not read shared tokens, the stale-token lifetime |
| `m5-guards` | the three timers, heartbeat, per-write deadline | `H + J < I` and `H + J < L` are exact boundaries; frame-only counting and no heartbeat **violate**; found: a hung upstream before its first byte is not caught, the agent's first-token timer cuts a live remote that only heartbeats, a vanished peer holds a slot for hours without a probe |
| `m6-capabilities` | how the capabilities of a row arrive | **decided** (4.3): variant D (listing cache + `revision` + `expected_revision` + signature envelope); A never converges, B shows up to five refusals per change, C never learns widening |

**Decided by modeling:** where the busy wait lives (4.1b) and how capabilities arrive (4.3).

**Decided by quorum** (cross-review round 3, two reviewers; where they split, the orchestrator's
evidence decided and says so):

1. **The agent's first-token timer is not armed for a `coddy` row** (both reviewers: option (a)). The
   remote owns model progress (S from the start of the call, `max_call_ms`), the client keeps the liveness
   guard I and the request bound R, `llm.WithFirstTokenArm` is dropped, Stop is unaffected because the
   context cuts every stage. Condition: `max_call_ms` is phase 1. Cost: a silent upstream is found by S (in the code the remote's explicit stall guard cancels the whole provider call, wrapper retries included, so the bound is S, not (retries + 1) times S); the user sees the wait counter and Stop works.
2. **`max_call_ms` exists, is on by default, 1800000 ms, at most 28800000 (8 hours, the operator's ceiling), `0` means that ceiling, phase 1** (4.1a). Both reviewers: on by
   default; they split on the value (300 s against 30 minutes); 300 s contradicts the plan's own test of a
   six-minute blocking row, so 30 minutes stands. To check against the longest non-streamed completions
   of the supported providers before release.
3. **The liveness probe of a vanished peer is phase 2** (4.2), together with its 45 s promise and only on
   the leg the node sees: the direct listener is plain HTTP/1.1, the tunnel already pings at 30 s and
   15 s.
4. **The relay erases the token when the owner's registration carries none** (phase 3, a change in
   `external/swarm/registry.go`); the interim rule is in 4.5a.
5. Constants and consistency (Q5, Q6, split, decided by evidence): the reviewer who found the 60 s against
   30 s body deadline, the unbounded `I = 0`, the missing client bound on the upload and the recovery
   wording was right; the corrections are in 4.1a, 4.1b, 4.2, 4.3, 4.5, 4.6 and 5a. Nothing blocks the
   red test harness for the untagged scenarios of step 1.

**Open** (the operator's):

- The upper bound of a slot held by a vanished peer on the direct leg before the phase 2 probe is the
  kernel's retransmission timeout (about 15 minutes on Linux by `tcp_retries2`, general knowledge, not
  verified here): an integration test with a dropped connection would close it.
- The limit of 5 was run at scale 2 and 3; the carry-over to 5 is an argument, not a run.
- Capability readers: the resolver for the four reasoning methods (25 call sites in 14 files) and the
  tri-state keys are real surface; the plan counts them as phase 2.
- Whether S should be tuned on the remote: a silent upstream is now found after S for the whole call (300 s by default).

## 6b. Implementation status (phase 1)

Implemented on the branch, each stage test-first and reviewed by the next: **config** (`shared_as`,
`shared_subscription_ack`, `httpserver.shared_models.{tokens,max_streams,max_call_ms}`,
`providers[].busy_wait_ms`, `agent.shared_busy_wait_ms`, provider type `coddy`, the token-class and
authentication checks, `coddy -t` findings), the **`coddy` provider and the wire** in `internal/llm`
(frames, errors and the five predicates, the busy wait, the stale-revision loop, the signature
envelope), the **server** in `external/httpserver` (the two routes, the token class and the one policy
snapshot per request, the limiter, the three timers, the key file), **agent and session wiring** (no
first-token timer for a `coddy` row, the capability cache with `revision`, dry-run probe, serve-start
checks), the **docs**, the **SPA and console** surfaces, and the **BDD harness**: the 22 untagged
scenarios (16 direct, 6 relay) pass in strict mode; the six `@phase2` scenarios are excluded.

Not implemented (phase 2 and later, as planned): the usage projection route (it answers 404), reasoning
levels and multimodal from the listing (the resolver and the tri-state keys), the liveness probe, the
`--dry-run` warning for a local key that differs from the listing, the relay erasing a stale token,
per-client relay scopes, relay discovery.

Decisions taken where the plan was silent: the swarm token class is `swarm.auth_token`,
`swarm.pairing_tokens`, `swarm.join[].pairing_token` and the swarm tokens of flags and environment
(`swarm.join[].token` and `swarm.upstreams[].token` are not, because a shared-model token is put there);
a `neuraldeep` row is subscription-backed when no key source is set (`api_key_command` is not run);
aliases are case-sensitive; frames are flat with `text_delta`, `reasoning_delta`, `tool_call`,
`tool_call_delta`, `tool_call_named`, `stop_reason`, `input_tokens`, `output_tokens`; the envelope is
`coddy1:<tag>:<b64url signature>:<checksum>` and its key lives in `<home>/shared-models.key` (0600, kept
in memory with a warning when it cannot be written); `emitted` counts the chunks passed to `onChunk`
(so a cut `Complete` is safely retried); a `quota` error with no reset time is a plain error, not a
wait; an unknown frame type is ignored; `retry_budget_ms` is the retry budget minus the ledger already
spent; an upstream `401`/`403` is `kind: upstream` (the remote's own authentication is `auth`); the
`invalid` codes are `protocol_mismatch`, `stale_revision`, `unknown_model`, `request_too_large`,
`body_timeout`, `invalid_option`; the busy countdown rides on `provider_usage` with the blocker
`remote_busy`; a request id header `X-Coddy-Request-ID` is answered; `llm.RequestOptions` carries
`RetryBudget`, and `llm.WithBusyWaitSleep` is the test hook of the wait.

Deviations from the text above: the wire error object has no numeric fields, so a context limit in an
`invalid` answer is not parsed (4.2 asked for parsed numbers); the 403 for a node without credentials
answers on the three routes whether or not a row has `shared_as`; the body deadline P is set when the
handler starts and is not cleared on an early refusal (`Connection: close` is not usable, since on the
h2 tunnel it would be a GOAWAY for the whole tunnel); a configuration candidate is checked **before** the
write of `PUT /coddy/config`, because the rollback of `configapi` overwrites its `.bak` on every load.

Defects found by the harness and fixed: the direct completion routes, `enhance` and `describe` did not
carry `BusyWait` for a `coddy` row and failed at the first `busy`; `streamIdleTimeout` switched the guard
off for a `coddy` row with `stream: false`; a flake in the fake clock of the shared-model tests.

## 6c. Code review of the implementation

The production code was cross-reviewed in three layers (server and authentication, the `coddy` provider
and wire, configuration and agent wiring), two reviewers each, every finding verified against the code
by an orchestrator, several by a reproducing probe. Confirmed and fixed with tests: a memory
amplification in the request decoder (a 32 MiB body of `{},` built about 4 GiB of heap: the number of JSON
objects and arrays is now capped before the body is parsed), a limiter key taken from an unvalidated
bearer on an open node, a race creating the key file, the provider type leaking through the
`invalid_option` text, `api_key_command` running twice per call, an unbounded `retry_budget_ms`, an integer
overflow in the busy-wait sleep (an unbounded `Retry-After` produced a request flood), an unbounded read of
a refusal body, an error frame printed without validation, a quota reset in the past that re-issued at
once, the busy-wait budget charged for stream time, the agent's recovery re-issuing deterministic
upstream statuses, the busy notice not cleared after admission, the reasoning clock off for a `coddy` row
with `stream: false`, and `<home>/.env` credentials invisible to the `coddy serve` start-up check.

## 7. Go libraries

No new module is needed. `net/http`, `httputil.ReverseProxy` (only for a possible passthrough
variant), `bufio` for an SSE reader with a raised line limit, `gjson` (already in `go.mod`) for
usage, `crypto/tls` for mTLS, and `golang.org/x/time/rate` for limits. Considered and rejected:
multi-provider SDKs (langchaingo, eino, genkit, any-llm-go), which reduce everything to their own
model and lose reasoning items; gRPC and ConnectRPC, which need protobuf generation while every
OpenAI client and proxy speaks HTTP and SSE; tsnet, libp2p, quic-go and OpenZiti, which add a large
dependency surface where the swarm tunnel already covers the dial-out case. Worth reading for
design: Bifrost passthrough, Agent Router `internal/translator`, Ollama's cloud-model
pass-through (`OLLAMA_HOST`, remote models in the shared listing).

## 8. Sources

Repository: `external/httpserver/server_direct.go`, `external/httpserver/server.go`,
`external/swarm/mount.go`, `internal/swarm/{dto,tunnel}.go`, `internal/llm/{provider,resilient,model_list}.go`,
`internal/config/{models,providers}.go`, `docs/operate/swarm.md`, `docs/operate/remote.md`,
`docs/plans/remote-control.md`, `docs/plans/provider-proxy-enforcement.md`,
`docs/plans/neuraldeep-usage.md`.

External (reported by research subagents): Bifrost passthrough
(docs.getbifrost.ai/integrations/passthrough), GoModel (github.com/ENTERPILOT/GoModel), Agent Router
(github.com/theagentrouter/agent-router), CLIProxyAPI (github.com/router-for-me/CLIProxyAPI),
Open Responses (openresponses.org), OpenAI reasoning and prompt-caching guides, Anthropic thinking
and prompt-caching docs, Anthropic legal and compliance page (code.claude.com/docs/en/legal-and-compliance).
