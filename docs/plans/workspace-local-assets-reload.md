# Workspace-local assets, worktree recovery, and fast History

## Evidence and baseline

- GitHub issue [#146](https://github.com/coddy-project/coddy-agent/issues/146) reported the original `${CWD}` baking defect. Its fix (PR #147) is present: `internal/config/expand.go` preserves braced `${CWD}` and `ConfigToJSONDTO` / YAML round trips retain it. No replacement implementation is needed.
- `Skills.ApplyDefaults` already includes `~/.agents/skills`, `${CODDY_HOME}/skills` (normally `~/.coddy/skills`), and `${CWD}/.coddy/skills`. `internal/skills/loader.go` follows symlinked skill directories through `os.Stat`, and `TestLoadSymlinkDirWithSKILLMd` covers that primitive.
- `Manager.buildFreshState`, session loading, and `SetSessionWorkspace` already reload skills/rules by `State.CWD`; the subagent runtime resolves its catalog lazily from `State.CWD` on each turn.
- The remaining MCP management defect is concrete: `external/httpserver/mcp_mgmt.go` uses `s.defaultCWD` for list/probe/trust/toggle/CRUD, while `MCPSection.tsx` sends no `X-Coddy-Session-ID`. Settings therefore only sees and edits the server-launch workspace.
- Stored HTTP sessions defer configured MCP connections until a prompt. The requested behavior is to warm configured MCPs when the SPA explicitly activates a selected chat, without starting processes during passive session reads.
- `FileStore.ListSnapshotsWith` reads and decodes every `messages.json` before applying archive/CWD/tag/origin filters. `/coddy/sessions` calls it twice per request (for `active_count` and result rows). A large archive therefore makes default History slow even though archived rows are omitted.
- A removed managed worktree leaves a stored CWD under `<repo>/.coddy/worktrees/<name>`. History uses that literal path and a cold-loaded chat then points its workspace menu at a directory that no longer exists.

## Invariants

1. A braced `${CWD}` remains a session placeholder through every config load/save path; default skill directories remain `~/.agents/skills`, `${CODDY_HOME}/skills`, and `${CWD}/.coddy/skills`.
2. A passive load—including `/coddy/slash-commands`, `/coddy/skills`, `/coddy/mcp`, paged transcript reads, remote reads, export, cancel, compaction, and permission recovery—does not launch deferred configured MCP processes.
3. Only an explicit, valid activation of an ordinary non-archived parent chat warms its deferred MCP servers. It is non-blocking, idempotent, state-owned, and timeout-bounded; a prompt still waits for its tool list.
4. Project-local MCP declarations, cache entries, trust receipts, switches, and reconciliation are scoped by canonical workspace. Config and home declarations, plus project-trust policy, remain global.
5. Only a missing path structurally below `<valid-repository>/.coddy/worktrees/<name>` recovers to that repository root. Effective recovered CWD is distinct from the persisted CWD and never overwrites it automatically.
6. Default History listing reads metadata, not transcripts. A persisted count is used only when it matches the identity of `messages.json`; legacy or stale metadata falls back to decoding only where a count is necessary.

## Implementation plan

### 1. Targeted workspace-resource regressions

- Keep existing #146 implementation and defaults unchanged. Extend its existing fixtures with two project workspaces and a symlinked local skill, so switching/reopening sessions proves that the correct session CWD selects local slash/skill entries.
- Retain focused coverage for rules and subagents requested by the user: through their existing agent/mention paths, prove a session opened in each workspace observes that workspace’s catalog. Do not add a second loader or duplicate generic #146 config-round-trip mechanisms.
- Clarify the required project directory spelling `.coddy/skills` in `docs/features/skills.md`.

### 2. Workspace-aware MCP management API

- Add a shared MCP-workspace resolver built on `resolveSessionCWD` for the workspace-dependent routes: `GET /coddy/mcp`, server/tool enable/disable, trust/untrust, local `PUT`, and `DELETE`. No header retains server-default CWD; malformed header is 400; unknown header is 404 and cannot fall back to mutate default CWD.
- Resolve the `ManagedServer` and its origin before each mutation. Use an explicit mutation scope:
  - `OriginProject` limits management/reconciliation to the resolved canonical workspace;
  - config and home origins reconcile all affected sessions;
  - `POST /coddy/mcp/project-trust` is deliberately global, ignores session headers, and keeps its all-session policy reload.
- Add a manager reconciliation entry point that accepts this scope, filters states by `CanonicalWorkspacePath(st.GetCWD())` for a project action, and preserves the existing turn-lock parking and shared pool leases.
- Cache probes by `mcp.PoolKey(server, workspace)`. Each cache entry retains its origin, server name, and canonical workspace. Invalidation rules are exact:
  - a project mutation evicts only matching `{OriginProject, name, canonical workspace}` entries;
  - config/home mutation evicts every matching `{origin, name}` entry across all workspace-bound `PoolKey` variants;
  - deleting a local shadow evicts only its local entry and preserves an already-valid global cache which it unmasks.
- Table-test every workspace-dependent MCP route for no header, malformed header, unknown header, and a valid header. Assert invalid/unknown requests leave declarations, trust receipts, and overrides untouched; assert the global project-trust route ignores an invalid header. Cover same-named local declarations in two projects, a workspace-bound global declaration in two projects, and shadow/unshadow transitions.
- Update OpenAPI and `docs/reference/http-api.md` route by route with header/no-header/400/404 behavior.

### 3. Active-session MCP Settings UI

- Thread a server-known active session ID from `App` through `Settings` and `SettingsSection` into `MCPSection`; client-only draft IDs remain headerless.
- Send `X-Coddy-Session-ID` for all workspace-dependent list/refresh/mutation calls. A new chat remains headerless and intentionally addresses the server default workspace.
- Treat the ID as a resource generation. On change, reset workspace-specific editor/expanded state, issue a new list request, and ignore late list or mutation completions captured under the previous `{sessionID, generation}`. An old mutation must not reload or clear state belonging to the new session.
- Add Vitest coverage for headers, new-session refetch/reset, stale list response, delayed mutation response after switch, and client draft behavior.

### 4. Explicit, non-blocking MCP activation

- Add an opt-in query marker (for example `activate_mcp=1`) to `GET /coddy/sessions/{id}/messages`. The SPA grants one token per selected-session generation and consumes it **before** starting its initial transcript request. It adds the marker only for that one request; StrictMode/retry, older-page reads, rebase, live attachment, post-stream reconciliation, remote reads, and non-current shadow refreshes remain marker-free and are explicitly tested.
- The handler validates the marker, resolves the state, and calls a manager method only when the state is an ordinary, non-archived, non-child, non-scheduler session. The manager acquires the same prompt-turn synchronization used by reload reconciliation, atomically reserves/consumes the deferred marker, creates/registers its background MCP generation, then releases the lock before any dial proceeds. Reload or workspace change can therefore cancel/supersede the reserved generation without a `takeDeferred → beginBackground` duplicate-leases window.
- Reuse the existing trust gate, pool, cancellation, `MCPConnectUpdate`, and `mcpStartTimeout`. A warmup racing with config reload or workspace reload leaves only the latest generation’s clients installed; a first prompt waits for the current background connection before exposing tools.
- Do not change `SetSessionWorkspace` scheduling without a separate reproducible blocking defect: its existing synchronized reload contract protects a turn from old-workspace MCP tools.
- Add unit/BDD coverage for activation returning promptly with a silent server, exactly one dial on repeated/concurrent activation, activation interleaved between deferred consumption and config/workspace reload, first-prompt timeout behavior, and passive `/slash`, `/mcp`, generic/paged messages remaining cold.
- Document the explicit activation query in OpenAPI and MCP/Web UI references. Run `make test-race` for this state-worker change.

### 5. Strict deleted-managed-worktree recovery

- Add a pure `RecoverManagedWorktreeCWD` helper. For a missing CWD, it accepts only a cleaned path matching `<repo>/.coddy/worktrees/<name>[/...]`, verifies the recovered `<repo>` is an existing main git checkout, and returns it. It handles native path separators and rejects arbitrary missing paths, lookalike `.coddy/worktrees` paths, and missing/non-repository parents.
- Represent a recovered session with both an effective CWD and a persisted-CWD override. Metadata list construction produces an effective CWD before CWD filters, text search, group/repo-root projection, and bulk-selection decisions; it memoizes recovery per raw path within a listing. Cold load carries the same effective CWD for menu/tools but remembers the stored deleted worktree path for persistence. Ordinary `Save` writes the persisted override unchanged; only explicit `SetSessionWorkspace` clears it and writes a new CWD.
- Add unit and workspace BDD coverage for `git worktree remove`: History lists/groups/searches the session under the repository parent and an opened chat resolves there; an ordinary save after recovery retains the deleted worktree in `session.json`; a random deleted directory remains unchanged.

### 6. Metadata-first History

- Extend `SessionMeta` with optional message-count metadata and a `messages.json` identity (file size and modification timestamp). `FileStore.Save` refreshes these only after the transcript is safely written and records the identity it observed; a crash/order mismatch, external edit, legacy bundle, or stale metadata is unknown rather than trusted.
- Introduce a metadata-only scan that reads `session.json` for normal and child bundles, applies archive/CWD/tag/origin filters before any transcript operation, and returns the rows needed for sorting/paging. Default `active_count` and result rows are derived from one such scan rather than two full snapshot walks.
- Use known counts only when current file stat identity still matches. For legacy/stale metadata decode messages only when required: all candidate rows for `sort=messages`; title/first-user search candidates as today; or the selected page for `include_stats`. Token totals remain page-scoped except for `sort=tokens`, which already needs every candidate total.
- Preserve ordering, offset paging, tags, archive behavior, origin filters, subagent/scheduler inclusion, activity, and bulk-delete semantics. Ensure `include_stats` reports exact counts after its targeted enrichment.
- Test modern fast rows, legacy/mismatched fallback, message sorting, page-only stats enrichment, child bundles, and default lists containing large/corrupt archived transcripts without decoding them. Add an HTTP scenario holding active-count plus page semantics under the metadata-first path.

### 7. Documentation and verification

- Update `docs/features/mcp.md`, `docs/features/skills.md`, `docs/features/worktrees.md`, `docs/features/sessions.md`, `docs/surfaces/web-ui.md`, `docs/reference/http-api.md`, and `external/httpserver/openapi.go` when the respective behavior changes.
- Rebuild UI embeds (`make build TAGS="http ui"`) and provide before/after screenshots of the affected MCP Settings and History/worktree states at relevant viewport widths.
- Run focused red/green Go, Godog, and Vitest tests; then `make test`, `make test-race`, `make docs-check`, `make site-docs-check`, and `make lint`.
- Cross-review this revised plan before coding and cross-review the final diff after verification; address actionable findings.
