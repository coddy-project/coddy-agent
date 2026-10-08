# Plan: shared models of a remote Coddy, phase 2 (capabilities from the listing, account usage, liveness of a peer)

Status: design record, written before the code; its four open dilemmas are now **decided** with the model-check engine (section 7;
reports `p2-d1-config-source.md`, `p2-d2-usage-cache.md`, `p2-d3-vanished-peer.md` and `p2-d4-busy-notice.md` in
`docs/plans/remote-model-provider-models/`) and every section below states the decided design. Branch `feat/remote-model-provider`, phase 1
committed (eight commits, `git log upstream/main..HEAD`). Parent record: `docs/plans/remote-model-provider.md` (sections 4.2, 4.3, 5, 5a, 6a, 6b); the
six `@phase2` scenarios are in `features/remote_model_provider.feature`; the capability decision is variant D of
`docs/plans/remote-model-provider-models/m6-capabilities.md` (section 9 there). Every reference below was read in the worktree
`coddy-agent-wt-remote-model` on 2026-10-07; the kernel facts of 5.1 were measured on Linux 7.0 (two experiments), the other
kernel and third-party statements are marked **[unverified]**. A verdict of a model is a statement about that model, not about the code;
what no model decides is kept in the residual risks of 7.6 and in section 9.

## 1. Goal and non-goals

**Goal.** Four things, each phase 1 left open on purpose:

- **A. Capabilities from the listing.** The reasoning levels, the default level, `off` and multimodal of a `coddy` row come from the
  remote's listing (the cache of `internal/session/context_window.go` already holds them), and a key written in the local row still
  wins, key by key. No refresh ever writes a key.
- **B. Account usage through the hop.** `GET /coddy/llm/models/{alias}/usage` answers the allowlist projection of 4.2 (today a 404),
  and the local Coddy shows the remote account's usage on the web UI, the console and the HTTP API like a local provider's.
- **C. Liveness of a vanished peer.** A client (or a relay) that disappears without a FIN or a RST while only heartbeats are written
  frees its slot within 45 s of its disappearance, on the leg the node sees, on a direct listener and on a tunnel; a client that vanishes
  behind a relay frees it as well, because the relay applies the same per-call option to its client connection (5.2).
- **D. Executable.** The six `@phase2` scenarios run (the `~@phase2` exclusion goes), the unit tests of the 5a rows that belong here
  exist, the documentation and the generated artefacts follow.

**Non-goals** (phase 3 or later, or declined here): per-client scopes, mTLS, an application-level probe that would cross an intermediary acknowledging for the peer (a TLS terminator or an HTTP proxy in front of a
direct listener, 5.2), relay discovery; a user timeout on macOS, Windows and the BSDs (a stub, 5.2); a push event
that tells the web UI a listing changed (a changed listing reaches a surface at its next read of the model list); a switch that lets a
lender hide its usage from borrowers (the lender's `providers[].usage_limits_panel: false` already turns it off for everybody); a batch
usage route for all aliases; a `protocol` bump (everything here is additive on protocol 1: the usage body ignores unknown fields on the
client, the server never removes one); the Codex and Devin cache affinity of 4.6.

## 2. Where the earlier plan is wrong or silent (verified against the code)

| # | Statement of the earlier plan | What the code says | Consequence |
|---|---|---|---|
| W1 | 4.2: the swarm tunnel "already pings at 30 s read-idle and 15 s timeout", so the tunnel leg is within the 45 s | True for the relay looking at a vanished node (`external/swarm/tunnel.go:129-130`, the relay is the h2 client). The node is the h2 server with no ping (`internal/swarm/tunnel.go:156-165`, `IdleTimeout: 0`), and its watchdog (`TunnelIdleTimeout` 150 s, `internal/swarm/tunnel.go:28-31,148-149`) counts the node's own writes as activity (`activityConn`, `:239-275`, "a connection this node is actively writing to is alive by definition") | A relay that vanishes while a call streams heartbeats is detected by nothing but the kernel's retransmission timeout, exactly the hole of the direct leg. The 45 s holds on no leg today. C.2 adds a node-side health check |
| W2 | 4.2: on the direct listener "only TCP's own retransmission timeout and keepalive apply" | Keepalive does not run on a connection with unacknowledged data, and a heartbeat every 15 s keeps data in flight. Measured (5.1): keepalive 2 s / 1 s / 3 probes did not fire, the connection ended at the retransmission limit (109.6 s with `tcp_retries2=8`) | Keepalive is no fallback; only a user timeout (or a probe that is not TCP's) bounds it |
| W3 | 4.3: "a `coddy` case in `fetchProviderUsage`" | There are two: the Go `Manager.fetchProviderUsage` (`internal/session/provider_usage_sources.go:37`, a switch on the provider type) and the TS `fetchProviderUsage` (`external/ui/src/ui/chat/providerUsage.ts:502`, a URL by provider name). The whole usage chain is keyed by provider name: the cache (`provider_usage.go:327`, `m.usage.entries[name]`), the route `GET /coddy/providers/{name}/usage`, the console footer map (`external/cli/footer.go:135-143`), the SPA's unsupported marks and `summarizeUsage` (`providerUsage.ts:264-272`). A `coddy` row's usage is per alias (a remote can share a codex row and an openai row through one local provider row) | A per-alias subject through the whole chain: B.4. The plan did not see it |
| W4 | 4.3 / 4.1b: the busy countdown ends with "the answer a row without usage gives" | `busyWaitNotice.clear` sends `Unsupported: true` (`internal/agent/coddy_provider.go:148-163`). Once a `coddy` row has usage, the SPA (`useProviderUsage.ts:162-175`) and the console (`external/cli/usage.go:641-660`) take that as "drop this row's usage", wiping the real snapshot; a clear that re-sends the cached snapshot loses to the busy update on `fetchedAt` order (`providerUsage.ts:486-496`); and a rule that lets a non-busy update supersede a busy one lets an old REST answer replace a newer snapshot (`p2-d4-busy-notice.md`) | The countdown is surface state of its own, ended by an explicit end update, never by a usage answer: B.6 (decided) |
| W5 | 5: "about 25 call sites in 14 files"; "the three multimodal reads" | The four methods have 30 call sites in 15 files outside `internal/config/reasoning.go` (`external/cli/app.go`, `external/gateway/telegram/resume.go`, `external/httpserver/{reasoning_levels_http,server_direct,server,server_metadata,shared_models_http}.go` (1, 2, 2, 1, 6), `internal/agent/{scheduled_run,subagent}.go`, `internal/session/{commands,config_options,settings,state}.go` (1, 1, 7, 3), `internal/tools/{export,switch_model}.go`), one direct field read (`external/cli/app.go:464`, `entry.ReasoningDefault`), and four multimodal reads (the three named plus `external/httpserver/shared_models_http.go:108`, added by phase 1) | A.3 lists them; a guard test keeps the count honest |
| W6 | 4.3: `multimodal` and `allow_reasoning_off` "become tri-state" | The config encodes the whole struct (`internal/config/yaml_comments.go:117`) and the two fields have no `omitempty` (`models.go:31,45`), so every saved `config.yaml` carries `multimodal: false` and `allow_reasoning_off: false` on every row, a `coddy` row saved by phase 1's Settings included | After the type change those read as an explicit false that pins the row. Harmless for every other type, a trap for a phase-1 `coddy` row: A.1, A.6, section 9 |
| W7 | 4.3: the Settings picker only needs to copy nothing | Phase 1 already does that (`SettingsSection.tsx:88-112`). Not covered: `GET /coddy/config/reasoning-levels` runs id detection on the alias for `provider_type=coddy` (`external/httpserver/reasoning_levels_http.go:62-64`) and `ReasoningLevelsField` writes the detected list into the row on Fetch, which pins it | A.5 |
| W8 | 5a Transport: `features/provider_proxy.feature` "gains the request classes completion, list and usage" for the new type | The feature has no `coddy` row; phase 1 covers completion and list in `internal/llm/coddy_registration_test.go:115` (`TestCoddyRowsHonourTheirProxy`) | The usage class joins that test; the feature file stays as it is (deviation recorded) |
| W9 | 4.2: the usage projection excludes "every list of upstream model ids" | `mapCodexUsage` also emits windows `codex:<i>:<id>` labelled `"<feature name> · 5h"` (`provider_usage_sources.go:67-79`), a metered feature or model name | The projection keeps windows by id allowlist and normalises labels: B.1 |
| W10 | 4.2: the projection has one relative time, `retry_in_s` | Windows carry an absolute `resets_at` only; the client schedules from relative values (`providerUsage.ts:usageNextReadMs`) and must not trust the remote's clock | `windows[].reset_in_s` replaces the absolute `resets_at` of the earlier plan on the wire; it leaks nothing and is skew-safe (D2, P6: with the absolute time only, a 60 s clock skew makes a lift never show) |

## 3. Area A: capabilities from the listing

### 3.1 Current state

- `llm.ModelEntry` carries `Revision`, `Multimodal`, `ReasoningLevels`, `ReasoningDefault`, `AllowReasoningOff` (`internal/llm/model_list.go:19-40`);
  `listCoddyModels` fills them (`coddy_models.go:77-95`); the manager's cache keeps the whole record per provider row and API model id
  (`context_window.go:73-90,387-420`), reads it with `Manager.ProviderModelEntry` and refreshes it with `RefreshProviderModelEntry`
  (`:431`). `coddy` is in `providerListsContextWindows` (`:130-145`). The provider-level handling of a stale view (fallback of the level,
  dropping images) exists (`internal/llm/coddy.go:234-280`).
- Nothing reads the record for a capability. `config.ModelEntry.Multimodal` and `.AllowReasoningOff` are plain `bool` (`models.go:31,45`);
  the four `*Config` methods (`reasoning.go:66,99,109,128`) look at the local keys and at `detectReasoningLevels(apiModel)`, which on
  an alias is noise (an alias `gpt-5` would be detected as a minimal-tier model).

### 3.2 A.1 Tri-state keys

| Where | Change |
|---|---|
| `internal/config/models.go:31,45` | `Multimodal *bool \`yaml:"multimodal,omitempty"\``, `AllowReasoningOff *bool \`yaml:"allow_reasoning_off,omitempty"\``. Absent (nil) means "ask the listing" for a `coddy` row and false for every other type; a written `false` means no, whatever the listing says |
| `internal/config/jsondto.go:115-135` | the same two fields as `*bool` with `json:",omitempty"` (the direct conversion `ModelJSON(m)` at `:497` and `ModelEntry(m)` at `:735` needs identical field types); clone both with the existing `cloneBoolPtr` in `ConfigToJSONDTO` (`:497-503`) and `JSONDTOToConfig` (`:735-739`), as `Stream` is cloned |
| `internal/config/ui_schema.go:238,248` | stay `boolean` for the form; descriptions say what absence means; `allow_reasoning_off` keeps `default false` (the form draws an absent key as off); `jsondto_schema_test.go:146-151` stays valid |
| `internal/config/config.schema.json:107,126` | `"type": ["boolean", "null"]` (the precedent is `stream`, `:131`), `default` stays `false` for the non-coddy meaning; the descriptions (they are the `doc:` lines of `coddy -t` and the table of `docs/reference/config.md`) say: absent is false, except for a model of a provider of type `coddy`, where the remote's listing decides; a written value wins. Widening a type is safe to publish with `make site-schema` at once |
| `internal/config/capabilities.go` (new) | exported `config.BoolPtr(v bool) *bool` (the test sweep needs it in five packages) and `func (c *Config) ModelMultimodal(ent *ModelEntry) bool` (nil-safe) |

`omitempty` on a pointer drops only nil, so an explicit `false` is written and survives `GET /coddy/config` and the settings save
(`restoreUntouched`, `yaml_comments.go:258`, works on YAML nodes and does not care about the Go type; S0 adds the round-trip test for the two keys, as it exists for `stream`). Tests that mutate the keys
(`Models[0].Multimodal = true`, `Multimodal: true` literals: about 20 sites in 13 test files of `external/gateway/telegram`,
`external/httpserver`, `internal/agent`, `internal/config`, `internal/session`; the compiler finds all of them) become `config.BoolPtr(true)`.

### 3.3 A.2 The resolver

`internal/config/listing.go` (new, untagged, no import of `internal/llm`):

```go
// ListedModel is what a model-listing cache knows about one model of one provider row.
type ListedModel struct {
	Multimodal        bool
	ReasoningLevels   []string // without "off"
	ReasoningDefault  string
	AllowReasoningOff bool
}
// ListingSource is the manager's cache seen from the configuration. ListedModel finds the provider row in
// the cfg it is given (cfg.FindProvider, then the cache key of that row) and in no other configuration, and
// answers from the cache as it is at the moment of the call.
type ListingSource interface {
	ListedModel(cfg *Config, providerName, apiModel string) (ListedModel, bool)
}
```

The five reads go through `func (c *Config) listedFor(ent *ModelEntry) (ListedModel, bool)`, which is false unless the row's provider is of
type `coddy`, a source is attached to the lineage of `c` (3.5) and the source's cache holds the model (no source, no entry yet, a failed read and a
model the listing does not carry are all "not known"). It calls the source with its own receiver, so a configuration an agent or a request
still holds after a reload is answered for its own row (a source that took the row from the manager's current configuration answered another
row after a repoint, in every mechanism: model `p2-d1-config-source`), and it reads live on every call: nothing resolved is kept in a
`Config`, a turn or an agent. The one frozen read is the coddy provider's per-turn snapshot, which a refusal replaces
(`coddy.go` `setView`); `coddyProviderInput` derives `in.ReasoningEffort` from the same record it reads for `ExpectedRevision`
(the rule of `fallbackReasoningEffort`), so level and revision of one request come from one record. Resolution, per key (for a provider type other than `coddy` every cell is today's behaviour, byte for byte):

| Key | Local key written | Local key absent, listing knows the model | Local key absent, no listing |
|---|---|---|---|
| levels (`ReasoningLevelsFor`, `ReasoningChoicesFor`) | `reasoning_levels` (an explicit `[]` hides the selector) | the listing's levels, no remap (the remote already applied its own, and `coddy` is not `codex`) | none; never detection from the alias |
| default (`DefaultReasoningLevelFor`) | the local `reasoning_default` when it is one of the resolved levels | the listing's default when it is one of the resolved levels | empty |
| `off` (`ReasoningOffOffered`) | the pointer | the listing's `allow_reasoning_off` | false |
| multimodal (`ModelMultimodal`) | the pointer | the listing's `multimodal` | false |

`ReasoningOffOffered` keeps its rule that a model without levels has nothing to switch. `ReasoningLevelsForProviderType(ent, "coddy")`
(the settings form's Fetch) returns the local explicit list or none, never a detection. The context window is unchanged (local
`max_context_tokens`, then the listing, then 128000).

### 3.4 A.3 The readers

One method per fact, and nothing else reads the field: `ModelMultimodal` and the five existing methods. The sweep, with the files that change
only their call (no new logic):

| Fact | Sites |
|---|---|
| multimodal | `external/httpserver/server.go:456` (the `/v1/models` row), `external/httpserver/server_metadata.go:192` (`configuredModelMultimodal`, four callers: `server.go:675,788,1230`, `queue_http.go:132`), `internal/agent/tool_images.go:28`, `external/httpserver/shared_models_http.go:108` |
| levels, default, off | the 30 sites of W5, plus `external/cli/app.go:464`, which reads `entry.ReasoningDefault` for the footer and becomes `DefaultReasoningLevelFor` only when that key is empty (a non-empty local default is shown as written, as today) |

A guard test, `internal/config/capability_readers_guard_test.go`, scans the production `.go` files of the repository (the pattern of
`internal/llm/proxy_guard_test.go`) for `\.Multimodal\b`, `\.AllowReasoningOff\b`, `\.ReasoningDefault\b`, `ResolvedReasoningLevels(` and
`DefaultReasoningLevel(` outside an explicit list of files whose type is not `config.ModelEntry` (`internal/llm/{coddy,coddy_models,model_list}.go`,
`internal/remote/{rest,handler}.go`, the wire row use in `shared_models_http.go`) and `internal/config` itself, so a new reader fails the test.

### 3.5 A.4 Reaching the cache from a `*Config` (decided: model `p2-d1-config-source`)

The manager serves many `*Config` values and builds a new one on every reload (`NewManager` `manager.go:167`, `storeConfig` `:466-489`; loaders: `config.LoadWithPaths` in `configapi.go:282`, `mcp/manage.go:349`, `skills_mgmt.go:546`, `manager.go:499`, `watch.go:105`, and, privately for the agent, `internal/agent/react.go:425` and `resume_permission.go:399`, which keep `a.cfg = next`); `Config` is read without locks after publication. The source therefore rides on what every loader already hands on, `config.Paths` (`paths.go:68`):

- `Paths` gets an unexported `listing *ListingCell` (an `atomic.Pointer` to the source; a pointer, so `Paths` stays a copyable value and nothing copies a lock). `WithListing() Paths` returns `p` with a fresh cell when it has none; `AttachListing(src)` stores the source (the last attach wins, never a panic); `AttachListingIfEmpty(src) bool` attaches only into an empty cell.
- Every loader allocates the cell when the `Paths` it is handed has none (`readConfigFile` in `config.go`, `recovery.go:114`, `check.go:281`); `JSONDTOToConfig`, `ParseConfigJSONPreservingSecrets` and `yaml_comments.go:697` pass their `Paths` on. Everything loaded through `LoadWithPaths(c.Paths)` shares the cell of `c` and is bound the moment it exists: the manager's reloads, the settings save, the skills reload, the watcher and the agent's private reloads need no line of their own.
- `NewManager` attaches the manager before `m.cfgAt.Store(cfg)` (allocating a cell first when `cfg.Paths` has none: the only plain write, before publication); `storeConfig` calls `AttachListingIfEmpty(m)` on `next` before `m.cfgAt.Store(next)`, so a config no loader made (a test's `ReplaceConfig(&config.Config{...})`) is bound before it is published and a cell another manager attached is never taken over. The bind is one pointer store; the reader's `cfg` is what the source receives (3.3); the resolver does not memoise.
- A second manager built on one config object (four tests in `internal/session`) takes the lineage over: a test that keeps reading through the older manager builds its own config. A guard test (`listing_guard_test.go`) requires every production caller of `config.LoadWithPaths(` to take its `Paths` from a live `*Config` or to be on an allowlist (`config.Resolve` at start-up, `cmd/coddy/serve_password.go`, the tools' `toolConfigPaths`).
- Not chosen (7.1): R1 a binding on every config, R2 a process global, R3 an explicit handle, R4 an overlay config.

### 3.6 A.5 Settings and the picker

- A `coddy` row's `multimodal` and `allow_reasoning_off` are drawn by a three-state control (`Remote`, `Yes`, `No`; `Remote` removes the key,
  the way `ReasoningLevelsField` removes `reasoning_levels` with `onChange(undefined)`), a new `TriStateField.tsx` chosen in the `models`
  `fieldOverride` of `SettingsSection.tsx` when the row's provider type is `coddy` (`providerTypeFor`, `ReasoningLevelsField` uses the same lookup);
  every other row keeps the Switch. A row whose key is written shows its value and a hint that the remote's listing is ignored for it.
- `ReasoningLevelsField` for a `coddy` row hides Fetch, says "the remote's listing decides" while the key is absent and shows the listing's levels read-only (the
  fetch `ContextWindowField` already makes, `useAutoProviderModels`); `useProviderModels.ts:8` learns `multimodal`, `reasoning_levels`,
  `reasoning_default`, `allow_reasoning_off` of a listed model (the route already serves them, `ModelEntry` JSON), and `ProviderModelList.tsx:183` shows a small
  "images" and levels badge for a `coddy` provider.
- `GET /coddy/config/reasoning-levels` answers `levels: []`, `detected: false` for `provider_type=coddy` without an explicit list.
- `USAGE_PANEL_PROVIDER_TYPES` (`SettingsSection.tsx:77`) gains `coddy`, and the `usage_limits_panel` descriptions (`ui_schema.go`, `config.schema.json:67-73`) list it.
- Both dictionaries (`en.ts`, `ru.ts`) get every new key in the same change (`messagesParity.test.ts`; names proposed: `settings.tristate.remote`, `.yes`, `.no`, `.hint`, `settings.reasoning.fromRemote`, `settings.providerModels.images`, `usage.remoteAccount`); the Russian wording rules apply (`агентный`, `субагент`, `worktree`).

### 3.7 A.6 `coddy -t` and `--dry-run`

`coddy -t` is offline and cannot compare with a remote; the phase-1 residue (`multimodal: false` written next to a `coddy` row) is invisible to it by
design. `--dry-run` has the listing: `internal/dryrun/providers.go:128` (`coddyModels`) already receives `[]llm.ModelEntry`; a new file
`internal/dryrun/coddy_keys.go` adds `coddyKeyFindings(m config.ModelEntry, row llm.ModelEntry) []finding`, one **warning** per local key that
differs: `max_context_tokens` (> 0 and not equal), `multimodal`, `allow_reasoning_off`, `reasoning_default` (not equal when written), `reasoning_levels`
(set difference both ways; a level the remote lacks is named as refused with `invalid_option`, a level the remote has and the row omits as unused).
Each message names the key, both values, the consequence and the fix ("delete the key to follow the remote"); it is located with `config.Locator`
at `models[<selector>].<key>` and falls back to the row. An alias the remote does not list stays the phase-1 error.

### 3.8 A.7 Edge cases

1. The listing has never answered (remote down at start): a `coddy` row has no levels and is not multimodal until it does; turn admission waits at most
   `ContextWindowWait` (`manager.go:1327`), `GET /v1/models` the same (`server.go:423`), and `readSettingsWindows` (`settings.go:171-184`) starts the read,
   without waiting, at every settings snapshot.
2. A local key wider than the remote: the remote answers `invalid_option` for as long as the key stays, no refresh fixes it (m6); `--dry-run` says so.
3. The alias is withdrawn: the record is gone, the capabilities vanish, completions answer 404, `--dry-run` errors.
4. `reasoning_levels: []` on a `coddy` row is the explicit opt-out and wins over the listing; an explicit `false` pins "no".
5. A persisted reasoning selection of a `coddy` row loaded before the listing: `EffectiveReasoning` (`state.go:1015-1041`) falls back to the default without rewriting the
   stored selection, so it returns when the listing lands.
6. A changed listing reaches the web UI at the next `GET /v1/models` (page load, config swap, model switch); there is no push event in phase 2 (non-goal).
7. Concurrent refresh and read: the cache copies records out (`copyModelEntry`), the resolver copies the levels again, no caller holds the cache's memory.

### 3.9 A.8 Tests

- `internal/config`: the table of 3.3 as a Go table test, each row for provider types `coddy`, `openai`, `codex`, `neuraldeep`, with a fake `ListingSource` (listing present, absent, narrowed,
  withdrawn); `ReasoningLevelsForProviderType("coddy")`; the codex remap untouched; YAML/JSON round trip: key absent stays absent through `MarshalConfigYAMLForEdit`, an explicit `false` stays,
  a legacy file with `multimodal: false` loads as explicit false, `ConfigToJSONDTO`/`JSONDTOToConfig` clone independence, `UpsertConfig`/UCI `models[model=X]/multimodal` set and clear,
  schema accepts `null`; the capability-reader guard test; the lineage (S1): a config loaded from `c.Paths` shares `c`'s cell, a `Paths` with no cell yields "not known" for the five methods, `WithListing` / `AttachListingIfEmpty` semantics, and the guard test over the production callers of `config.LoadWithPaths(`.
- `internal/session`: the manager as `ListingSource` (levels, default, off, multimodal after a listing lands; unknown model; the key by `api_base` change); `NewManager` attaches before the first read; a config loaded through `LoadWithPaths(c.Paths)` or `JSONDTOToConfig(j, c.Paths)` answers from the listing; `ReplaceConfig` with a hand-built config binds it; a second manager on the same config object takes the lineage over; a config of another lineage is never answered from this manager's cache; `SetContextWindowLister` is the stand-in.
- `external/httpserver` (a new test file, S1): `GET /v1/models` for a `coddy` row (levels with and without `off`, multimodal, local key wins, listing narrowed after a refresh), `/v1/chat/completions` keeps an image part for a row
  the listing marks multimodal and drops it otherwise, `reasoning-levels` route for `provider_type=coddy`, the remote's own `sharedRow` unchanged for lender rows.
- `internal/agent` (new test files, S1): `modelReadsImages` and the `read` image refusal for a `coddy` row, and the agent's two private reloads (`react.go:425`, `resume_permission.go:399`: `config_commit`, then `ReasoningChoicesFor` of `a.cfg`) answering from the listing; `internal/tools`: `switch_model` lists the listing's levels (it is built from `cfg` alone and reads the lineage's cell). The one-record rule of 3.3 is tested by S3: a refresh between the reads of the level and of the revision of one request cannot produce a refusal at a matching revision.
- `internal/dryrun`: every difference of 3.7, nothing for equal values, no warning for an absent key.
- SPA: `TriStateField`, the `models` override, the Fetch handling, the badges, `sharedModels.ts` helpers, the parity test.

## 4. Area B: account usage through the hop

### 4.0 Current state

- The route is reserved and answers 404 for every alias (`external/httpserver/shared_models_http.go:166-176`, OpenAPI `openapi_shared_models.go:195-210`); the gate already admits the LLM-only token on it (`isSharedLLMPattern`, `:40-48`, `auth.go:205`).
- The manager owns one usage cache and schedule keyed by provider name (`internal/session/provider_usage.go`: TTL 20 s, floor 15 s, backoff capped at 5 min, sticky rejection, generation counters), fed by a switch on the provider type
  (`provider_usage_sources.go:37`: `neuraldeep`, `codex`, `devin`); a type without a source answers `Unsupported`, and `providerUsageSource` (`:195`) gates every publisher. It reaches surfaces as `acp.ProviderUsageUpdate`
  (`internal/acp/types.go:544-613`, which carries provider, type, plan, key name, wallet, upstream model-id lists and blocked-model entries), through the manager's sender, the observers and the events stream, and over `GET /coddy/providers/{name}/usage`
  (`providers_usage_http.go:36-80`).
- A `coddy` row has no source. The only `provider_usage` update it ever sends is the busy countdown of phase 1 (`coddy_provider.go:113-165`).

### 4.1 B.1 The route and the projection (remote side)

`llmUsageGet` (`external/httpserver/shared_models_http.go:169`, today a 404 for every alias) becomes the handler below; the code goes to a new
`external/httpserver/shared_usage.go` (the projection, a pure function, unit-testable) and the registration stays as it is (`:54-58`); the gate already lets the LLM-only token through
(`isSharedLLMPattern`, `auth.go:205`).

1. The gate's snapshot (`authSnapshot`), `anonymousSharedRefused` -> `403 kind: auth`, exactly as the listing.
2. `cfg.FindSharedModel(alias)`; nil (unknown, private, a selector) -> `404` `sharedInvalid("unknown_model", ...)`, the text of the completions route, naming no provider.
3. The row's provider; `s.mgr == nil`, or `Manager.ProviderUsage` answering `Unsupported` (no source, or `usage_limits_panel: false` on the lender's row) -> `200 {"supported": false}`.
4. `u, err := s.mgr.ProviderUsage(ctx, provider.Name, false)`. Never `refresh=true`: a holder of a shared-model token cannot make the lender read its upstream account more
   often than the manager's own pacing allows (TTL 20 s, floor 15 s per provider row, `provider_usage.go:34-40`), however many tokens, aliases and clients ask. The route is served from the manager's per-account cache, so any number of clients, aliases and lender-side reads together make at most one upstream read per `providerUsageFloor`, and a client-driven one only once the TTL has passed (model `p2-d2-usage-cache`, P1 and P1b: a route that forced a refresh violates P1b).
5. `projectSharedUsage(u *acp.ProviderUsageUpdate, readErr error, ent *config.ModelEntry) llm.WireUsage` below.

The projection is an allowlist built field by field, never a copy-and-delete of `acp.ProviderUsageUpdate`:

| Field | Rule |
|---|---|
| `supported` | `true` once step 3 passed |
| `account_wide` | `true`: every source reads an account (or key) level meter; the field is there so a later per-model meter can say `false` |
| `stale` | `u.Stale`, or `u.Error != ""`, or the read failed (`err != nil`); a failed read with nothing cached answers `{supported: true, stale: true, windows: []}` with HTTP 200 and no error kind, so the lender's credential state is not exposed |
| `windows[]` | only windows whose `id` matches `^(session\|week\|day\|acu\|window-[0-9]+)(-secondary)?$` (this drops the `codex:<i>:...` feature windows, W9); `label` kept when it matches `^([0-9]{1,4}[smhd]\|week\|day\|ACU)$`, else the id; `used_percent` clamped to 0..100; `reset_in_s` (a pointer: absent when the source window has no `resets_at`, 0 once passed) = `ResetInSec` as `ProviderUsage` delivers it, the remaining time relative to the moment the remote answers (age-corrected); `exhausted`. **No absolute reset time is on the wire**: the two hosts' clocks differ and a client that schedules from the lender's `resets_at` shows a lift late by the skew, or never (D2). The counters `used`, `limit`, `remaining` are dropped (they reveal the plan's size) |
| `blocked`, `blockers[]`, `retry_in_s` | the account block as given, blocker ids through the set `session_exhausted, week_exhausted, rpm_exhausted, session_cooldown, abuse_cooldown, daily_capacity_exhausted, key_blocked, key_cap_blocked, wallet_empty, user_blocked, quota_exhausted` (an unknown id becomes `other`). A `BlockedModels` entry whose model equals the row's API model id (trimmed, case-insensitive, as `modelBlocked` in `providerUsage.ts:107-120`) adds `blocked: true`, the blocker `model_blocked` and that entry's retry time (the larger of the two); entries for every other model are dropped. **A block that concerns a model is named under the alias only** because nothing but the alias's own gate survives |
| unlimited | when the account is `Unlimited` or the row's API model id is in `UnlimitedModels`, `windows` is empty: the alias spends no metered quota, and the id list itself never leaves |
| never | provider name and type, plan, key name, `observedAt`, `fetchedAt`, `resets_at` (no absolute time at all), the rate window (rpm figures), `cooldownSec`, the wallet, `retryAt`, `blockedModels` of other models, `unlimitedModels`, `refreshPending`, `resuming`, `disabled`, every model id |
| account identity | none: no account tag, no next-read hint, no revision of the usage; two aliases of one account answer with equal numbers and nothing else relates them (D2: U2 and U3 refused) |

Answer headers: `Content-Type: application/json`, `Cache-Control: no-store`. The values are the account's: every holder of a shared-model token learns how much quota is left (the plan accepts it, section 6 there).
Edge: the manager resolves the provider by the name in the gate's snapshot against its own active configuration; a request that straddles a reload that retypes that row sees
the new row's answer, once.

DTO, in untagged `internal/llm/coddy_wire.go` beside `WireListing`, so both ends use one definition: `WireUsage{Supported, AccountWide, Stale, Windows []WireUsageWindow, Blocked, Blockers, RetryInS}`,
`WireUsageWindow{ID, Label, UsedPercent, ResetInS, Exhausted}`, `func CoddyUsagePath(alias string) string` (`/coddy/llm/models/<url.PathEscape(alias)>/usage`). A reflection test fixes the JSON key set to
the allowlist, so neither `resets_at`, a tag nor a hint can slip in.

### 4.2 B.2 The client (`internal/llm/coddy_usage.go`, new)

`CoddyUsageForProvider(ctx, in ProviderInput, alias string) (*WireUsage, error)`: GET through `HTTPClientForProviderProxy(in.ProxyURL)` (the proxy guard scans the package), 15 s bound,
`Authorization: Bearer`, body limit 1 MiB, JSON decode that ignores unknown fields. Failures become `*ProviderUsageError{Status, Kind, RetryAfter}` so the manager's existing failure path
(`provider_usage.go:526-571`: stale windows kept, sticky rejection, backoff from `Retry-After` capped at 5 min) applies unchanged. Non-2xx go through `classifyCoddyAnswer` (`coddy_errors.go:379`) first:
401 and 403 -> `unauthorized`; a 404 with `code: not_found` (a phase-1 remote's reserved route) -> `&WireUsage{Supported: false}` and no error (a JSON `not_found` body only: an HTML or other non-JSON 404, `CoddyKindUnsupported`, is `unavailable` and takes the ordinary backoff); 404
`unknown_model` and other `invalid` -> `invalid` (which drops the entry's numbers, 4.3); 408/429/502/503/504, transport errors -> `unavailable` with `Retry-After`. `CoddyUsageFingerprint(provider config.ProviderConfig, alias string) string` hashes type, normalised
`api_base`, proxy, the credential as configured (key, command text, env value) and the alias, the way `NeuralDeepUsageFingerprint` does (`neuraldeep_usage.go:323`), never running `api_key_command`.

### 4.3 B.3 Mapping and the manager

`providerUsageSource` (`provider_usage.go:195`) gains `coddy`; `fetchProviderUsage` (`provider_usage_sources.go:37`) a `coddy` case calling `CoddyUsageForProvider` and `mapCoddyUsage(w, provider, alias, fetchedAt)`:
`Provider` = the row, `ProviderType: "coddy"`, `Model` = the alias (4.4), `FetchedAt` = the receipt time on this host, windows mapped one to one with `ResetInSec = reset_in_s` and `ResetsAt = fetchedAt + reset_in_s`
(the local clock: nothing absolute is read from the wire; both absent for a window without `reset_in_s`), `Blocked`, `Blockers`, `RetryInSec = retry_in_s`, `RetryAt = fetchedAt + retry_in_s`, `Stale`; no plan, key name,
wallet, rate; `!Supported` -> `Unsupported: true`.

- **Unsupported memory.** A `Supported: false` answer, and only that (the 200 body or the JSON `code: not_found` 404 of a phase-1 remote; an HTML or other 404 is `unavailable` and takes the ordinary backoff), is remembered per subject for `coddyUsageUnsupportedTTL` = 60 s, not the five minutes of `usageUnsupportedTTL`: reads inside it answer it without a GET, a manual refresh (`refresh=1`) ignores it, and the turn-end publishers (`publishProviderUsageAsync` `:698`, `publishProviderUsageOnReady` `:753`) stay silent for it.
- **An alias that no longer exists.** `unknown_model` is `invalid` and drops the entry's numbers (`usageRecordFailureLocked` treats `ProviderUsageInvalid` of a `coddy` row like a rejected key: an empty snapshot with the error); a transport failure keeps its stale numbers as today.
- **Follow-up read.** The remote answers from its own cache (TTL 20 s), so a read at a turn's end can return a snapshot older than the turn. When the first fetch of a `coddy` subject after a turn end (`publishProviderUsageAsync`) completes, the manager arms one deferred refresh at `fetchedAt + providerUsageTTL` (`usageArmPendingLocked`, announced as `refreshPending` like any deferred refresh) unless one is pending; a deferred fire, a backoff end or a follow-up arms none. Without it a quiet surface shows the numbers from before the last turn indefinitely; with it the turn's spend shows within `providerUsageTTL + providerUsageFloor` (D2, P4b). It lives in the manager only: it never goes through the agent and never touches the countdown of 4.6.

### 4.4 B.4 The per-alias subject (decided: D2, U1)

`acp.ProviderUsageUpdate` (`internal/acp/types.go:544-613`) gains `Model string \`json:"model,omitempty"\``: the alias, empty for every other type. The manager's cache key becomes a subject:
`type usageSubject struct{ provider, model string }`, key `provider` when `model == ""` and `provider + "/" + model` otherwise (a provider name has no slash, `SplitModelRef`), so every non-coddy entry keeps
its key. These change from `name string` to the subject: `providerUsageRead` (`:235`), `usageEntryLocked` (`:327`), `usageDeferLocked`, `usageDeferredFire` (`:394`), `usageArmPendingLocked`,
`usageAwait`, `usageStartFetchLocked`, `usageRecordFailureLocked`, `usageProviderForSession` (`:676`, returns the row and the alias), `publishProviderUsageAsync`, `publishProviderUsageOnReady`; `DropProviderUsage(provider)`
(`:586`) drops every entry of the provider; `pauseProviderUsage` (`:613`) is per entry already. `providerUsageEntry` keeps the subject so a deferred fire can rebuild it.

Public API without signature churn: `Manager.ProviderUsage(ctx, name, refresh)` and `ProviderUsageForSession` (the `backend` interface of `external/cli/backend.go:59` and `remote.Handler` implement them) accept a provider
name **or a model selector** `provider/alias`; for a `coddy` row the selector selects the alias, for every other type the alias part is ignored, so callers can always pass the selector. The REST route
`GET /coddy/providers/{name}/usage` (`providers_usage_http.go:36-80`) takes `?model=<alias>`, required for a `coddy` row (400 `{"ok":false,"error":"invalid","detail":"..."}` without it), ignored for the others; the answers carry
`usage.model` and, for the unsupported answer, `model`.

The remote's cache is per provider (the account); the local one is per subject `(provider, alias)` (**decided, D2: U1**): nothing the remote says about accounts keys an entry, so nothing in the projection tells a client which aliases share one. An alias in use costs at most one GET per `providerUsageFloor` through the relay, all served from the remote's single per-account cache, so a client with N aliases in use makes at most N x ceil(W / 15 s) requests in a window W (N = 2, W = 30 s: 4, measured) and an alias nobody reads costs nothing. A repointed alias shows the old account's numbers for at most one `providerUsageTTL` (20 s) after the repoint; numbers marked stale by a failed read stay until a read succeeds (except after `unknown_model`, 4.3). A snapshot shown is built from a remote upstream read at most 40 s earlier. Entries are discarded by the fingerprint rule (a rotated key, a renamed alias) and by `DropProviderUsage(provider)`.

### 4.5 B.5 Surfaces

- **SPA.** `ProviderUsage` gains `model?`; `fetchProviderUsage(provider, refresh, fetchImpl, model?)` appends `?model=` (always sent: the server ignores it for other types); the in-flight sequence of `useProviderUsage.ts` is keyed by provider, or by provider and model for a provider an answer reported as `providerType: "coddy"` (a learned set, so other providers' request counts do not change); the unsupported marks stay keyed by provider and none is recorded for such a provider (the manager's 60 s memory per alias is the only one, so memories do not add up);
  `summarizeUsage` (`providerUsage.ts:264`), `usageIsNewer` (`:486`), `usageBannerKey` (`:400`) and `applyPushed` (`useProviderUsage.ts:162`) match `u.model` when it is set; `BLOCKER_KIND` gets `model_blocked: "window"`;
  `usageProviderTitle` appends the alias for a `coddy` row and `UsageSection.tsx` adds a one-line note ("the remote's account, shared with everyone who borrows from it", key `usage.remoteAccount`); window countdowns are computed from `resetInSec` as received.
  The busy countdown is separate (4.6): `useProviderUsage` keeps a `busy` slot and a tombstone beside `usage`, `applyPushed` routes an update whose `blockers` contain `remote_busy` to it and every other update to the snapshot path, and `UsageBanner` takes the slot as its own prop.
- **Console and remote console.** `external/cli/usage.go` (`usageActive` `:635`, `refreshUsage` `:836`, `showUsage` `:858`) passes `a.modelID` where it passes the provider and matches `Model`; `footer.go:135-160` keys `usages` by
  provider plus model when `Model` is set; `usageBrand` (`:124`) names a `coddy` row by its provider. `applyProviderUsage` (`usage.go:641-690`) routes the same way: `a.remoteBusy` (already apart from `footer.usages`) takes the countdown branch with a tombstone, and the `Unsupported` branch no longer ends a wait.
  `internal/remote/usage.go:79-120` splits the selector, adds `?model=`, and keeps its unsupported marks per provider, recording none for a provider an answer said is `coddy`.
- **Events.** `coddy.provider_usage` frames (`providers_usage_http.go:79`) carry the update as it is, so `model` rides along; OpenAPI and the ACP page describe it.

### 4.6 B.6 The busy countdown next to a real snapshot (decided: D4, model `p2-d4-busy-notice`)

The countdown is state of its own on every surface, ended by an explicit end update on the existing fields, with a strict tombstone and two local nets; no new field and no cache read by the agent (7.4).

- **Wire.** An update whose `blockers` contain `remote_busy` belongs to the countdown of the call that sent it; every other update is a snapshot. `Resuming: true` with `Blocked: true`, `RetryAt`, `RetryInSec` and `FetchedAt` = now renews it (as today). The same update with `Resuming` absent, `Blocked: false`, no retry fields and `FetchedAt` = the clock at sending is its **end**: no usage data, no `Unsupported`. Both set `Provider`, `ProviderType: "coddy"` and `Model` = the alias (`llmTransport.model`, the subject of 4.4), so one alias's countdown never shows or ends another's. `FetchedAt` is an RFC 3339 UTC string compared as a string.
- **Agent.** `busyWaitNotice.clear` sends the end and nothing else, at its three sites (the admission report, the first chunk, after `Stream` returns), so an error and a cancel end the countdown too. It reads no cache; `Manager.CachedProviderUsage` does not exist. The stamp is the clock, never a cache's `fetchedAt`.
- **Surfaces (SPA hook, console, remote console).** The snapshot path is B.5's: no countdown update touches it, and no snapshot or `Unsupported` read answer touches the countdown. The countdown slot holds the last accepted countdown update and a tombstone (the largest `FetchedAt` of an end or of a dropped countdown): a countdown update is accepted iff its `FetchedAt` is greater than the tombstone and not older than the slot's; an end is accepted iff it is not older than the slot's, and then empties the slot and raises the tombstone (an end wins a tie; a countdown replayed after the end, by relay replay, mirror or reconnect, is ignored). Nets: the slot is dropped, tombstone raised, when the viewed session's turn ends (SPA `turnEpoch` of `useProviderUsage`; console `turnDone`) and when `RetryInSec` after receipt plus 2 s has passed on the surface's own clock (never against the server's absolute `RetryAt`). A change of subject drops slot and tombstone.

### 4.7 B.7 Edge cases

An alias whose remote provider has no usage source: `supported: false`, remembered for 60 s per alias (4.3), nothing published. A remote that predates phase 2: the reserved 404 means the same. A relay: the route rides the mount (`mountedPrefixes` carries `/coddy/`),
a node unreachable is a relay 502 -> `unavailable`; usage reads take no stream slot and are not counted by `max_streams`. The provider row's `usage_limits_panel: false` on the local side stops the reads (`EffectiveUsageLimitsPanel`, `providers.go:70`).
A rotated key or a renamed alias is a new fingerprint and a fresh entry (`usageEntryLocked`). `DropProviderUsage` after a sign-in does not apply to `coddy` (no sign-in) but the config swap pauses in-flight reads as for any row.

### 4.8 B.8 Tests

Server (`external/httpserver/shared_usage_test.go`): the projection against the NeuralDeep fixture of `provider_usage_test.go:106-130` (key name, wallet, option models, blocked models) with a needle scan of the whole body for every excluded value;
codex feature windows dropped; label normalisation; unlimited model; a blocked model folded in under the alias, another model's block dropped; `reset_in_s` age-corrected, absent for a window with no reset clock, and no `resets_at` anywhere in the body; unknown, private and selector aliases all 404 naming nothing; unsupported provider and
`usage_limits_panel: false` -> `{"supported": false}`; failed read -> stale; shared token 200, other token and anonymous per the gate; a second request inside the TTL does not read upstream (counter on the stand); reflection test on `WireUsage`.
Client (`internal/llm/coddy_usage_test.go`): every status of 4.2, an HTML 200, an oversize body, `Retry-After`, the proxy (extend `TestCoddyRowsHonourTheirProxy`), fingerprint stability and change. Manager (`internal/session`): per-alias entries independent, one GET per alias inside the TTL,
the floor and backoff, sticky unauthorized, `Drop` per provider, the subject keys of non-coddy rows unchanged (the existing usage tests stay green untouched), the 60 s unsupported memory (JSON answers only, an HTML 404 is `unavailable`, a manual refresh bypasses it; a fake clock), turn-end publishers silent for it, `invalid` drops the numbers while a transport failure keeps them stale, the follow-up read fires once per turn end and does not chain, race-clean.
Surfaces: the route's `?model=` (400 without, ignored for neuraldeep), SPA/console/remote-handler tests for the model-aware matching, the learned set, `model_blocked`, no unsupported mark for a `coddy` provider. Countdown (4.6): a snapshot during a wait leaves the banner, the end leaves the snapshot, an older end, a replayed countdown and a tied end behave as the tombstone says, turn end and expiry drop the slot, an `Unsupported` answer leaves the countdown alone; the console tests that clear with `Unsupported` are rewritten to the end update.

## 5. Area C: liveness of a vanished peer

### 5.1 C.1 Facts

- Code: the direct listener is `httpx.NewServer` (`internal/httpx/server.go:33`) with `ListenAndServe` (`external/httpserver/serve_http.go:134,138`): plain HTTP/1.1, Go's default accepted-socket keepalive (15 s), no write timeout; the response has a 60 s per-write deadline and a heartbeat at most 15 s apart (`shared_stream.go:20-22`, the per-write deadline in `writeLocked` `:177-199`),
  neither of which fails when the peer's kernel buffers still take bytes. A cut connection cancels the call: `callCtx` derives from `r.Context()` (`shared_models_http.go:607`), the slot is released by `defer slot.release()` (`:270`).
- Experiment (a throwaway Go program, not committed, run inside `unshare -Urn` with an iptables DROP of the server's segments, one heartbeat every 3 s, the peer vanishing 1 s after the call started; the opt-in test of 5.4 reproduces it): with `TCP_USER_TIMEOUT` set per call to 10 s the handler's context was cancelled
  **11.7 s** after the peer vanished (H + U, not U); with keepalive tightened to 2 s / 1 s / 3 and `tcp_retries2=8` it was cancelled after **109.6 s**, the retransmission limit, so keepalive never ran (W2). `tcp_retries2` is 15 on this host, which the kernel documentation puts at about 15 minutes **[unverified here: only the value 8 was run]**.
  On Linux 7.0 the retransmit timer is clamped to the remaining user timeout (the abort did not wait for the next backed-off retransmission); an older kernel **[unverified]** may wait for the first retransmission instant at or after U.
- Second experiment (`p2-d3-vanished-peer.md`, section 6; `unshare -Urn`, loopback `iptables` DROP for X seconds, a live peer that only reads, Linux 7.0.0-34, RTO about 0.2 s): the call dies **U after the first unacknowledged byte plus 0.4 s** (U = 10 s: written at 4.02 s, cut at 14.4 s; U = 30 s: written at 15.01 s, cut at 45.4 s), and a **live** peer is cut by an outage shorter than U: 8 s against U = 10 s, 27 s against U = 30 s (7 s and 25 s survived), the last retransmission instant before U, not U (5.3).
- Tunnel: relay -> node pings 30 s / 15 s (relay side); node -> relay has none (W1).

### 5.2 C.2 Design (decided: model `p2-d3-vanished-peer`)

One promise on both legs: **the slot of a peer that vanished is freed at most B = `sharedLivenessBound` = 45 s after it**, on the leg the node sees, with no effect on any other route and no cut of a peer whose link is silent for less than the last TCP retransmission instant before the timeout (5.3). One mechanism per leg: no listener-wide option, no `TCP_INFO` polling, no application probe, no user timeout on the tunnel socket (7.3).

**Direct leg, Linux (Android builds the same file): `TCP_USER_TIMEOUT` per call, U = B - H.** H + U = 15 s + 30 s = **45 s** from the peer's disappearance: the first heartbeat written after it is the first unacknowledged byte (at most H later, plus the timer's lateness and under a second of kernel granularity) and the kernel aborts U after that (clamped retransmit timer, measured on Linux 7.0).

- `internal/platform/usertimeout_linux.go` (`//go:build linux`) and `usertimeout_other.go` (`//go:build !linux`): `func SetTCPUserTimeout(c net.Conn, d time.Duration) error` (`*net.TCPConn` through `SyscallConn().Control`, `unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, ms)`;
  `d == 0` restores the system default), `TCPUserTimeout(c) (time.Duration, error)` for tests, and `ErrUserTimeoutUnsupported` returned by the stub. `golang.org/x/sys` is already a direct dependency (`go.mod:25`); `syscall.TCP_USER_TIMEOUT` is not defined on amd64.
  Per-OS files follow the repository's pattern (`filelock_unix.go` / `filelock_other.go` / `filelock_windows.go`); the Windows and Android halves are checked by `make check-windows` / `make check-android`, not by `make lint`.
- `internal/httpx`: `NewServer` sets `ConnContext` (the connection under an unexported key; the relay's `*tls.Conn`, `external/swarm/serve_swarm.go:82-86`, is unwrapped with `NetConn()`), exported `httpx.ConnContext` for servers built another way, `httpx.UserTimeoutFor(r, d) (restore func(), err error)`: finds the `*net.TCPConn` of the request, sets `d`, returns a function that sets 0. A request without a TCP connection (the h2 tunnel, a test server without the hook) or one that is not HTTP/1.x (an h2 stream shares its connection, so a per-call option would reach other streams) gets `ErrUserTimeoutUnsupported` and a no-op restore: that call goes on unprobed on this leg (the tunnel check below covers the tunnel). `httpx.LivenessBound` (45 s) and `httpx.LivenessHeartbeat` (15 s) are the constants the handler and the relay mount share.
- **Per call, not per listener**: a listener-wide option aborts a browser's SSE (`/v1/responses`, the events stream) on any outage between the tolerance and the kernel's limit, where today it survives for minutes. `runSharedCall` (`shared_models_http.go:595`) sets the option **after the request body has been read** (the upload is bounded by P = 30 s, which must stay <= B) and before `stream.start()` (`:632`) with `U = B - H`; `sharedTimings()` (`:62`) returns B too (a test seam: a short test shortens B, H and P together) and clamps H to B / 3, so U >= 2B / 3. U = B - H grows as H shrinks; there is no 1 s minimum (a U of 1 s cuts a live peer on a one-tick outage, and H >= B cannot be bounded). The restore is a `defer` registered right after the set and runs on **every** exit (final frame, error frame, cancel, failed write): a connection kept alive after an `error` frame must not carry the option into the next request. The slot keeps its one CAS-guarded `release()`; the probe adds no handler logic and no second release.
- On expiry the kernel resets the socket, the connection reader errors, `r.Context()` ends, `provider.Stream` is cancelled, `runSharedCall` returns through `case r.Context().Err() != nil` (`:655`) logged as `client_gone`, and the slot is released.

**Tunnel leg, every platform: a node-side HTTP/2 health check, and nothing else.** `internal/swarm/tunnel.go:156` builds `http2.Server{MaxConcurrentStreams, IdleTimeout: 0}`; it gains `ReadIdleTimeout: TunnelPingAfter` = **15 s** and `PingTimeout: TunnelPingTimeout` = **30 s** (fields exist in the pinned x/net, `config.go:55-56`). The node pings when no frame arrived for 15 s and closes a relay that does not answer within 30 s: **at most 45 s after the last frame the relay sent**, whatever the heartbeat is. The split is not the relay's own 30 s / 15 s: the bound is the same, but a ping behind unacknowledged heartbeats is answered only when a retransmission gets through, and the longer timeout doubles the outage a live relay survives. A closed connection ends `ServeConn`, every in-flight request context is cancelled, the slot is released; `activityConn`'s 150 s watchdog stays. No user timeout on the node's dialled socket: Linux only, and behind an egress proxy (`netx` CONNECT) it times the node-to-proxy leg, which the proxy acknowledges, while the ping travels end to end. Constants are exported so a test can scale them.

**Through a relay.** A relay that dials the node reaches the direct listener over a pooled HTTP/1.1 connection (the per-call option applies to it); a node that dialled out is the tunnel case; either way the node's peer is the relay. **A client that vanishes behind a relay is invisible to the node** (the relay acknowledges and answers everything) and would hold its slot until the relay's own request ends. The relay's mount (`external/swarm/mount.go:162`, `httputil.ReverseProxy`, `FlushInterval: -1`) therefore sets the same per-call option on the client's connection for `POST /swarm/nodes/{node}/coddy/llm/completions` (that route only), `U = B - H` from `httpx`, with a deferred restore; the relay's request context ends at the abort, the proxy cancels the node request (a reset stream on a tunnel, a closed connection on a direct dial) and the node's slot is freed within B of the client's disappearance too.

**Other legs and platforms.** macOS, Windows, the BSDs: `SetTCPUserTimeout` is a stub and the direct leg keeps the operating system's retransmission timeout (minutes), as the page says (`TCP_RXT_CONNDROPTIME` and `TCP_MAXRT` are **[unverified]**, a follow-up behind the same signature). A peer behind an intermediary that acknowledges for it (a TLS terminator such as nginx or Cloudflare, an HTTP proxy) is not covered on any platform: its own limits apply. A kernel that does not clamp the retransmit timer aborts at the first retransmission instant at or after U: the bound is then H + that instant, up to about H + 2U **[unverified which kernels]**.

**Constants.** B = H + U = R + TP = 45 s (H = 15 s, U = 30 s, R = `TunnelPingAfter` = 15 s, TP = `TunnelPingTimeout` = 30 s); H <= B / 3; P = 30 s <= B; m5's `I >= 2H` unchanged; the relay's own 30 s / 15 s pings stay. Both sums are pinned by tests.

### 5.3 C.3 Edge cases

**A live peer whose link goes silent.** The user timeout counts from the first unacknowledged byte; the kernel retransmits at backed-off instants (RTO, 3 RTO, 7 RTO, ...) and aborts at the first timer after U without another try, so a live peer survives an outage only if a retransmission lands after the link is back and **before U**: it survives the **last retransmission instant before U**, between about U/2 and U (RTO 0.2 s: 25.4 s of 30 s; RTO 1 s: 15 s; RTO 2 s: 14 s; measured on Linux 7.0: U = 30 s, 25 s survived, 27 s cut at 45.5 s). The client sees a cut stream, retried only before output (the existing `emitted` contract); the documentation says "a silent link of more than about 15 to 25 s", not "30 s". The tunnel has the same shape with TP: the node closes a live relay whose answer to a ping needs a retransmission later than 30 s, and the tunnel's calls go with it.
The heartbeat gap is part of the bound: B = H + U (+ the timer's lateness), `sharedHeartbeat` is clamped to B / 3, a unit test pins H + U = 45 s and R + TP = 45 s. A peer that vanishes during the upload is bounded by P (30 s <= B: the option is set after the body); one that vanishes after the `final` frame costs nothing. The option is restored on every exit, so a kept-alive connection starts its next request with the system default. `Expect: 100-continue` is unaffected; IPv6 is the same socket option.

### 5.4 C.4 Tests

`internal/platform` (Linux file): set, read back, restore on a loopback pair; non-TCP conn error; the stub on the other builds (`GOOS=windows go vet`). `internal/httpx`: `NewServer` stores the connection, a `*tls.Conn` is unwrapped, `UserTimeoutFor` sets and restores, no-op without a connection and for an HTTP/2 request. `external/httpserver`: a server on a custom listener whose accepted connection returns `ETIMEDOUT` from `Read` while the call streams heartbeats: the provider's context is cancelled and `sharedLimit.inUse` is 0 within 100 ms, logged `client_gone`; the sockopt is 0 while the body is read, `B - heartbeat` during the call and 0 after it, on the `final` and the `error`-frame path alike; the next request on the connection starts with the default; `sharedTimings` returns B and clamps H to B / 3; `sharedHeartbeat == httpx.LivenessHeartbeat`.
`internal/swarm` + `external/swarm` (`-tags http,swarm`): `TunnelPingAfter + TunnelPingTimeout` equals the bound; a relay side that swallows frames and never answers pings is closed within the scaled bound and the slot of a held call is freed; a relay that answers late but within `TunnelPingTimeout` is not cut; a live relay is never cut while the call is silent longer than `TunnelPingAfter`. `mount.go`: a client that vanishes behind the relay (its connection returns `ETIMEDOUT` while heartbeats are forwarded) cancels the node request within the scaled bound; the option is `B - H` during the call, 0 after it, and no other route sets it. An **opt-in** kernel test (`CODDY_TEST_NETNS=1`, re-exec under `unshare -Urn`, `iptables` DROP, skipped when unavailable) reproduces the experiments of 5.1 against the real handler (U = 10 s: an outage of 5 s is survived, one of 9.5 s cuts the call at U after its first unacknowledged byte); it closes the "about 15 minutes" open item of 6a and stays out of `make test`.

## 6. Area D: executable specification, tests, documentation

### 6.1 The six scenarios

`runRemoteModelSuite` (`bdd_remote_model_test.go:1394-1429`) loses `Tags: "~@phase2"` and its comment; the `@phase2` lines (`features/remote_model_provider.feature:30,37,107,118,134,148`) and the header sentence (`:14`) are removed in the same commit.
Steps are written in the harness's own style (clauses of `remoteClauses`, state in `remoteModelState`):

| Scenario | New steps and stand parts |
|---|---|
| levels offered (`:31`) | `the local coddy adds the model "..." without reasoning levels` -> `localAddsModel`; `GET /v1/models of the local coddy reports "..." with the reasoning levels "low, high"` -> `modelsReportLevels` (exact order of `reasoning_levels` of that row) |
| images offered (`:38`) | `... without declaring it multimodal` -> `localAddsModel`; `GET /v1/models ... reports "..." as multimodal` -> `modelsReportMultimodal`; `the local coddy lets a user attach an image to a message sent to "..."` -> POST the local `/v1/chat/completions` with a data-URL image to `remote/coder` and assert the remote's stub received one image part with the same bytes (the intake keeps parts only for a model `ModelMultimodal` accepts, `server.go:788`) |
| narrowed levels (`:108`) | `the remote operator narrows "coder" to the reasoning level "low"` -> `mutateRemote` sets `ReasoningLevels` to `["low"]`; `the local coddy falls back to the level "low" and the provider received "low"` -> the first completion asked for `high`, the second for `low` (request bodies in `remote.capt`), `providerReceivedLevel("low")`. `streamsAtLevel` (`:480`) now passes its "offered" check only through the resolver |
| key not overwritten (`:119`) | `the local row "..." sets max_context_tokens 64000` -> `mutateLocal`, and the local stand writes `<home>/config.yaml` with `config.MarshalConfigYAML` before it starts and keeps its bytes; `the local coddy refreshes its view of the remote listing` -> `ForgetContextWindows("remote")`, `AwaitContextWindows` (10 s), `WaitContextWindowsIdle`, and the remote saw a second listing request; `the local configuration file still says 64000` -> the file is byte-identical and loads with the row at 64000 |
| `off` (`:135`) | remote clause `that allows reasoning off` -> `remoteSpec.allowOff`, row `AllowReasoningOff: config.BoolPtr(true)`; steps exist (`streamsAtLevel`, `providerReceivedLevel("off")`); `sharedCallOptions` passes `off` through (`shared_models_http.go:529-535`) |
| usage (`:149`) | remote clause `whose provider reports (\d+) percent of its quota used` -> `remoteSpec.usagePercent`: the remote's provider row becomes type `neuraldeep` with an explicit key and `api_base` at a stub of the hub's `/limits` (fixture of `provider_usage_test.go`, session limit 15000, key name and wallet distinctive), closed in `remoteStand.close`; `the local coddy reads the usage of the model "..."` -> GET the local `/coddy/providers/remote/usage?model=coder`; `it reports 62 percent of the quota used` (`windows[session].usedPercent`, `providerType coddy`, `model coder`); `the remote marks the reading as account-wide` (the captured remote answer has `account_wide: true`); `the document never mentions ...` (needle scan of the captured remote body and of the local answer for the upstream model id, the provider name, the key name and the wallet figures) |

Two new relay scenarios in `features/remote_model_provider_relay.feature`, "The account usage of a shared model is read through a relay that dials the node" and "... through the reverse tunnel of a node that dialled out", reusing the steps above; the relay `Given` parses `whose model ...` (`bdd_remote_model_relay_test.go:74`) and gets a sibling for `whose provider reports ...`.
`features/provider_proxy.feature` gets no `coddy` row (W8).

### 6.2 Unit tests that belong to this phase (5a rows)

Server: the usage projection rows, `a vanished peer frees the slot within the probe bound` (direct, tunnel and a client behind a relay), `a block names the alias only`. Dry run: the disagreement warnings. SPA: tri-state toggles, usage matching. Config: the tri-state round trip, the schema. Client: usage client and proxy. Detailed lists are in 3.9, 4.8, 5.4.

### 6.3 Documentation (same pull request)

`docs/features/shared-models.md`: the sentence "In this version the model's other capabilities are the local row's own keys" (`:134`) becomes the listing rules of 3.3 and the three-state control; the usage section replaces the Limitations bullet (`:243`) and gains the exposure statement; the liveness bullet (`:245`) becomes a section with the per-leg bounds, the platform table, what a live peer survives (5.3) and the list of what is not covered (5.2: macOS, Windows and the BSDs on the direct leg; a peer behind an intermediary that acknowledges for it; a kernel that does not clamp the timer; a node that dials a direct peer through a CONNECT proxy); the tunnel and direct claims of the swarm page (`docs/operate/swarm.md`) are corrected (W1) and say that the relay bounds its own clients.
`docs/reference/http-api.md` (`:210` the reserved row, `:420` the `not_found` code, `:441`), `external/httpserver/openapi_shared_models.go:195-210` and a `CoddyLLMUsage` schema, `external/httpserver/openapi.go:3602-3640` (`ProviderUsage.model`, the route's `model` parameter and 400), `docs/reference/acp-protocol.md:533-565` (`model`), `docs/getting-started/configuration.md` (`:684` types of `usage_limits_panel`, `:727` sources, `:752` "Account usage is not read for this type", the dry-run section), `docs/surfaces/console.md` (`/usage`, footer), `docs/operate/{security,serve}.md` (the usage exposure, the user timeout), `DESIGN.md` (Context popover usage section, the tri-state control), `AGENTS.md` (two table rows say "until the second phase"), `config.example.yaml`, `internal/skills/bundled/configure-coddy/SKILL.md`, a screenshot of the usage block of a `coddy` row and of the tri-state control under `docs/assets/` (Dark, 1280 wide, `<feature>-<state>-<theme>-<width>.png`, embedded on the page) per the documentation contract, the plan sections of section 10 here.
No subcommand, `serve` verb or flag is added, so the man page and the completions stay (`AGENTS.md`, Packaging). Every stale sentence is found with `git grep -n -i -e 'second phase' -e 'second step' -e 'reserved for' -e 'until the second' -e 'liveness probe' docs AGENTS.md DESIGN.md external internal`.

### 6.4 Generated artefacts

`make docs` (config tables from the schema, hub, llms files, assets inventory), `make docs-check`, `make site-schema` then `make site-schema-check` (a type widened to `boolean|null`, no key renamed or removed: safe to publish with the same pull request), `make site-docs-check`; the SPA's embedded assets by `make build TAGS="http ui"`.

## 7. Decided dilemmas

Each had more than one defensible answer and was decided with the model-check engine (`mcd 0.2.0`, explicit state, a discrete clock where time matters; every verdict quoted is `exhaustive`). A verdict is a statement about a hand-written model, not a proof about the code: the tests of the sections above are the conformance check. The reports in `docs/plans/remote-model-provider-models/` hold the runs and the decoded counterexamples.

### 7.1 D1. How a `*config.Config` reaches the manager's listing cache (`p2-d1-config-source.md`)

**Question.** The resolver of 3.3 needs the manager's cache; the 30 readers hold a `*Config` (`tools.SwitchModelTool(cfg)` nothing else), the manager serves many configs and builds one per reload, the agent keeps private ones.
**Options.** R1 a binding on every config (the agent re-binds); R2 a process-global source; R3 an explicit handle; R4 an overlay config carrying the listing in the keys; R5 a source on `config.Paths`, one atomic cell per lineage; R6 = R5 plus an attach-if-empty in `storeConfig`.
**Decision.** **R5 with the backstop (the model's R6)**: the row is resolved in the reader's own `cfg`, reads are live (3.3, 3.5); no line in the agent or the HTTP server.
**Evidence.** R1 fails on two paths of today's code: the agent's private reloads leave `a.cfg` unbound (`unbA`, `blind`, `liveA`; a level validated at admission is silently not sent, `lost`) and the skills reload stores the config in the server before the manager binds it (`skills_mgmt.go:546-556`: `unbS`, `blind`, even with the re-bind). R2, R3, R5, R6 have no reachable unbound read on any production path; R5 alone fails for a config no loader made (`HAND=1`) and the backstop closes it; R4 is refuted (`fnonop`: a save writes the listing into the operator's keys; `stfromref`: every refresh runs `storeConfig`). A source that takes the row from the manager's current config answers another row after a repoint in every mechanism (`SRCCFG=0`, `cross`); a second manager on one config object takes the lineage over in all but R3 (tests only). Live reads keep "refused at most once after a change" (CK = 1); frozen per turn without recovery gives CK = NCALL, frozen per config never sees the cache (`stalerd`), a split read of level and revision allows a refusal at a matching revision (`rflevel`): hence the one-record rule of 3.3.
**Rules kept** (each has a failing mutant): one cell per lineage; attach before publish in one store; the row from the reader's config; no memo; one record per request; last attach wins; the guard test. **Not decidable.** Race freedom of the cell and of every `Paths` consumer with a pointer field; conformance; the frozen `SwitchModelTool(cfg)` text (advisory).

### 7.2 D2. The usage cache per alias, polling cost and coalescing (`p2-d2-usage-cache.md`, `p2-d2-usage-cache-skew.pml`)

**Question.** Where is the remote's usage cached and refreshed per alias, and what does the projection tell a client about aliases that share an account?
**Options.** U1 one local entry per (provider, alias), each with its own GET; U2 an opaque account tag so aliases with equal tags share an entry; U3 a next-read hint. Also open: the reset time on the wire, a follow-up read after a turn end, the memory of `supported: false`.
**Decision.** **U1**, no tag, no hint; `windows[].reset_in_s` on the wire and `resets_at` off it; one follow-up read after the first fetch that follows a turn end; `supported: false` remembered 60 s per subject (4.1, 4.3, 4.4).
**Evidence.** Upstream reads: at most one per floor for any number of clients and aliases (P1), a client-driven one only after the TTL (P1b; a route that forced `refresh=true` violates it). Cost: one GET per floor per active alias (P2a), 4 per 30 s for two aliases (N = 3: 6 derived, run inconclusive), where U2 saves only 4 -> 3 and 6 -> 4 and U3 nothing. Safety (P3): U1 shows a repointed alias's old account for at most the TTL (tight); U2 is unbounded without a guard (decoded: another alias keeps the shared entry fresh) and with one bounded only by the revalidation period, which erases the saving; an unclamped U3 hint breaks it. Numbers a failed read keeps stale stay until a read succeeds, for every option (P3s), hence `invalid` drops them. Freshness (P4): what is shown is built from an upstream read at most 40 s earlier; plain U1 never shows a turn's own spend on a quiet surface (P4b: warm remote cache, nothing reads again), the follow-up shows it within TTL + floor. Memory (P5): at most M, M + P - 1 on a surface that reads every P. Skew (P6): with only `resets_at` a 60 s skew makes a lift never show; with `reset_in_s` the lag is 7 ticks for any skew.
**Constants.** `providerUsageTTL` 20 s, `providerUsageFloor` 15 s, backoff cap 5 min unchanged; `coddyUsageUnsupportedTTL` 60 s (new); follow-up delay = TTL; shown age <= 40 s; repoint exposure <= 20 s. **Not decidable.** Relay latency under load, the real skew distribution, the N = 3 cost for U1 and U3, conformance.

### 7.3 D3. Where and how a vanished peer is cut (`p2-d3-vanished-peer.md`)

**Question.** Which mechanism bounds the slot of a vanished peer on each leg, at which scope, composed with the heartbeat, the body deadline and the kept-alive connection.
**Options.** Direct: O1 per-call `TCP_USER_TIMEOUT`; O2 the option on every accepted socket; O3 a `TCP_INFO` poller; O4 an application probe the client answers; O5 nothing off Linux; O6 a small send buffer with a write deadline. Tunnel: T1 node-side h2 `ReadIdleTimeout` / `PingTimeout`; T2 a user timeout on the node's dialled socket.
**Decision.** Direct **O1**, U = B - H, set after the request body, restored by a `defer` on every exit; tunnel **T1 only**, R = 15 s, TP = 30 s; the relay mount applies O1 to its client connection on the completions route; O5 off Linux, documented (5.2).
**Evidence.** Slot freed within B of the vanish: O1 (B = H + U) and T1 (B = R + TP, independent of H) verified at three scales, tight (B - 1 violated); clamp and 45 s measured on Linux 7.0 (5.1). O2 violates "no effect on other routes" (an outage below the kernel's limit aborts a browser SSE, `c2cut`); O3 has O1's tolerance with a poller and no gain; O4 cuts every live client that does not answer when strict, never detects one when lenient; O6 rests on kernel buffer accounting; T2 is blind behind a CONNECT proxy and adds nothing to T1. Without a `defer` the next request on a kept-alive connection inherits U (`leak`), and a success-path restore fails after an `error` frame; the CAS-guarded `release()` stays the only release; P must stay <= B (PB = 8 > 7 violates). A client that vanishes behind a relay is invisible to the node's mechanisms (`late`) and bounded only when the relay applies O1 to it. **"No cut of a live peer whose outage is shorter than U" is contradicted**: a live peer survives to the last retransmission instant before U (about U/2 to U, 15 to 25 s of 30; kernel check: 25 s survived, 27 s cut), and 15 s / 30 s doubles the tolerance of 30 s / 15 s for the same bound (RTO 2 s: 30 s against 14 s).
**Constants.** B = H + U = R + TP = 45 s (H 15 s, U 30 s, R 15 s, TP 30 s); H <= B / 3; P = 30 s <= B; m5's `I >= 2H` unchanged. **Not decidable.** The tolerance on a real RTT-driven RTO; kernels older than the clamp; macOS, Windows, BSDs; SACK, TLP and RACK; relay forwarding delay; the h2 timer's tie at the deadline.

### 7.4 D4. The busy countdown and the real usage snapshot on one surface (`p2-d4-busy-notice.md`)

**Question.** How does the end of a `remote_busy` notice avoid wiping, or being shadowed by, the usage snapshot of the same row?
**Options.** N1 the end re-sends the cached snapshot (as it is, or re-stamped); N2 the end stays `Unsupported`; N3 the countdown is state of its own on the surface; N4 an explicit end plus a rule that a non-busy update supersedes a busy one whatever its `fetchedAt`.
**Decision.** **N3 + an explicit end on the existing fields + a strict stamped tombstone + two local nets** (4.6). No new field; the agent sends only the end and reads no cache; N1, N2 and N4's ordering exemption are rejected.
**Evidence.** Every one-slot surface violates "the latest snapshot is never lost" (S1) or "no flicker" (S3), and on the warm scale "an older snapshot never replaces a newer" (S2): N2 wipes (W4); N1 as is never clears the countdown (`fetchedAt` order, L1); N1 re-stamped misorders; N4's exemption lets a session-open answer replace a newer snapshot; a split surface with an `Unsupported` clear wipes, with a supersede rule it flickers. Split slots + end + tombstone + nets violate nothing at scales A, W, W2 (two waits) and T (ties); each part has a failing mutant: no nets, a lost end leaves the countdown forever (`LOSS`); no tombstone, a replayed notice revives it (`REPLAY`); a non-strict comparison fails on a tie of two RFC 3339 seconds; no clear on the error path loses promptness; an end stamped from the cache's `fetchedAt` is ignored.
**Constants.** Expiry = `RetryInSec` after receipt plus 2 s on the surface's clock. **Not decidable.** Wall-clock bounds and browser-server skew (the nets are receipt-relative), React batch order, several tabs on one subject, the `limit_wait.go` notices of a `coddy` row (no `remote_busy` blocker: they keep today's rule, a snapshot replaces them; only `Model` is added), conformance.

### 7.5 Consistency notes (what the decisions changed in each other's sections, and why)

1. **Stage graph.** D1 removes the agent re-bind: S1 no longer owns `internal/agent/{react,resume_permission}.go`, is not a sweep stage, and the model-check pre-step is gone. A directory has one owning stage at a time: S3 now follows S1 (both own files in `internal/session`), S5 follows S1 and S3 (`external/httpserver`), S9 follows S5 (8).
2. **D4 against D2.** D4 removes `Manager.CachedProviderUsage` and the agent's cache read; D2's follow-up read is armed inside the manager (`publishProviderUsageAsync`, `usageArmPendingLocked`), never goes through the agent and never touches the countdown slot; the end update carries no data, so the two paths share only `Model`. With a per-alias subject (D2) D4's two updates and the `limit_wait.go` update of a `coddy` row carry `Model`, so S3 owns `internal/agent/limit_wait.go` too. "No unsupported mark for a `coddy` row" (D2) and "an `Unsupported` answer leaves the countdown alone" (D4) are one rule from two sides.
3. **D3 against D2 in `external/httpserver` and `external/swarm`.** Disjoint files: S4 owns `external/swarm/mount.go` (completions route only; the usage route rides `mountedPrefixes` unchanged, takes no stream slot and no option); S5 owns `shared_models_http.go`, `shared_stream.go`, `shared_usage.go`, `providers_usage_http.go` and the OpenAPI files. The relay's listener is already `httpx.NewServer` (`serve_swarm.go:82`), so S4's `ConnContext` reaches it. The mount cannot import `external/httpserver`, so B and H live in `internal/httpx` (S4) and S5's `sharedLivenessBound` / `sharedHeartbeat` are held equal to them by a test.
4. **Statements corrected.** In the first draft: "U = B - H, minimum 1 s, so a shorter heartbeat shortens U" was inverted (U grows as H shrinks; the minimum is dropped, H is clamped to B / 3); "an outage shorter than U never cuts" is false (last retransmission instant before U); "the node's peer is the relay, so the client is covered" is false for a client behind a relay (the non-goal "relay-side probe of the relay's own clients" is reversed); "an egress proxy does not change this" holds for T1 only; the tunnel split became 15 s / 30 s. D2: `reset_in_s` and no `resets_at` on the wire; unsupported memory 60 s, an HTML 404 is `unavailable`. D4: the busy end is neither `Unsupported` nor a re-sent snapshot. D1: the first draft's "a config published before `storeConfig` binds it: none on the manager path" is true, but `skills_mgmt.go:546-556` stores a config in the HTTP server before the manager and the agent keeps its own second load (`react.go:425`, `resume_permission.go:399`): neither is the manager path.
5. **Added by this edit, not by a report.** (a) `httpx.UserTimeoutFor` refuses a request that is not HTTP/1.x: an h2 stream shares its connection, so a per-call option would reach other streams (it keeps D3's "no effect on other routes" true by construction; the model has no h2 client leg, 7.6). (b) `reset_in_s` is a pointer on the wire (`*int`, absent when the source window has no `resets_at`, 0 once passed) so "no reset clock" and "passed" stay distinguishable under `omitempty`. (c) D1's one-record rule (a line in `coddyProviderInput`) is S3's, because `coddy_provider.go` is S3's file. (d) B and H as `httpx.LivenessBound` / `LivenessHeartbeat` (note 3).

### 7.6 Residual risks (kept open on purpose)

- **D1.** Race freedom of the cell and of every `Paths` consumer with a pointer field (`make test-race`, `go vet`, a copy-by-value test of `Paths`); conformance of the code; the weight of `lost` and `blind` for users is not measured.
- **D2.** Numbers kept stale by a failed read stay until a read succeeds (only `unknown_model` drops them): a repointed alias can show another account's numbers, marked stale. A quiet surface hides a lender that enables its reader for longer than the memory (M + P - 1 given reads, unbounded without). The N = 3 cost for U1 is derived, not run; the reset-lag bound assumes the single follow-up of the SPA and console (`USAGE_FOLLOW_UP_MS`).
- **D3.** A live peer survives 15 to 25 s of silence, not 30 s, depending on the RTO. A kernel without the clamp gives H + the first retransmission instant at or after U (up to about H + 2U) **[unverified which kernels]**; macOS, Windows and the BSDs keep the retransmission limit (minutes). A peer behind an intermediary that acknowledges for it (TLS terminator, HTTP proxy) is uncovered on every platform (the application probe O4 is the phase-3 candidate), as is a node that dials a direct peer through a CONNECT proxy. **A client behind a relay whose own connection is HTTP/2 (a TLS relay and an h2 client) is not covered, because `UserTimeoutFor` refuses an h2 stream (5.2): not decided; it needs an h1-only relay listener for that route or a probe.** The opt-in kernel test needs Linux and root in a user namespace.
- **D4.** Several tabs on one subject are modelled as one surface; the `limit_wait.go` notices of a `coddy` row keep today's rule; the console orders snapshots by arrival (`footer.SetUsage`), so an older answer can overwrite a newer one with or without a wait (pre-existing, independent of D4; S8 may order by `fetchedAt`, not decided here); a client older than this change shows nothing for the end update (9).
- **Parent plan, 6a "Open".** The retransmission figure is measured on Linux 7.0 and closes there; "about 15 minutes" at `tcp_retries2 = 15` stays unverified until the opt-in kernel test runs.

## 8. Stages and file ownership

Stages own files, and a directory has one owning stage at a time (the second table). "After" lists what must have landed (its exported names are fixed in the sections cited). Each stage works test first and runs only its narrow tests (section 10). All four dilemmas are decided (7): no model-check step precedes any stage.

| Stage | Owns | After | Parallel with |
|---|---|---|---|
| **S0** tri-state and the one multimodal reader | `internal/config/{models,jsondto,ui_schema,capabilities}.go`, `config.schema.json`, `reasoning.go` (the off pointer), their tests; the four reads (`server.go:456`, `server_metadata.go:192`, `agent/tool_images.go:28`, `shared_models_http.go:108`) and the compile sweep of about 20 test literals in `external/gateway/telegram`, `external/httpserver`, `internal/agent`, `internal/session` | none | none: runs alone, behaviour unchanged for every type |
| **S1** resolver and lineage binding (A.2-A.4) | `internal/config/{listing,paths,config,recovery,check,reasoning,capability_readers_guard_test,listing_guard_test}.go` (the `ListingCell` and `Paths` methods of 3.5; the loaders allocate the cell), `internal/session/{capability_listing.go, manager.go (two call lines: NewManager and storeConfig)}`, `external/httpserver/reasoning_levels_http.go`, new test files in `internal/agent`, `internal/tools` and `external/httpserver` (3.9). Not the agent's reload files, not `skills_mgmt.go` | S0 | S2, S4, S6, S7, S8 |
| **S2** usage wire and client | `internal/llm/{coddy_wire.go (usage DTO: no `resets_at`, no tag, no hint), coddy_usage.go (404 `unknown_model` to `invalid`, JSON `not_found` to `Supported: false`, other 404 to `unavailable`), coddy_registration_test.go (proxy), coddy_usage_test.go, coddy_wire_test.go}` | none | S0, S1, S4, S6, S7, S8 |
| **S3** usage in the manager, the countdown's agent side (D2, D4) | `internal/acp/types.go` (`Model` only), `internal/session/{provider_usage.go,provider_usage_sources.go}` + tests (4.3: the 60 s memory, the follow-up with a fake clock, the drop on `invalid`), `internal/agent/{coddy_provider.go, limit_wait.go}` + tests (the end update of 4.6 on admission, error and cancel, `Model` on both notices and on a `coddy` row's limit-wait update; the one-record rule of 3.3) | S1, S2 | S4, S6, S7, S8 |
| **S4** liveness primitives (C), the relay's client leg (D3) | `internal/platform/usertimeout_{linux,other}.go` (+tests), `internal/httpx/*` (`ConnContext` unwraps `*tls.Conn`, refuses h2; `LivenessBound`, `LivenessHeartbeat`), `internal/swarm/tunnel.go` (`TunnelPingAfter` 15 s, `TunnelPingTimeout` 30 s, sum pinned), `external/swarm/mount.go` (completions route only), the tunnel and mount tests of `external/swarm` | none | S0-S3, S6-S8 |
| **S5** server (B.1, C call sites) | `external/httpserver/{shared_models_http.go, shared_usage.go, shared_stream.go, providers_usage_http.go, openapi.go, openapi_shared_models.go}` + tests (the projection with `reset_in_s` and no `resets_at`, its key-set reflection test; the option after the body read with the deferred restore on every exit; `sharedLivenessBound`, the B seam in `sharedTimings()`) | S0, S1, S2, S3, S4 | S6, S7, S8 |
| **S6** dry run (A.6) | `internal/dryrun/{providers.go,coddy_keys.go,coddy_provider_test.go}` | S0 | S1-S5, S7, S8 |
| **S7** web UI (A.5, B.5, the countdown) | `external/ui/src/ui/{chat/*Usage*,chat/providerUsage*,settings/{SettingsSection,TriStateField,ReasoningLevelsField,ProviderModelList,useProviderModels,sharedModels}*,i18n/messages/{en,ru}.ts}` + tests (`useProviderUsage`: the `busy` slot, tombstone, nets; no unsupported mark for a `coddy` provider) | contract of 4.4, 4.5 and the wire of 4.6 only | S0-S6, S8 |
| **S8** console and remote console (B.5, the countdown) | `external/cli/{usage,footer,updates,app}.go` (`app.go:464` included; the end branch and tombstone of `a.remoteBusy`) + tests, `internal/remote/usage.go` + test | S3's contract (names of 4.4, wire of 4.6) | S5-S7 |
| **S9** executable spec (D) | `features/remote_model_provider*.feature`, `external/httpserver/bdd_remote_model*_test.go` | S0-S6 | S7, S8 |
| **S10** documentation and generated files | `docs/**`, `AGENTS.md`, `DESIGN.md`, `config.example.yaml`, `internal/skills/bundled/configure-coddy/SKILL.md`, the plan of section 10, then `make docs`, `make site-schema` | S0-S9 | none |
| **S11** integration | nothing owned; the commands of section 10, fixes go back to the owning stage | all | none |

| Directory | Owners, in order |
|---|---|
| `internal/config` | S0, S1 |
| `internal/session` | S0 (test literals), S1 (`capability_listing.go`, `manager.go`), S3 (`provider_usage*.go`) |
| `internal/agent` | S0 (`tool_images.go`, test literals), S1 (new test files), S3 (`coddy_provider.go`, `limit_wait.go`) |
| `external/httpserver` | S0 (the four reads), S1 (`reasoning_levels_http.go`, a new test file), S5, S9 (`bdd_remote_model*_test.go`) |
| `internal/tools`, `external/gateway/telegram` | S1 (a new test file), S0 (test literals) |
| `internal/llm`, `internal/acp`, `internal/dryrun`, `external/ui`, `features/` | S2, S3, S6, S7, S9 |
| `internal/platform`, `internal/httpx`, `internal/swarm`, `external/swarm` | S4 |
| `external/cli`, `internal/remote` | S8 |

Stage graph (acyclic): S0 -> S1 -> S3 -> S5 -> S9 -> S10 -> S11; S2 -> S3; S4 -> S5; S0 -> S6 -> S9; S7 and S8 depend on contracts only.

### 8a. Names the stages agree on

| Name | Stage that creates it | Used by |
|---|---|---|
| `config.BoolPtr`, `(*Config).ModelMultimodal(ent) bool`, `ModelEntry.Multimodal`/`AllowReasoningOff` as `*bool` | S0 | every stage that builds or reads a row |
| `config.ListedModel`, `config.ListingSource`, `config.ListingCell`, `config.Paths.{WithListing, AttachListing, AttachListingIfEmpty}`, the unexported `(*Config).listedFor` | S1 | S1 only; `Manager.ListedModel(cfg, provider, apiModel)` implements the source |
| `llm.WireUsage`, `llm.WireUsageWindow` (`ResetInS`, no `resets_at`), `llm.CoddyUsagePath(alias)`, `llm.CoddyUsageForProvider`, `llm.CoddyUsageFingerprint` | S2 | S3 (client), S5 (server projection), S9 |
| `acp.ProviderUsageUpdate.Model`, the subject form of `ProviderUsage(ctx, name, refresh)`, `coddyUsageUnsupportedTTL` (60 s), the countdown family (blocker `remote_busy`) and its end update (4.6; no new field) | S3 | S5 (route `?model=`), S7, S8, the agent files of S3 |
| `platform.SetTCPUserTimeout`, `platform.TCPUserTimeout`, `platform.ErrUserTimeoutUnsupported` | S4 | `httpx` |
| `httpx.ConnContext`, `httpx.UserTimeoutFor(r, d) (restore func(), err error)`, `httpx.LivenessBound` (45 s), `httpx.LivenessHeartbeat` (15 s) | S4 | S4 (`external/swarm/mount.go`), S5 (`runSharedCall`, `sharedTimings()`), S9 (stand servers get `ConnContext`) |
| `swarm.TunnelPingAfter` (15 s), `swarm.TunnelPingTimeout` (30 s) (node side, exported for scaled tests, sum pinned to B) | S4 | S4 tests |
| `sharedLivenessBound` (45 s) and the B seam in `sharedTimings()` | S5 | S5 tests |
| HTTP: `GET /coddy/llm/models/{alias}/usage` body, `GET /coddy/providers/{name}/usage?model=`, `usage.model`; no new config key, no new flag | S5 | S7, S8, S10 |

## 9. Risks

- **Build tags and platforms.** `internal/platform/usertimeout_linux.go` builds on Android as well (GOOS=android satisfies `linux`) with cgo; `usertimeout_other.go` must compile for Windows, macOS, the BSDs. Run `make check-windows`, `make lint-windows`, `make check-android` (NDK), `go vet` for `GOOS=darwin`; do not trust `make lint`, which compiles the host only. `internal/httpx`, `internal/platform`, `internal/llm`, `internal/config`, `internal/acp` stay untagged and import nothing tagged; `external/httpserver` keeps `//go:build http`, the relay tests `http && swarm`.
- **`bool` to `*bool`.** About 20 test literals break at compile time (S0 alone). Every saved `config.yaml` already has `multimodal: false` and `allow_reasoning_off: false` on every row: they load as explicit false, which is today's behaviour for every non-coddy row; `GET /coddy/config` now shows them as `false` instead of omitting them (the Settings form draws the same). A `coddy` row saved by a phase-1 build with those two keys is pinned to "no": visible in the three-state control (selected `No`), warned by `--dry-run` when the remote lists true, fixed by choosing `Remote`; the documentation says so. UCI paths (`config_set models[model=X]/multimodal`), `restoreUntouched`, the schema validator and the generated config reference must be re-checked for pointer-to-bool (the precedent is `stream` and `usage_limits_panel`).
- **Other provider types byte for byte.** The resolver's `coddy` branch is guarded by the provider type before it reads any source; a table test compares the five methods for `openai`, `anthropic`, `neuraldeep`, `codex`, `devin` before and after; the usage subject's key for a non-coddy row equals the provider name (existing usage tests untouched and green); the SPA's unsupported marks stay per provider and none is recorded for a provider an answer said is `coddy`; no new request leaves for another type; the user timeout is set only inside `runSharedCall` and in the relay mount's completions route.
- **Concurrency.** New locks only in the manager's usage state (existing mutex, new key); the listing cell is one `atomic.Pointer`, no lock; `make test-race` over `internal/session`, `internal/httpx`, `internal/swarm`, `external/httpserver`, `external/swarm`; the opt-in kernel test is Linux and root-in-userns only.
- **Privacy.** Usage values are the lender's account's: every holder of a shared-model token reads them (the plan's accepted position). The needle scan of 4.8 is the guard against a leaked model id, plan, key name, wallet or feature name.
- **Kernel dependence.** The 45 s bound is measured on Linux 7.0, where the retransmit timer is clamped to the user timeout; elsewhere it is H + the first retransmission instant at or after U **[unverified which kernels]**. The outage a live peer survives is the last retransmission instant before U (about 15 to 25 s of 30), not U.
- **Wire.** `reset_in_s` replaces the absolute `resets_at` of the parent plan's allowlist (W10), and `model_blocked` and `other` are new blocker ids; both ends ignore what they do not know, protocol stays 1. A phase-2 client against a phase-1 remote reads `supported: false` from the reserved JSON 404. The busy end update (4.6) is a usage update without data and with `blocked: false`; a client older than this change shows nothing for it and keeps a stale countdown until its turn ends (the same as for any unknown update).

## 10. Verification

Narrow, per stage (tags as the code needs them):

```
go test ./internal/config -count=1
go test ./internal/llm -run 'Coddy|ProviderProxyGuard' -count=1
go test ./internal/session -run 'ProviderUsage|ModelEntry|Listing|Capabilit|ContextWindow' -count=1
go test ./internal/platform ./internal/httpx ./internal/dryrun ./internal/remote ./internal/agent ./internal/tools -count=1
go test -tags=http ./external/httpserver -run 'Shared|RemoteModel|Usage|Reasoning|Models' -count=1
go test -tags=http,swarm ./external/httpserver ./external/swarm ./internal/swarm -run 'RemoteModel|Tunnel|Relay|Mount|Liveness|UserTimeout' -count=1
go test -tags=cli ./external/cli -count=1
golangci-lint run ./internal/... && golangci-lint run --build-tags http,swarm,cli ./external/... ./internal/...
cd external/ui && npx vitest run src/ui/chat src/ui/settings src/ui/i18n && npm run typecheck && npm run format:check
```

Whole tree before the push: `go build ./... && go build -tags=http,scheduler,memory,cli,gateway,swarm ./...`; `go test -tags=http,scheduler,memory,cli,gateway,swarm ./...` (the express `make test` adds the SPA build and `test-agent-rules`); `make test-race`; `make lint`; `make lint-windows`; `make check-windows`; `make check-android`; `make test-cache` is not needed (no prompt or history change).
Russian wording: `git grep -n -i 'агентск'`, `git grep -n -i 'сабагент'`, `git grep -n -i -e 'рабочее дерево' -e 'рабочего дерева' -e 'рабочем дереве' -e 'рабочие деревья'` find only the rule files.
Documentation: `make docs && make docs-check`, `make site-schema-check`, `make site-docs-check`; embedded SPA: `make build TAGS="http ui"`. The executable spec: `go test -tags=http ./external/httpserver -run TestRemoteModelFeature -count=1` and `go test -tags=http,swarm ./external/httpserver -run TestRemoteModelRelayFeature -count=1` with no tag exclusion; optional `CODDY_TEST_NETNS=1 go test -tags=http ./external/httpserver -run VanishedPeer`.

**Plan sections to update when this is done** (`docs/plans/remote-model-provider.md`): the status line and 6b (a phase-2 status paragraph and the decisions of this plan); 4.2 (usage bullet: final fields (`reset_in_s`, no `resets_at`), the 404 sentence; the paragraph "A peer that vanishes without a trace": the per-leg mechanisms (O1, T1 at 15 s / 30 s, the relay's client leg), the measured facts, the correction of the tunnel sentence, W1/W2); 4.3 (the capability bullet: implemented, the usage bullet: W3); 4.5a (nothing changes, the tunnel bound is cited); 5 (item 2 done, the phase-2 scenarios); 5a (Server, Dry run, Transport (W8), SPA rows); 6 (Privacy gains the usage exposure statement) and 6a (the four new models, "Open" items closed: the retransmission figure measured, the resolver and tri-state keys implemented); the four reports `p2-d1-config-source`, `p2-d2-usage-cache`, `p2-d3-vanished-peer` and `p2-d4-busy-notice` added to the table of 6a, one line of decision each.
