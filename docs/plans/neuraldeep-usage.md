# Plan: NeuralDeep usage in the status bar (issue #102)

Status: v5 after four cross-review rounds (codex gpt-5.6-sol, cursor auto, coddy
on neuraldeep/qwen3.8-27b, and a fresh Claude subagent; every answer so far was
CHANGES_REQUESTED, each round narrower than the last). Round-1 deltas are marked
`[rev]`, round-2 `[rev2]`, round-3 `[rev3]`, round-4 `[rev4]` (codex only; the
plugin's plan-phase budget of four iterations is spent, the remaining items are
implementation details and the code phase re-checks them). Layers 1 and 2 are
implemented on this branch: `features/neuraldeep_usage.feature` (HTTP, three
scenarios) and five console scenarios in `features/cli_tui.feature`; the
reference docs are `docs/cli.md` (Footer, `/usage`), `docs/http-api.md` and
`docs/acp-protocol.md`.
Branch: `claude/coddy-tui-usage-info-746e58`.

## 1. Goal

When the active model belongs to a `neuraldeep` provider, every Coddy surface
shows how much of the account's quota is spent and when it resets, so the
operator sees the limit coming instead of learning about it from a 429 in the
middle of a turn. Issue #102 asks for it "next to the prompt input, like
Claude Desktop"; the comments add the wallet balance for wallet keys and the
Claude Desktop behaviour on a hit limit: a banner with the reset time and an
"auto-continue when limits reset" option.

Primary surface for this iteration is the interactive console (`coddy` on a
TTY): its footer is the status bar, and the console already has the token and
context counters there. The SPA composer and the auto-resume follow as
sub-features on the same plumbing.

## 2. What the reference tools do

- **Claude Code**: `/usage` draws two bars, the 5-hour session and the 7-day
  window, each with "N% used" and "Resets <time>". The custom status line
  script receives `rate_limits.five_hour.used_percentage`,
  `rate_limits.seven_day.used_percentage` and `resets_at` (epoch seconds), and
  `cost.total_cost_usd` for API billing; a window disappears from the JSON
  once its `resets_at` passes. Warnings appear as a line above the input.
- **Claude Desktop** (screenshots in the issue): a muted banner above the
  composer, "You've used 79% of your Fable 5 limit · Resets Fri, Aug 21,
  12:00 AM", dismissable. On a hit limit: "Usage limit reached · Auto-resuming
  at 10:20 PM · [View details] [x] Auto-continue when limits reset · [Try
  again]".
- **Codex CLI**: `/status` prints "5h limit: 73% left (resets 1 Apr, 18:22) ·
  Weekly limit: 54% left (resets 5 Apr, 09:15)"; the composer corner shows
  "NN% context left"; a warning line appears when a window is nearly spent.
- **OpenCode**: footer shows context tokens and percent plus the running
  session cost ("1.2K (12%) · $0.05"); per session, no remaining quota.
- **Cursor**: no native indicator; third-party extensions put "requests used /
  remaining, next reset" on the status bar. **ChatGPT desktop**: no counters,
  an inline "limit reached, try again after ..." card when limited.

Common denominator that Coddy adopts: percent **used** per window plus a reset
time, a warning threshold at 80 %, a distinct "limit reached" state with the
resume time, and one command that prints the full breakdown.

## 3. What already exists

Coddy side:

- The console footer (`external/cli/footer.go`) renders two dim lines: cwd,
  branch and title; then `↑in ↓out  N.N%/ctx` left and `(provider) model •
  reasoning` right. Counters arrive as ACP updates `token_usage` and
  `usage_update` through `applyLoopMessage` (`external/cli/updates.go`), which
  drops updates whose session id is not the adopted one.
- Update fan-out differs per surface `[rev]`: under ACP stdio and the local
  console the manager-wide `acp.UpdateSender` (`m.server`) reaches the client;
  under `coddy serve` that sender is a `serverRef` with no ACP server behind it
  (`cmd/coddy/http.go`), so it is a no-op, and the SSE bridge exists only as
  the `sender` argument of `HandleSessionPromptWithSender` while a turn
  streams. Out-of-turn server-wide events go to `GET /coddy/events`
  (`external/httpserver/events_hub.go`), fed by `Manager.AddTurnObserver`.
  `HandleSessionReady` is called by the console and the ACP server only; the
  SPA selects its model through `metadata.model` at prompt time
  (`server_metadata.go`), never through `HandleSessionSetConfigOption`.
- The remote console client (`internal/remote`) maps SSE frames back to ACP
  updates during a turn and calls `/coddy/*` REST otherwise; `/v1/models`
  gives it `owned_by` per model but no provider type. `[rev2]` The selector
  id itself names the provider row: `models[].model` is `<provider>/<upstream
  id>` and `ModelEntry.ProviderName()` is the part before the first `/`, so
  every client can find the row name without config access; the upstream id
  after the slash is what the server resolves (`ResolvedLLM.Model`).
- `[rev2]` Turns enter the manager through several doors:
  `HandleSessionPromptWithSender` (console sender, ACP via
  `HandleSessionPrompt` with the manager sender and nil opts, the HTTP
  composer with the SSE bridge, `coddy -p`, the Telegram gateway, the
  background wake) and, for a turn resumed after a permission answer in the
  SPA, `runPermissionResume` in `external/httpserver/permission_resume.go`,
  which admits the turn through `Manager.BeginTurn` and runs the agent with
  its own relay sender, and `Manager.RunPlan` (a saved plan run from the SPA,
  `external/httpserver/coddy_plans.go`), which admits through `beginTurn` and
  calls the runner with a no-op sender (`[rev3]`). Subagent child turns also
  pass through `HandleSessionPromptWithSender` with `subagentTurn` set.
  `beginTurn` is the single admission path of every door; its `finish`
  closure cancels the turn context and releases the turn. Early returns
  exist between admission and the runner (hydration errors, the ask-mode
  refusal). `HandleSessionPromptWithSender` returns right after `m.runner`
  on error, and the streaming handler then finishes the SSE stream at once.
  The composer stream already sends an idle keepalive every 15 s
  (`bridge.go`), so proxies see traffic during long turns.
- The `neuraldeep` provider type resolves its key from `api_key` /
  `api_key_command` / `NEURALDEEP_API_KEY` or the hub login stored at
  `$CODDY_HOME/providers/<name>/neuraldeep-auth.json`
  (`llm.neuralDeepEffectiveKey`), and its API base from the two-deployment
  allowlist (`llm.neuralDeepAPIBase`; any other `api_base` is discarded, and
  only `CODDY_NEURALDEEP_BASE_URL` redirects the process, which is how the
  existing stand-in tests work). `config.ResolveLLM` yields the upstream model
  id (`ResolvedLLM.Model`, without the `provider/` selector prefix).
- 429 handling: `internal/llm/resilient.go` reads `Retry-After-Ms` /
  `Retry-After`, then the body phrase `Limit resets at: <UTC>`, then `retry in
  Ns`, and caps the pause at `RetryMaxDelay`, which `ResilientOptionsFromAgent`
  never overrides: it is always 60 s. The wrapper is applied at every provider
  construction site (agent turns, subagents, the memory copilot, HTTP direct
  completions and the title/enhance helpers, compaction).
- `/coddy/*` routes sit behind `authGate` (`external/httpserver/auth.go`) as
  soon as a bearer token is configured (`--auth-token`, `CODDY_HTTP_TOKEN`,
  `httpserver.auth_token`); without one the server warns when it listens on a
  non-loopback address. The remote client sends the bearer on every call.
- `internal/llm` does not import `internal/acp` today.
- Test harnesses: `features/cli_tui.feature` drives the console over a stub
  runner and an in-memory terminal (package runs sequentially, no
  `t.Parallel`); `features/neuraldeep_auth.feature` has a stand-in hub and a
  stand-in OpenAI-compatible API (`httptest`) selected through
  `CODDY_NEURALDEEP_BASE_URL`.

NeuralDeep side (repository `neuraldeep-llm-proxy`, nothing to build):

- `GET https://api.neuraldeep.ru/v1/limits` (alias
  `hub.neuraldeep.ru/api/v1/limits`), `Authorization: Bearer sk-...`, schema
  version 1, strictly read-only, `Cache-Control: no-store`. Documented in
  `docs/services/api/api-reference.md` (Limits API) with the polling advice
  "no point polling more often than every 15-30 s (edge limits 120/min per
  IP)". Errors: 401 unknown key, 403 blocked user, 503 with `Retry-After: 5`
  when the counter store is down (never 200 with zeros).
- Live payload from a pro key (2026-09-06), abridged:

  ```json
  {"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro",
   "unlimited_volume":false,"options":[],"bypass":false,"fair_use":true,
   "key":{"name":"coddy","status":"ok","billing_mode":"wallet","cap":null},
   "decision":{"scope":"chat","can_request":true,"blockers":[],"retry_after_sec":null},
   "chat":{"session":{"used":407,"limit":15000,"remaining":14593,"reset_in_sec":777,
                      "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
           "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                   "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
           "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},
           "cooldown_sec":0,"scope":"account"},
   "daily_capacity":{"pct_used":0.0,"exhausted":false,"resets_at":"2026-09-07T00:00:00+00:00"},
   "night":{"enabled":true,"active":false,"capacity_factor":2,"window_start_msk":0,"window_end_msk":6},
   "wallet":{"balance_rub":-1229.24,"spent_rub_30d":2000.74},"kimi":null}
  ```

  Facts that shape the design: `limit`/`remaining` are `null` when a window
  does not apply (`fair_use:false` for wallet or bypass keys,
  `unlimited_volume`); `options[].models` lists upstream model ids that bypass
  the session/week windows (Qwen ∞); `wallet` is `null` unless the account has
  a balance or spend, and the balance **can be negative**; every number is
  already effective (the night x2 is applied); `decision.blockers` is drawn
  from a fixed vocabulary (`session_cooldown`, `session_exhausted`,
  `week_exhausted`, `rpm_exhausted`, `abuse_cooldown`,
  `daily_capacity_exhausted`, `key_blocked`, `key_cap_blocked`,
  `wallet_empty`) and `retry_after_sec` says when the timed blockers lift.
- On 429 the gateway sends `Retry-After`, `Retry-After-Ms`,
  `X-RateLimit-Remaining-Requests: 0`, `X-RateLimit-Reset-Requests` and the
  body phrase `Limit resets at: YYYY-MM-DD HH:MM:SS UTC`
  (`litellm/retry_headers.py`). Successful responses carry no quota headers,
  which is why polling the endpoint is the only source.

The policy of that endpoint is binding for Coddy too: no dollar figures, money
only as the account's own rubles, subscription volume only as counters and
percentages.

## 4. Design

### 4.1 One update type for every surface

`internal/acp/types.go` gains `ProviderUsageUpdate` (`sessionUpdate:
"provider_usage"`). `[rev]` It is NeuralDeep-shaped today (`plan`, `wallet`,
`unlimitedModels` have no meaning elsewhere); consumers branch on
`providerType`, and a later provider adds its own fields rather than
pretending to fill these.

```go
type ProviderUsageUpdate struct {
    SessionUpdate string        `json:"sessionUpdate"`         // "provider_usage"
    Provider      string        `json:"provider"`              // provider row name
    ProviderType  string        `json:"providerType"`          // "neuraldeep"
    ObservedAt    string        `json:"observedAt,omitempty"`  // payload observed_at, RFC3339 UTC
    Plan          string        `json:"plan,omitempty"`        // tier: free | starter | pro
    KeyName       string        `json:"keyName,omitempty"`
    Windows       []UsageWindow `json:"windows,omitempty"`     // session, week, day
    Rate          *UsageRate    `json:"rate,omitempty"`        // chat.rpm, the live minute
    CooldownSec   int           `json:"cooldownSec,omitempty"` // chat.cooldown_sec
    Wallet        *UsageWallet  `json:"wallet,omitempty"`
    Blocked       bool          `json:"blocked"`               // a request would fail now
    Blockers      []string      `json:"blockers,omitempty"`    // vocabulary above, plus user_blocked
    RetryAt       string        `json:"retryAt,omitempty"`     // observed_at + retry_after_sec
    RetryInSec    int           `json:"retryInSec,omitempty"`  // retry_after_sec as received
    Unlimited     bool          `json:"unlimited,omitempty"`   // no volume windows for this key
    UnlimitedModels []string    `json:"unlimitedModels,omitempty"` // options[].models, upstream ids
    BlockedModels []UsageBlockedModel `json:"blockedModels,omitempty"` // blocked_models[], model gates [rev5]
    Stale         bool          `json:"stale,omitempty"`       // windows come from an earlier fetch
    Error         string        `json:"error,omitempty"`       // unauthorized | unavailable | invalid
    Unsupported   bool          `json:"unsupported,omitempty"` // provider type has no usage source [rev2]
    FetchedAt     string        `json:"fetchedAt,omitempty"`   // local time of the fetch, RFC3339 [rev2]
    RefreshPending bool         `json:"refreshPending,omitempty"` // a refresh is deferred by the floor [rev4]
    RefreshInSec  int           `json:"refreshInSec,omitempty"`   // when it fires, server-relative [rev4]
}

type UsageWindow struct {
    ID          string  `json:"id"`     // "session" | "week" | "day"
    Label       string  `json:"label"`  // "3h" | "week" | "day"
    // Counters are optional: the day window is percent-only. [rev]
    Used        *int    `json:"used,omitempty"`
    Limit       *int    `json:"limit,omitempty"`
    Remaining   *int    `json:"remaining,omitempty"`
    UsedPercent float64 `json:"usedPercent"`
    Exhausted   bool    `json:"exhausted,omitempty"`
    ResetsAt    string  `json:"resetsAt,omitempty"`   // server clock, for display
    ResetInSec  int     `json:"resetInSec,omitempty"` // for local deadlines, age-corrected at delivery [rev]
}

type UsageRate struct {
    Used       int `json:"used"`
    Limit      int `json:"limit"`
    Remaining  int `json:"remaining"`
    ResetInSec int `json:"resetInSec"`
}

type UsageWallet struct {
    BalanceRub  float64 `json:"balanceRub"`
    SpentRub30d float64 `json:"spentRub30d"`
}
```

Custom update kinds already exist (`token_usage`, `usage_update`,
`memory_phase`), so ACP clients that ignore unknown kinds keep working. The
update never carries the key, the hub URL, or a dollar amount.

`[rev2]` Relative durations are age-corrected on every delivery: the cache
stores the local fetch time, and `ResetInSec`, `RetryInSec` and
`Rate.ResetInSec` are decremented by the snapshot's age (clamped at 0) when a
cached snapshot is handed out, so a 20 s old answer does not schedule a
refresh 20 s late (codex 2, claude 6). `FetchedAt` travels with the update so
a surface can show "observed 12s ago". `Unsupported` is the in-band twin of
the REST envelope's flag (codex 1, cursor 2, claude 9). `[rev3]` The update
is **account-scoped**: it carries nothing derived from one session's model,
so one cached snapshot serves every session of the provider and a session on
a Qwen ∞ model can never make another session's limited model read as
unlimited (codex 1, cursor 1, claude 1). Each client compares
`UnlimitedModels` with the upstream id it already knows, the selector suffix
after the first `/` (`SplitModelRef`, section 3).

Rules for the two failure families `[rev]`:

- transport and credential failures set `Error` and leave `Blocked` alone:
  401 → `unauthorized`; 5xx, 429, network errors and timeouts →
  `unavailable`; undecodable body or `schema != 1` → `invalid`;
- policy denials set `Blocked` and `Blockers`, never `Error`: 403 → `Blocked`
  with `user_blocked`; a 200 with `decision.can_request:false` → its
  `blockers`; `daily_capacity.exhausted` and `key.status != ok` are already
  in the server's list.
- `unauthorized` is sticky: automatic triggers skip the fetch until `/usage`,
  a REST `?refresh=1`, or a config / credential change (`[rev]` coddy 6).

### 4.2 Fetcher: `internal/llm/neuraldeep_usage.go`

`FetchNeuralDeepUsage(ctx, apiBase, key string, hc *http.Client)
(*NeuralDeepUsage, error)` does `GET {apiBase}/limits` with the bearer key,
decodes the schema-1 payload into a typed struct (unknown fields ignored) and
returns it unmapped; the timeout comes from the caller's context, with a 10 s
guard for callers that pass none (`[rev]` coddy 10). `[rev]` `internal/llm`
keeps not importing `internal/acp`: the mapping to `ProviderUsageUpdate`
lives in `internal/session` (4.3), which imports both.

Typed errors: `NeuralDeepUsageError{Status int, Kind string}` with the kinds
of 4.1. Error text passes through `redactNeuralDeepSecrets` (`sk-[A-Za-z0-9_-]+`,
which covers the whole bearer value); the fetcher logs nothing itself.

Key and base resolution reuse the provider's own rules
(`neuralDeepEffectiveKey`, `neuralDeepAPIBase`, `HTTPClientForOptionalProxy`
for `providers[].proxy`), exposed as `llm.NeuralDeepUsageForProvider(ctx,
provider config.ProviderConfig, authPath string)`, which also returns the
non-secret account fingerprint used as the cache key (4.3): `sha256(type |
resolved base | proxy | key)[:16]` so a rotated key or a switched deployment
is a different cache entry (`[rev]` codex 2).

Mapping rules (in the collector): `chat.session` → window `session`,
`chat.week` → `week`, each with `Label` taken from the payload's `window`
(`3h` as is, `iso-week` shown as `week`, any other value shown verbatim,
`[rev2]` cursor 3); `daily_capacity` → window `day` with `UsedPercent =
pct_used` (the hub computes `round(min(spend/budget, 1) * 100, 1)`, so the
scale is 0-100, clamped again on our side, `[rev2]` cursor 8, claude 12) and
`Exhausted`, counters omitted; `chat.rpm` → `Rate`; `chat.cooldown_sec` →
`CooldownSec`; `wallet` → `Wallet`; `decision` → `Blocked`, `Blockers`,
`RetryInSec` and `RetryAt = observed_at + retry_after_sec` (server clock);
`fair_use:false`, `bypass:true` (`[rev2]` cursor 4) or `unlimited_volume` →
`Unlimited`; `options[].models` → `UnlimitedModels`. Percent for counted
windows is `used/limit*100` in float, clamped to [0, 100], and 0 when
`limit` is null or 0 (`[rev]` coddy 7); a counted window with a null limit
is dropped. `[rev3]` The `day` window is mapped even when `Unlimited` is set:
the hub applies the daily budget to wallet and bypass keys too (cursor 8).
Its `ResetInSec` is derived as `resets_at - observed_at` because the payload
gives it no relative value (claude 5). A timed blocker whose
`retry_after_sec` is null takes `RetryAt` from the window it names
(`session_exhausted` → session, `week_exhausted` → week,
`daily_capacity_exhausted` → day; claude 7).

`[rev3]` The fetcher's error carries the server's pause:
`NeuralDeepUsageError{Status, Kind, RetryAfter}` with `RetryAfter` parsed from
`Retry-After` as delay seconds or an HTTP-date on 429 and 503 (codex 2), which
the collector turns into its backoff deadline.

### 4.3 Collector: `internal/session/provider_usage.go` (default build)

`[rev2]` The file is `provider_usage.go`, not `usage.go`: `manager_usage.go`
already owns the context-window `usage_update` (claude 13).

The manager owns the cache and the server-side triggers so every surface gets
the same numbers:

- `providerUsageCache` keyed by provider name **and** the account fingerprint:
  last update, local fetch time, in-flight flag (a mutex-guarded in-flight
  map coalesces concurrent callers; no `x/sync` promotion, `[rev]`), the
  sticky `unauthorized` mark, a backoff deadline and a pending-refresh
  timer. `[rev3]` Pacing follows the hub's advice with one rule for every
  caller (codex 5, cursor 3): automatic reads (session ready, the reset
  timer, REST without `refresh`) return the cached snapshot while it is
  younger than **20 s**; refreshes (`/usage`, REST `?refresh=1`, the
  turn-end publish) run at once when the last fetch is **15 s** or older,
  and otherwise are **deferred**: the entry remembers that a refresh is
  wanted and fires it when the floor expires, delivering the result through
  `m.server` and the observers. So a turn that ends inside the floor updates
  the footer a few seconds later instead of being dropped or turning into a
  5 s poll. A `Retry-After` on a 429 or 503 becomes a backoff deadline: until
  it passes every caller gets the cached snapshot marked `Stale`.
  `storeConfig` (the one place every config swap passes, `[rev3]` claude 9)
  and `DropProviderUsage(name)` (called by the neuraldeep-auth login and
  logout handlers, `[rev2]` claude 5) clear the entry, including the sticky
  mark, which is keyed by the fingerprint.
- `(*Manager) ProviderUsage(ctx, providerName string, refresh bool)
  (*acp.ProviderUsageUpdate, error)`: resolves the provider row; for a
  provider type without a usage source it returns an update with
  `Unsupported: true` (`[rev]` claude 12, so clients cache the answer);
  otherwise the cached or freshly fetched update, age-corrected. On a fetch
  error the update carries `Error`, `Stale: true` and the previous windows,
  so a surface keeps showing the last known numbers.
- `[rev3]` Turn-end publish hangs on the `finish` closure that `beginTurn`
  hands every door (claude 3, codex 3): after the cancel and the release,
  `finish` starts the refresh when the turn **ran the runner**
  (`State.MarkTurnRan`, set right before the runner call by the prompt path,
  `RunPlan` and the permission resume, reset at admission, so hydration
  errors and the ask-mode refusal fetch nothing), the turn is **not a
  subagent turn** (`subagentTurn` implies skip: children publish nothing and
  a parent with staggered children cannot turn into a poll, claude 2), and
  the caller did not set `SkipUsagePublish` (threaded into `beginTurn`;
  `coddy -p`, the Telegram gateway and the background wake set it, `[rev2]`
  claude 4; default on so a new surface gets the numbers unless it says it
  cannot show them). The refresh resolves the session's effective model →
  provider and, still inside `finish` and under the cache mutex, either takes
  the in-flight slot or records the deferred refresh, so a REST pull that
  arrives right after the stream closed joins that fetch instead of reading
  the pre-turn snapshot (claude 4). The fetch itself runs asynchronously
  under its own 5 s context (never the turn context, `[rev]` claude 3) and
  delivers through `m.server` **and** the usage observers below. Nothing is
  written to the turn's sender after the turn (codex 4): there is no in-band
  wait at all, so the turn result, the `[DONE]` frame and the turn lock are
  never delayed (cursor 5, claude 7, coddy 1).
- Server-side triggers:
  1. **session ready** (`HandleSessionReady`): asynchronous, 5 s budget,
     through `m.server` (reaches the console and ACP clients; on HTTP nobody
     is listening there, the SPA uses REST);
  2. **turn end**: the `finish` closure as above; on HTTP the result reaches
     the events hub, and the SPA and the remote console additionally pull it
     over REST once their stream closes (4.4, 4.6), which the cache
     coalesces into the same fetch. A pull that finds the refresh deferred
     (the snapshot's `fetchedAt` precedes the turn's end) schedules one more
     pull when the floor expires, and no more (`[rev3]`).
  Model changes are client-driven (`[rev2]` codex 3, claude 8): the console,
  local or remote, asks its backend for `ProviderUsage` after `/model` and
  applies the answer itself; the SPA re-fetches REST after picking a model.
  No server-side model trigger exists, because `State.SetSelectedModelID` is
  called from several HTTP paths without a manager callback.
- `Manager.AddUsageObserver(fn func(sessionID string, u
  acp.ProviderUsageUpdate)) (remove func())` `[rev]`: the same pattern as
  `AddTurnObserver`, so the HTTP server can forward out-of-turn updates to
  `GET /coddy/events` as event `provider_usage` (claude 2). Observers receive
  every fresh snapshot (codex 4) and must not block.
- The server never polls the hub on a timer. Surfaces keep their own clock:
  they schedule one refresh at `receivedAt + ResetInSec + 2 s` using the
  age-corrected value of the **windows** and the retry deadline only, never
  the per-minute `Rate` (`[rev3]` coddy 4, otherwise the timer would fire
  every minute), skipping values at or below zero (claude 5); when the
  answer's `resetsAt` has not moved they retry once after 30 s and then give
  up until the next trigger. Display uses the hub's absolute `resetsAt`,
  deadlines use the relative value: under clock skew the two differ by the
  skew, on purpose (coddy 8).
- `[rev2]` Test hook `WaitProviderUsageIdle(timeout)` joins in-flight
  fetches, so a console scenario's teardown cannot leak a fetch into the next
  scenario's stand-in server (claude 11; the stand-in is selected through a
  process-wide environment variable).

### 4.4 Transport

- **HTTP bridge**: `provider_usage` becomes a named SSE event in
  `Sender.SendSessionUpdate` (`external/httpserver/bridge.go`), whose
  `default` branch otherwise drops it (needed by the auto-resume updates of
  4.7, sent during a turn); the events hub publishes the same frame for the
  out-of-turn updates. `[rev2]` `GET /coddy/events` sits behind the same
  bearer policy as REST (`authGate`; EventSource clients pass
  `?access_token=` as `isSSETokenPattern` allows), so the hub is never an
  easier path to the wallet than REST (cursor 7).
- **REST**: `GET /coddy/providers/{name}/usage[?refresh=1]` (`-tags http`)
  answers `200 {"ok":true,"usage":<ProviderUsageUpdate>}`; `200
  {"ok":false,"unsupported":true}` for a provider type without a usage
  source; `200 {"ok":false,"error":"...","usage":<stale update or null>}`
  when the fetch failed (`[rev]` codex 3: the stale numbers travel too);
  `404` for an unknown provider name. The route inherits `authGate` like
  every `/coddy/*` route (`[rev]` claude 5, cursor 7, coddy 3: the wallet is
  account data, and the existing bearer policy is the gate; the docs say so,
  and the no-token warning already covers non-loopback listeners). Added to
  `openapi.go` and `docs/http-api.md`.
- **Remote console** (`internal/remote`): the SSE frame maps to the update in
  `onFrame`; `HandleSessionReady` (not `HandleSessionNew`, `[rev]` claude 6:
  the console adopts the session id only after `session/new` returns) injects
  the REST answer locally; `[rev2]` after a turn's stream closes
  (`HandleSessionPromptWithSender` in the remote handler, success or error)
  and after the remote `/model` branch it calls REST again (claude 8), and
  `/usage` calls it with `refresh=1`. The provider row name is the selector
  prefix (section 3, `[rev2]` cursor 6). The `unsupported` answer is cached
  per provider so non-NeuralDeep models cost no round trips (`[rev]` claude
  12). The console's `backend` interface gains `ProviderUsage(ctx, name
  string, refresh bool)` with both implementations; the console applies the
  returned update itself, which is why local and remote behave the same.
  `[rev3]` A sticky `unauthorized` on a remote server is informational for
  the console (the key lives on the server; re-login happens there), and a
  remote `/usage` clears the mark through `refresh=1` (coddy 5).
- **ACP stdio**: the update is sent as-is; Zed ignores unknown kinds.

### 4.5 Console (the status bar)

`footer.go` gets a third dim line, rendered only while a usage update for the
**active model's provider** is held (switching to another provider hides it):

```
pro • 3h 3% (resets 20:59) • week 7% (resets Mon 03:00) • wallet -1 229 ₽
```

- Segment order: plan, the `session` window, the `week` window, the `day`
  window only when its percent is above 0 or it is exhausted, wallet only
  when present. `[rev]` Drop order when the line does not fit the width:
  wallet, then day, then week, then plan; what remains is truncated like the
  other footer lines (cursor 10).
- Reset time in local time: `15:04` when within 24 h, `Mon 15:04` within
  7 days, `Jan 2` beyond. The counters do not tick; the line re-renders on
  every update and on the reset timer below.
- `[rev]` The application owns a single re-armable `time.Timer`
  (`usageResetTimer`): every update re-arms it to the earliest `receivedAt +
  ResetInSec + 2 s` among the windows and the retry deadline; firing posts an
  internal loop message that asks the backend for a forced refresh; a
  session or model switch re-arms or stops it, quit stops it (codex 4, coddy
  5). Tests drive it with an injected clock.
- Colour: dim by default; a window at **80 % or more** renders its segment in
  the warning role; a negative wallet balance renders in the warning role.
- `[rev]` Blocked rendering, by blocker (cursor 8, claude 9): timed blockers
  (`session_exhausted`, `week_exhausted`, `daily_capacity_exhausted`,
  `abuse_cooldown`, `session_cooldown`) replace the windows with `limit
  reached (resets 20:59)` in the error role, the time being `RetryAt`;
  short timed blockers under a minute (`rpm_exhausted`, a short cooldown)
  render `rate limited (retry in 42s)`; non-timed ones render `key blocked`,
  `wallet empty`, `account blocked`; an unknown id renders `blocked:
  <id>`. `Error: unauthorized` renders `<provider>: key rejected, run coddy
  providers login <provider>` with the row name from the update (`[rev3]`
  codex 5, cursor 7); `unavailable` and `invalid` keep the last numbers and
  append `(stale)`. `[rev3]` Every string that came from the hub (plan, key
  name, window label, blocker id) passes `tui.SanitizeText` before it is
  rendered, and a formatting test feeds control characters and ANSI
  sequences through the footer (codex 5).
- `[rev3]` When the update says `Unlimited`, or the active model's selector
  suffix is in `UnlimitedModels` (compared on every render, cursor 9), the
  windows are replaced by `∞ volume`; the wallet and the day window stay.
- `[rev2]` With no windows to show (first fetch failed, `Error` set, nothing
  stale), the line shows only the error segment for `unauthorized` and is
  hidden for `unavailable` and `invalid` (cursor 9).
- `[rev2]` Wallet runes are fixed for width measurement (cursor 12): ASCII
  `-` for a negative balance, a plain space as the thousands separator, the
  ruble sign `₽` (U+20BD, width 1 under go-runewidth): `-1 229 ₽`. The
  fixture for the truncation tests carries exactly these runes.
- Transcript notices (once per window per reset period, remembered by
  `resetsAt`): at 80 % `You've used 82% of your NeuralDeep 3h limit · resets
  20:59` (warning role); on a timed block `Usage limit reached · resets
  20:59` (error role). They mirror the Claude Desktop banner without a modal.
- `/usage` (client-side slash command, listed in `/hotkeys` and the
  autocomplete) forces a refresh and prints the breakdown as a dim block; the
  day window and the rate always appear here even when the footer hides them
  (`[rev]` codex):

  ```
  NeuralDeep · pro · key coddy
    session (3h)   ▮▯▯▯▯▯▯▯▯▯    3%    407 / 15 000   resets 20:59
    week           ▮▯▯▯▯▯▯▯▯▯    7%  9 981 / 150 000  resets Mon 03:00
    day            0%
    rpm            0 / 120 this minute
    cooldown       none
    wallet         -1 229 ₽ (2 001 ₽ spent in 30 days)
    observed 2s ago
  ```

  `[rev2]` The cooldown row shows `CooldownSec` as `12m 05s` when it is above
  zero and `none` otherwise (cursor 11).

- `[rev]` The wallet stays on the footer on purpose: the issue asks for it
  there, and the footer is the operator's own terminal (claude 13). `[rev12]`
  The switch shipped as `providers[].usage_limits_panel` (default `true`):
  off, the row is never read, reads answer `unsupported` with `disabled`, and
  every surface stays quiet, so a shared screen shows nothing.

The console has no i18n; strings are English literals like the rest of
`footer.go`.

### 4.6 SPA (sub-feature 3)

- `[rev11]` A **usage section** at the end of the context popover
  (`UsageSection.tsx` in `ContextBreakdownPopover.tsx`) when the selected
  model's provider reports usage, the way Claude Desktop lists its plan
  limits under the context window: the provider and plan, one meter per
  metered window with its reset time and percent used, the wallet, and a
  note for a hit limit, a rejected key, an unlimited model, a stale read or
  a turn waiting for the reset. The first cut put a pill with a tooltip next
  to the context ring; the operator asked for the Claude Desktop placement
  and no extra control in the composer, so the pill went.
- A **banner** above the composer at 80 % and on a block, with the Claude
  Desktop wording for a timed block and the cause for the others (an empty
  wallet, a blocked key or account, a rate limit), dismissable per provider
  row and reset period (localStorage keyed by the row, the window and
  `resetsAt`).
- Data `[rev]`: REST on session open and model change, `[rev2]` REST again
  once a turn's stream ends (`[DONE]` or an error frame), the
  `provider_usage` SSE frame during turns (auto-resume, 4.7), and the same
  event on `GET /coddy/events` for turns other clients run (claude 2). i18n keys in `en.ts` and `ru.ts`; `DESIGN.md` and
  `docs/ui.md` sections; screenshots in the PR per
  `.claude/rules/workflow.md`, `[rev11]` kept to the default (dark) and the
  light theme at 1280 px plus one stacked-shell shot, PNG only.

### 4.7 Auto-resume after a hit limit (sub-feature 4, separate PR)

Claude Desktop's "auto-continue when limits reset". `[rev]` Round 1 rejected
the wait inside the resilient wrapper (cursor 2, coddy 4, claude 4, codex 5):
the wrapper has no session or sender, it wraps every provider (subagents,
memory copilot, HTTP helpers, compaction), and its cap is a fixed 60 s.
`[rev2]` Round 2 added that the wrapper today retries three times at the cap
before giving up, so a long reset costs three futile requests and about three
minutes before anyone learns of it (codex 6, claude 10), and that heartbeats
must beat common 60 s idle proxies (cursor 10).

- **Default path (already delivered by 1 and 2)**: the turn ends with the
  provider error, the turn-end refresh publishes the `Blocked` update with
  `RetryAt`, the footer and the notice show the reset time, and the operator
  resumes with the next prompt.
- **Prerequisite, shipped first in the same PR**: a typed
  `llm.QuotaResetError{ResetAt, Delay, Cause}` returned by the resilient
  wrapper the moment the server-requested pause exceeds what the capped
  retries could ever cover, `RetryMaxDelay * (RetryMax + 1)` (`[rev3]`
  claude 6: a 90 s `Retry-After` still succeeds through today's three capped
  waits, and `features/llm_retry_after.feature` pins that), sourced from
  `Retry-After` headers or the `Limit resets at` phrase, both parsed today;
  `X-RateLimit-Reset-Requests` stays unparsed. Above that threshold the
  retries cannot succeed, so every provider fails fast instead; its own
  scenario pins the threshold. `[rev3]` A pause longer than
  `wait_for_limit_reset_max_ms` fails immediately as well; the agent never
  sleeps the maximum and retries before the reset (codex 6).
- **Opt-in wait** `agent.wait_for_limit_reset` (bool, default `false`) with
  `agent.wait_for_limit_reset_max_ms` (default 4 h): only the top-level agent
  turn reacts to `QuotaResetError`; subagents, helpers and compaction keep
  failing fast. The agent sends a `ProviderUsageUpdate` with `Blocked` and
  `RetryAt` through its turn sender, waits under the turn context (`esc`
  cancels) re-sending the update every **20 s** so the countdown stays
  visible (the composer stream's own 15 s keepalive is what keeps proxies
  alive, `[rev3]` claude 8), then re-issues the same LLM call:
  the user message is already persisted, so nothing is duplicated. Nothing is
  retried after streamed output was emitted (the wrapper's rule).
- Surfaces: the console status line reads `Usage limit reached · resuming at
  20:59` without a running counter, the SPA banner reads `Auto-resuming at
  20:59`.
- Known trade-off, documented: the turn lock and the stream stay open for the
  wait; a proxy with an idle timeout shorter than the heartbeat drops the
  stream and the SPA reconnects through the composer relay; the ACP and
  console streams are local processes without a proxy. The option is off by
  default for exactly this reason.
- Config schema sync per the workflow rule (`docs/config.schema.json`,
  `docs/config-reference.md`, `config.example.yaml`, `UISchemaMap`,
  `configure-coddy` skill). Its design gets its own review before it is built.

`[rev4]` Build notes, reviewed before the code:

- The wrapper's threshold is what the retries still available could wait:
  `RetryMaxDelay * (RetryMax - attempt)`, 180 s by default before the first
  retry, nothing with retries disabled (so every named pause is a reset
  then), capped by the caller's `RetryBudget`, which the agent sets to its
  first-token timeout for streamed transports since that timer would cut a
  longer sleep anyway (`[rev5]` fresh reviewer 1, 2). `serverRetryDelay`
  already yields the pause; the check runs after the retryable gate (a 429
  that arrived mid-stream is never re-issued) and before the attempt gate,
  so no request is repeated once the pause is known to exceed the budget.
  `QuotaResetError` wraps the cause (`errors.Is` / `errors.As` on the
  original keep working) and is never retryable itself.
- While it waits the agent sends the `provider_usage` update with a new
  `resuming: true` field next to `blocked: true`, `retryAt` and `retryInSec`
  from the error, and the row's `provider` / `providerType` resolved from
  the session's model. It goes through the turn sender only (ACP, the
  console, the SSE turn stream), never into the manager's cache, so the
  next turn-end refresh replaces it with the hub's numbers. The console
  routes a `resuming` update to its live status row before the footer,
  the notices and the reset timer see it (`[rev5]` fresh reviewer 4).
- Only a top-level turn waits (`subagentDepth() == 0`; `RunPlan` runs at
  depth 0 and waits the same way). A subagent's turn fails fast with the
  error and the parent reads the report as today; the memory copilot,
  compaction and the HTTP helpers never see the option.
- The wait is bounded by `wait_for_limit_reset_max_ms` (default 4 h, an
  explicit 0 never waits, a negative value is rejected by validation), a
  total per turn: what earlier waits of the same turn spent counts, the
  retry wrapper's own sleeps after a 429 included, on calls that succeeded
  afterwards too (the wrapper charges them to the turn's `llm.LimitLedger`
  and reads the total when it judges a new pause; `[rev7]` codex 3), so a
  provider that keeps naming short resets cannot hold the turn open without
  end; a resumed permission keeps the account, a new user turn starts one.
  The wrapper judges a call against the ledger as it was before the call
  plus the call's own time, so a sleep is never booked twice (`[rev8]`
  codex 4). A 429 that names no pause never becomes a reset: its ordinary
  backoff runs while the budget allows and the call then ends with the
  provider's own error, so an empty budget ends it at once, a small one
  bounds the ordinary retries, and no countdown is ever built on a guessed
  moment (`[rev9]` codex 5). The wrapper keeps the two bounds apart: the
  first-token timer is the call's own budget (this call's time only), the
  wait's maximum with the ledger is the turn's, so a turn that spent much
  of its maximum still retries a short named pause under the timer instead
  of counting it down; `Run` starts the ledger before the built-ins, and
  compaction's provider carries neither bound nor ledger (`[rev10]` fresh
  reviewer 2, 4); with the option on the same maximum caps the
  wrapper's retry sleeps on a limit, and an explicit zero means no sleep on
  a limit anywhere (`RetryBudgetSet`) (`[rev5]` codex 2, fresh reviewer 3;
  `[rev6]` codex 1, 2). A pause that would exceed it fails fast with the
  error, before any sleep. The
  turn context bounds it too: a user Stop ends the turn as cancelled, any
  other cancellation ends it with the error and names the cause. The loop
  re-runs the same iteration with the same messages (`turn--; continue`)
  and does not consume a `max_turns` slot; the failed call persisted
  nothing and streamed nothing (a chunk of any kind, tracked by the loop
  itself, rules the wait out), so nothing is duplicated.
- Surfaces: the console's live status row reads `Usage limit reached ·
  resuming at 20:59` while the footer keeps the hub snapshot, and goes back
  to waiting for the model at the reset; the SPA banner of sub-feature 3
  (#144) reads `Usage limit reached · Auto-resuming at 20:59` in the
  warning tone when the turn stream's frame carries the flag (the events
  stream never carries it). The wrapper's `RetryBudget` counts the sleeps
  already taken, so a chain of short pauses cannot run into the first-token
  timer unreported (`[rev5]` fresh reviewer 1, codex 3), and keeps
  headroom for the request after a pause (a quarter of a small budget,
  five seconds of a large one) so that request is not cut by the timer
  either. At the reset the console's row goes back to the model and the
  footer asks the hub for the numbers after the reset (`[rev6]` fresh
  reviewer 3, 4). A maximum under the ladder's 60 s cap also bounds the
  turn's ordinary 429 retries, which the reference says (`[rev6]` fresh
  reviewer 2).
- Tests: `features/llm_retry_after.feature` gains the threshold scenario
  (a 600 s pause fails after one request as a quota reset error); the agent
  loop's happy path is a godog scenario over a fake provider whose first
  call fails with a one-second reset and whose second answers; the config
  helpers, the console status and the SPA banner have unit tests.

### 4.8 Security and privacy

- The key never leaves `internal/llm`: not in updates, not in logs, not in
  REST answers; error text is redacted with the existing `sk-***` filter.
- No dollar figures anywhere, matching the endpoint's policy; the wallet is the
  account's own rubles.
- The fetcher is read-only and cheap (Redis reads on the hub); the 20 s cache,
  the 15 s floor with deferred refreshes and the in-flight coalescing keep a
  busy process far below the edge limit.
- REST exposure follows the server's bearer policy (`authGate`); the docs
  name the wallet and key status as the account data the route returns.

### 4.9 Failure modes

| Situation | Behaviour |
|---|---|
| No login, no key | `Error: unauthorized` without a request; console line says how to sign in; sticky until `/usage` or a credential change |
| 401 from the hub (revoked key) | same, after one request |
| 403 (blocked user) | `Blocked`, blocker `user_blocked`, error role |
| 503 / 5xx / 429 / network / timeout | previous numbers kept, `Stale`, `Error: unavailable`; next trigger retries |
| Payload changes shape or `schema != 1` | `Error: invalid`, stale numbers kept; nothing crashes |
| `fair_use:false` (wallet or bypass key) | windows hidden, `∞ volume` plus wallet |
| Model in `options[].models` (Qwen ∞) | same as above for that model only, compared by the client |
| Two sessions on different models of one provider | one account-scoped snapshot; each session applies its own model |
| Turn ends inside the 15 s floor | refresh deferred to the floor's end, delivered through the usual channels |
| Saved plan run from the SPA | publishes like a prompt turn (same `finish`) |
| Negative wallet | shown as `-1 229 ₽` in the warning role |
| Reset passes while idle | timer fires one forced refresh, one 30 s follow-up if the window has not rolled |
| Provider switched to non-NeuralDeep | line hidden, cache kept |
| Remote console | numbers come from the server's own key; identical rendering |
| Cancelled or failed turn | refresh runs in the background under its own context; the failed turn's `Blocked` state reaches the footer |
| Permission-resumed SPA turn | same publish as a composer turn |
| `coddy -p`, Telegram, background wake | no fetch (`SkipUsagePublish`) |
| Hub answered 429 or 503 with `Retry-After` | cached snapshot marked `Stale` until the deadline passes |

## 5. Delivery (BDD)

Layered order, each layer red → green → `make test` → docs → `make lint`:

1. **Core and transport**: `acp.ProviderUsageUpdate`, the fetcher, the
   collector with its cache, triggers and observers, the SSE event, the REST
   endpoint, the events-hub frame, the remote mapping. Spec
   `features/neuraldeep_usage.feature` (`@http`): "The account usage of a
   NeuralDeep provider is readable over REST", "A finished turn refreshes the
   account usage" (the refreshed numbers arrive on `GET /coddy/events` and
   on the post-stream REST pull, `[rev3]` codex 4), "A provider without a
   usage source says so" (stand-in API serving `/limits` behind
   `CODDY_NEURALDEEP_BASE_URL`, stub runner). Unit tests `[rev]`: payload
   mapping (nulls, unlimited, options, negative wallet, blockers, limit 0,
   the day window's derived reset), error kinds, `Retry-After` as seconds
   and as an HTTP-date on 429 and 503, stickiness, cache TTL, floor,
   deferred refresh and coalescing, account and endpoint rotation, cache-hit
   and stale-snapshot timing with a fake clock (codex 2), the publish from
   `finish` on the success and the error return, on the permission-resume
   path and on a plan run, none for a subagent turn or an opted-out caller
   (claude 1, 2, 3), two sessions on different models sharing one snapshot
   (codex 1), observers receiving every snapshot, remote frame mapping and
   the remote post-turn REST pull (the console's own timer covers a deferred
   refresh, the remote client arms none). Docs: `docs/http-api.md`,
   `external/httpserver/openapi.go`, `docs/acp-protocol.md` (a new
   "Coddy-specific session updates" section that also documents
   `token_usage` and `usage_update`, `[rev]` claude 15).
2. **Console status bar**: footer line, thresholds, notices, `/usage`, the
   reset timer. Spec: scenarios in `features/cli_tui.feature` over a
   `neuraldeep` provider whose API base is a stand-in server through
   `CODDY_NEURALDEEP_BASE_URL`: "The footer shows the NeuralDeep session and
   weekly usage", "The footer warns when the session window is nearly spent",
   "/usage prints the account breakdown", "Switching to another provider
   hides the usage line". Unit tests: formatting (reset time buckets, percent
   rounding, drop order and truncation), colour roles, blocker copy, notice
   de-duplication, timer re-arming with a fake clock. Docs: `docs/cli.md`
   (Footer, Commands, Testing), README console bullet, `docs/config.md`
   NeuralDeep section (one sentence). Screenshot of the console footer for
   the PR.
3. **SPA** (second PR): pill, banner, i18n, vitest, DESIGN.md, docs/ui.md,
   screenshots.
4. **Auto-resume** (third PR, #145, branch `claude/coddy-usage-auto-resume`
   on top of #143): config, agent-loop wait, status line and banner wording,
   config schema sync, spec scenarios in `features/llm_limit_wait.feature`
   (the turn waits and answers, off by default, a pause beyond the maximum)
   and the threshold scenario in `features/llm_retry_after.feature`.

`[rev]` Steps 1 and 2 ship in one PR as two commits: the plumbing alone has
no visible surface, and the status bar is what the issue asks for (cursor 12,
coddy 9). The ACP and SSE contract is reviewable from the first commit.

NeuralDeep repository: no code change is needed; one documentation commit
lists Coddy as a consumer of `GET /v1/limits` in
`docs/services/api/api-reference.md` and cross-links this plan, so a schema
change there knows what breaks.

## 6. Alternatives considered

- **Reading `X-RateLimit-*` from inference responses**: the gateway sends them
  only with a 429, so the footer would show nothing until the limit is hit.
- **The console polling the hub itself**: breaks `--remote`, where the key
  lives on the server; also duplicates the SPA path. The manager is the one
  place every surface already listens to.
- **A server-side periodic timer**: costs requests while nobody looks; the
  reset time makes a surface-side event-driven refresh sufficient.
- **`GET /api/cli/usage`** (hub domain): older, lacks `remaining`, `decision`
  and the wallet detail; `/v1/limits` is the endpoint the platform documents
  for agents.
- **A standard ACP update**: the protocol has no quota notification; a custom
  kind follows the precedent of `token_usage`.
- **Persisting the snapshot in session stats**: unnecessary, the ready
  trigger repopulates after a restart, and the numbers are account-wide, not
  per session.
- **Waiting for a reset inside the resilient wrapper** (round 1): rejected,
  see 4.7.

## 7. Addressed concerns (round 1)

- Sender under HTTP (codex 1, claude 1): fixed, 4.3 trigger 2 and 4.4.
- Out-of-turn delivery on HTTP, `HandleSessionReady` not reached by the SPA,
  `metadata.model` path (claude 2): fixed with usage observers and
  `GET /coddy/events`, 4.3, 4.4, 4.6.
- Cancelled turn context and hot-path delay (claude 3, cursor 4, coddy 1):
  fixed, the fetch runs under its own context in the background (round 2
  removed the in-band wait entirely, see 8).
- Turn end must refresh, not read the cache (cursor 1): fixed.
- Cache key and invalidation (codex 2): fingerprint plus `ReplaceConfig`.
- Schema gaps: day window counters, rpm, 403, failure mappings, stale usage
  in REST errors, `RetryAt` from `observed_at` (codex 3, cursor 3, 5, 6,
  coddy 2, 7, claude 8, 9): fixed in 4.1, 4.2, 4.4.
- Reset refresh mechanism and clock skew (codex 4, cursor 11, claude 7,
  coddy 5): application-owned timer from `ResetInSec`, 4.3 and 4.5.
- Auto-resume (codex 5, cursor 2, coddy 4, claude 4): redesigned in the
  agent loop, separate PR with its own review, 4.7.
- REST auth and wallet exposure (cursor 7, coddy 3, claude 5): `authGate`
  documented, wallet kept with the reasoning, 4.4, 4.8.
- Blocker copy and short blocks (cursor 8, claude 9): fixed, 4.5.
- `UnlimitedModels` against the upstream id, on every render (claude 10,
  cursor 9): fixed.
- Narrow terminals (cursor 10): drop order, 4.5.
- Stand-in servers only through the env override, sequential package (claude
  11): stated in 3 and 5.
- `unsupported` marker for the remote client (claude 12): fixed.
- Layering and `x/sync` (claude 14): mapping in `internal/session`, no new
  dependency.
- `schema == 1` required (cursor 13): fixed.
- Provider-agnostic framing (coddy 8): dropped, 4.1.
- Sticky 401 (coddy 6): fixed.
- One PR for 1 + 2 (cursor 12, coddy 9): kept, reasoning in 5.
- Reviewer answers to 7: one 80 % threshold, percent used, day only above 0
  in the footer and always in `/usage`; the turn-end send is asynchronous
  (round 2).

## 8. Addressed concerns (round 2)

- `Unsupported` missing from the type (codex 1, cursor 2, claude 9): added,
  4.1.
- Cached relative durations drift (codex 2, claude 6): age-corrected at
  delivery, `FetchedAt` on the update, 4.1 and 4.3.
- Model-change trigger not wired (codex 3, claude 8): made client-driven for
  the console (local and remote) and the SPA; no server trigger, 4.3, 4.4.
- Turn-end concurrency, sole owner of the turn sender, observers fed by every
  snapshot (codex 4), in-band wait on the hot path and behind the turn lock
  (cursor 5, claude 7): no in-band send at all, asynchronous publish after
  `finish()`, REST pull by the SPA and the remote console, 4.3, 4.4, 4.6.
- Floor versus upstream guidance (codex 5) and turn-end defeated by the floor
  (cursor 1): 20 s TTL for automatic reads, 15 s floor for manual refreshes,
  turn-end exempt with a 5 s cross-session guard, `Retry-After` backoff, 4.3.
- Auto-resume latency and heartbeats (codex 6, cursor 10, claude 10): typed
  `QuotaResetError` as the prerequisite, 20 s heartbeat, default path is
  ending the turn, 4.7.
- Publish on the error return and on the permission-resume path (claude 1,
  2): `PublishProviderUsage` from both, 4.3.
- `UnlimitedModels` under `--remote` (claude 3): `ModelUnlimited` stamped by
  the server, 4.1, 4.5.
- Turns with nothing to show (claude 4): `SkipUsagePublish`, default on with
  opt-out, 4.3.
- Sticky flag and credential writes outside `ReplaceConfig` (claude 5):
  fingerprint-keyed, `DropProviderUsage` from the auth handlers, 4.3.
- Console BDD leaking fetches between scenarios (claude 11):
  `WaitProviderUsageIdle`, 4.3 and 5.
- `pct_used` scale (cursor 8, claude 12): 0-100 from the hub's own formula,
  4.2.
- Window label from the payload (cursor 3), `bypass` (cursor 4), remote
  provider row name (cursor 6), events hub auth (cursor 7), empty first
  failure (cursor 9), cooldown row (cursor 11), wallet runes (cursor 12): 4.2,
  3, 4.4, 4.5.
- File name (claude 13): `provider_usage.go`.

## 9. Addressed concerns (round 3)

- `ModelUnlimited` in a provider-wide snapshot (codex 1, cursor 1, 2, 5, 10,
  claude 1): removed; clients compare the selector suffix, 4.1, 4.2, 4.5.
- `Retry-After` data path (codex 2): on the fetcher's error, 4.2.
- Turn finalization, subagent turns, `RunPlan`, early returns, LIFO defer
  (codex 3, claude 2, 3): publish from `finish` with `MarkTurnRan`, 4.3.
- BDD scenario wording (codex 4, claude 10): synced with the spec file, 5.
- Sanitising hub strings and the provider row name in the hint (codex 5,
  cursor 7): 4.5.
- Auto-resume: fail fast above the maximum (codex 6), threshold for
  `QuotaResetError` (claude 6), keepalive premise (claude 8): 4.7.
- Turn-end pacing (cursor 3) and the REST race (claude 4): one 15 s floor
  with deferred refreshes, slot taken inside `finish`, 4.3.
- Stale §7 text and §4.8 numbers (cursor 4, 6): fixed.
- `day` on unlimited keys (cursor 8), `day` reset derivation and timer
  arming (claude 5, coddy 4), null `retry_after_sec` (claude 7): 4.2, 4.3.
- `storeConfig` as the cache-clear site (claude 9): 4.3.
- Remote sticky `unauthorized` (coddy 5), two clocks (coddy 8): 4.4, 4.3.
- Type file already carries `omitempty` on the timestamps (claude 10): 4.1
  now matches the code.

## 10. Addressed concerns (round 4, codex)

- Cache lookups must not run `api_key_command` (`EffectiveAPIKey` may block for
  30 s): the fingerprint hashes the credential **as configured** (literal key,
  command text, env value, stored login file content) and never its output;
  the effective key is resolved only when a network fetch is due, inside the
  fetch's own context. `[rev4]` 4.2, 4.3.
- Clearing must invalidate asynchronous work: every cache entry carries a
  generation; `DropProviderUsage` and `storeConfig` bump it, stop the deferred
  timer, cancel the in-flight context, and a result with a stale generation
  is discarded instead of stored or broadcast. `ShutdownProviderUsage` stops
  timers and joins in-flight work for teardown; `WaitProviderUsageIdle` joins
  in-flight fetches. Tests cover logout and config replacement during an
  in-flight fetch and a deferred refresh. `[rev4]` 4.3.
- No session-global "ran" flag: the marker lives on the admission's own turn
  context (`session.MarkTurnRan(ctx)`), so a concurrent admission that loses
  the lock cannot reset another turn's state; `finish` is idempotent.
  `[rev4]` 4.3.
- `finish` reserves the refresh slot before `clearActive()` publishes
  `turn_ended`, so a client that pulls REST on that event joins the fetch.
  `[rev4]` 4.3.
- A deferred refresh is explicit on the wire: `RefreshPending` and
  `RefreshInSec` on the update (server-relative), and the remote console
  schedules one cancellable follow-up from them, cancelled on session switch
  and shutdown. `[rev4]` 4.1, 4.4.
- Auto-resume budget: the remaining capacity of the wrapper is
  `(RetryMax - attempt) * RetryMaxDelay`, computed per attempt; the boundary
  is pinned by a test. `[rev4]` 4.7.

## 11. Addressed concerns (code review rounds)

Codex (six iterations of the plugin's code phase), Cursor, coddy and a fresh
Claude reviewer reviewed the implementation of layers 1 and 2. What changed
against sections 4.3 and 4.4 above, which describe the plan before the code:

- The fetch context is cancel-only; the fetcher bounds its HTTP read to 5 s
  itself and the credential helper keeps its own budget under that context
  (`ProviderConfig.EffectiveAPIKeyContextErr`), reporting a helper cut short
  as `unavailable`, never as a rejected key.
- Every read that finds a fetch in flight joins it, cache reads included, so
  a read timed on a deferred refresh returns the result of that refresh; a
  read whose account changed under it (a logout, a rotated key, a config
  swap) answers an error instead of a superseded snapshot.
- Sessions that a deferred refresh owes are a list, merged into the fetch's
  waiters when it fires; the result reaches each of them through the manager
  sender and the observers. The session-ready trigger is one of those
  waiters rather than a bounded read, so a slow helper still delivers.
- The remote console arms no follow-up timer: the console's own timer reads
  the cache when the server says a refresh was deferred, for every backend;
  the remote client's own pulls (ready, after a turn) run on a goroutine, are
  refused after `Close`, never recreate a forgotten session, and remember an
  unsupported provider for five minutes.
- A 401 drops the numbers read with the key and the footer shows the sign-in
  hint first; a 403 replaces the blockers with the account block; a
  `Retry-After` backoff is capped at five minutes and a refresh inside it is
  deferred to its end; a config save keeps the pacing state.
- The footer keeps one snapshot per provider row and survives a theme
  switch; the passed-reset follow-up competes with the other deadlines.
- The turn stream's `provider_usage` event is reserved (nothing emits it
  today); REST answers carry `detail` next to a non-kind `error`.
- A deferred refresh resolves the account again when it fires: a settings
  save that swapped the credential while the timer ran sends the fetch into
  a fresh entry (the old numbers never describe the new key, and switching
  back never serves the other account from the cache), and the sessions
  the refresh owes move with it. A fetch that a save cancelled counts as an
  attempt, since the request may have reached the hub: its replacement
  waits for the floor and the hub's pause instead of doubling the read.

## rev5 - model gates (10.09.26)

A gate that covers part of the catalogue instead of the chat class had no place
in this snapshot. The hub's `decision` answers for chat as a whole, so an
account whose Kimi budget for the period was spent still reported
`can_request: true` - correctly, because every other model on the same key kept
answering. Coddy read that as a healthy account and showed it, while each
request to the selected model came back `429` with a reset a month out. On
10.09.26 an operator granted the user limit resets over that (they clear the
session and week counters and never touched this budget), the user spent them,
and the afternoon went into looking for a fault that did not exist.

The hub now sends `blocked_models[]` next to `decision`: `{model, blocker,
resets_at, reset_in_sec}`, empty when nothing is gated, and the field is always
present so its absence means an older deployment rather than "nothing blocked".
`can_request` deliberately stays green - flipping it would stop an agent that
has no business stopping.

Coddy carries the list through as `BlockedModels` and matches it against the
selector suffix, the way `UnlimitedModels` is matched:

- the console footer names the model and when it comes back ("kimi-k2.6 blocked
  (resets Oct 9)") instead of the account's windows, and `/usage` says the same
  in its head. Naming the model matters: bare "limit reached" reads as the whole
  key being out, and the operator stops working rather than switching models;
- the transcript notice fires once per model and reset time;
- the composer summary returns `kind: "blocked"` for that model only, so the SPA
  banner appears with its existing wording while every other model stays metered.

An entry without a model name is dropped on mapping (it names nothing a client
could match), and an unparsable `resets_at` is dropped rather than passed
through as a bogus deadline.
