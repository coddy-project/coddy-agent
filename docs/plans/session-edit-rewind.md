# Message editing: in-place rewind, no branches, no file rollback

## Context

Today, editing a sent user message **forks the conversation**:

- `POST /coddy/sessions/{id}/branches` copies every message **before** the Nth
  user message into a **new** session bundle, reverses workspace turn diffs
  (`diffs/turn_<n>.json`, `RestoreWorkspaceFiles`), records `branches.json`
  metadata in both bundles, and returns `newSessionId`.
- The SPA navigates to that new session and sends the edited text there. Both
  threads stay readable; a `‹ n/m ›` `branch_nav` row appears under the branch
  point; `resolveLatestLeaf` redirects session opens to the freshest leaf.

Operator's intent: **the edit should rewrite the message and rewind the history
in the same session** — one linear conversation, no branches ("транки"), no
file rollback, no navigator. Additionally: the pencil icon moves into the
bubble's footer row (`.msg-user-foot`), before the copy button, always visible
like copy is.

## Goal

- `POST /coddy/sessions/{id}/rewind` `{ "userMessageIndex": N }` — truncates
  `messages.json` at that user message **in place** (the message itself and
  everything after it is dropped), in the **same** session.
- The SPA sends the edited text as an ordinary prompt to the same session —
  the conversation continues linearly from the rewind point.
- The pencil sits in `.msg-user-foot`, before `MessageCopyIconButton`, always
  visible.
- **Remove** the whole branch machinery (`branches.json`, both `/branches`
  endpoints, `branch_nav`, `BranchNavigator`, `resolveLatestLeaf`) **and** the
  per-turn workspace diff/rollback (`diffs/`, `TakeWorkspaceSnapshot`,
  `captureAndStoreTurnDiff`, `RestoreWorkspaceFiles`).

## Non-goals

- No file-system rollback at all (explicitly dropped by the operator).
- No UI for recovering the truncated tail — it is gone (same acceptance as the
  operator stated).
- Console/ACP/Telegram surfaces never had message editing; nothing to remove
  there (verified: no branch/edit code in `external/cli`, `internal/acp`,
  `internal/remote`, `external/gateway`).

## Endpoint contract

`POST /coddy/sessions/{id}/rewind` — body `{ "userMessageIndex": N }`,
0-based over `user` rows (a `background_wake` row counts as a user message —
same counting as `sliceMessagesBeforeUserN` and the SPA's `userMsgIndices`).

Responses:

- `200` `{ "object": "coddy.session_rewound", "sessionId": "...", "messagesRev": R }`
  — history now ends right **before** the Nth user message.
- `400` — malformed id, negative index, or index at/beyond the last user
  message boundary it cannot resolve (N ≥ user-message count means "rewind
  past the end" → nothing meaningful to drop → `400`).
- `404` — unknown session.
- `409` — subagent child / scheduler run (read-only transcripts, same refusal
  wording as today's branch endpoint) **or a turn in flight**
  (`Manager.SessionTurnActiveInProcess` covers this process; the existing
  turn-lock probe covers other processes if already exposed — check
  `manager_turn_lock_*`; refusal mirrors `deleteSessionBundle` semantics).

Rationale for a separate verb (not `PATCH messages`): rewind is a destructive
operation with its own guards; the edited text then goes through the ordinary
`POST /v1/responses` path, which reuses attachments, mentions, queue, mode,
model metadata — exactly like the branch flow did with two requests.

## Session layer — `internal/session/rewind.go` (new)

`func (m *Manager) RewindSession(sessionID string, userMessageIndex int) (uint64, error)`

- The HTTP handler first does `coddyEnsureLoaded`, so the `*State` is live.
  `RewindSession` resolves `st := m.getSession(id)`; if nil (defensive, e.g. a
  future caller that skipped ensure-load), fall back to truncating
  `messages.json` on disk through `m.store` — or simply return an error; decide
  in review (leaning to: require loaded state, keep one code path).
- Refusals map to existing sentinels: `ErrSubagentReadOnly`,
  `ErrSchedulerSessionReadOnly` (read via `snap.Meta` / `st`), plus new
  `ErrSessionTurnActive` and `ErrRewindOutOfRange`.
- New `State` method `TruncateMessagesBeforeUserN(n int) (uint64, error)`:
  - under `s.mu`: compute the prefix with the **same** counting as
    `sliceMessagesBeforeUserN` (move that helper to `rewind.go`; it counts
    `llm.RoleUser`, which is how `background_wake` rows are stored too);
    assign `s.Messages = prefix`; `markMessagesEdited()` (bumps `msgRev` and
    `msgEditRev` → the `MessagesForPersist` cache invalidates and `since_rev`
    consumers see a new revision); unlock; `touchPersist()` → the persist
    worker rewrites `messages.json` atomically.
- Bundle cleanup in the same call (all on `st.GetPersistedSessionDir()`):
  - `os.Remove(branches.json)` and `os.RemoveAll(diffs/)` — legacy artifacts
    whose readers are gone. (Other untouched sessions keep those files on
    disk harmlessly; they are never read again.)
  - `pending_permission.json`: if `ReadPendingPermission` succeeds and its
    `toolCallId` is no longer among the remaining messages' tool calls →
    `ClearPendingPermission` (a stale gate would otherwise resurrect a
    phantom permission card after reload).
  - `tool_calls/<id>/` entries whose id is not referenced by the remaining
    messages → delete. Keeps the bundle honest; "More…" can only fetch ids
    that exist in the transcript anyway.
  - `ui_log.json`: drop entries with `userTurnIndex > userMessageIndex`
    (entries are stamped with `CountUserTurns` at write time, i.e.
    1-based turn number; surviving turns are `<= userMessageIndex` — verify
    off-by-one during implementation with a unit test).
- Returns the new `messagesRev`.
- Untouched on purpose: `todos/`, `plans/`, `assets/`, `stats.json`,
  `session.json`, `subagents/` children (they are transcripts of runs that
  happened; deleting them loses real history — they stay openable by URL),
  the message queue (only fills while a turn runs; rewind is refused then),
  background tasks (live processes keep running; their cards stay in the
  Tasks panel — a task whose `tool_call_id` left the transcript simply does
  not map onto a row).

## HTTP handler — `external/httpserver/coddy_rewind.go` (replaces `coddy_branches.go`)

- `POST /coddy/sessions/{id}/rewind` → `coddyRewind`.
- Order of checks mirrors `coddyBranchCreate`: id validation → JSON body →
  `userMessageIndex >= 0` → `coddyEnsureLoaded` (loads bundle into manager) →
  read-only refusals (subagent, scheduler — reuse `writeSubagentsError`) →
  active-turn refusal (`SessionTurnActiveInProcess`, plus the disk turn lock
  if `TurnLockHeld`-style helper exists for cross-process parity with
  delete) → `mgr.RewindSession` → `200` with `messagesRev`.
- **Watcher notification**: publish a `session_rewound` event on
  `GET /coddy/events` (`{sessionId, messagesRev}`), so other tabs / remote
  clients watching the same session refetch instead of showing a phantom
  tail. On the SPA side: new `ServerEvent` variant + `parseServerEvent` +
  `dispatchServerEvent` in `chat/serverEvents.ts` (plain data, worker-safe),
  and an `App.tsx` handler that calls `loadMessages` fresh for the viewed
  session (drop its shadow tail first). If reviewers judge this overkill for
  v1, it degrades gracefully to "other tabs resync on next load" — but the
  initiating tab must still reconcile correctly either way.
- `bgWG`: `captureAndStoreTurnDiff` is its only producer; after removal the
  field and `s.bgWG.Wait()` in shutdown become dead — remove them (check no
  other `bgWG.Add` exists).

## Go removal inventory

- `internal/session/branches.go` — delete file. Move `sliceMessagesBeforeUserN`
  (renamed or kept) into `rewind.go`. Everything else is branch-only:
  `BranchFile`, `BranchPoint`, `BranchSessionRef`, `BranchOrigin`,
  `ReadBranchFile`, `WriteBranchFile`, `branchPointForIndex`, `messagePreview`,
  `userMessageAt`, `sessionLastUpdatedMs`, `stampLastUpdated`, `liveRefs`,
  `refPosition`, `BranchPointView`, `CreateBranchSession` (+Params/Result),
  `PruneBranchRefs`, `dropBranchRef`, `LoadBranchPointViews`, `TurnDiffsDir`,
  `TurnNumber` (only the diff flow used it; `CountUserTurns` in `ui_log.go`
  stays — it has other callers).
- `internal/session/turn_diff.go` — delete file (snapshot/diff/restore; only
  the branch flow used it).
- `internal/session/branches_test.go` — delete; port the
  `sliceMessagesBeforeUserN` counting cases into `rewind_test.go`.
- `internal/session/subagent_test.go:797-803` — branch-refusal test → rewrite
  as rewind-refusal test.
- `external/httpserver/coddy_branches.go` — delete; `registerBranchRoutes()`
  call in `server.go` → `registerRewindRoute` (or fold the single route into
  the existing route table).
- `external/httpserver/server.go` — remove `TakeWorkspaceSnapshot` +
  `captureAndStoreTurnDiff` at ~L650 and ~L1221 (both turn entry paths), and
  `bgWG` if unused.
- `external/httpserver/coddy_coddy.go` — remove `PruneBranchRefs` call +
  comment in `deleteSessionBundle`.
- `external/httpserver/openapi.go` — remove `/branches` paths, `branchPoints`
  schema block, `fileRollbackNote`, the `branches.json` sentence in the
  DELETE description; add `/rewind` (request/response/status codes).
- `external/httpserver/bdd_branch_delete_test.go` — delete; replaced by
  `bdd_rewind_test.go` (reuse the `storedSession` harness shape).
- `features/session_branch_delete.feature` — delete; replaced by
  `features/session_rewind.feature`.
- `internal/docs` — embedded docs rebuild automatically; nothing manual.

## SPA removal inventory (`external/ui/src`)

- `ui/chat/branchInject.ts` + `branchInject.test.ts` — delete.
- `ui/chat/resolveLatestLeaf.ts` + `resolveLatestLeaf.test.ts` — delete.
- `ui/chat/BranchNavigator.tsx` + `BranchNavigator.test.tsx` — delete.
- `ui/chat/types.ts` — remove the `branch_nav` variant.
- `ui/messages/MessageList.tsx` — remove `branch_nav` rendering +
  `onBranchSwitch` prop.
- `ui/chat/ChatScreen.tsx` — remove `onBranchSwitch` pass-through.
- `ui/App.tsx` — remove: `resolveLatestLeaf` + `branchInject` imports,
  `pendingBranchSendRef`, `skipLeafResolveRef`, `switchBranch`, the
  `resolveLatestLeaf` hop in `pickSession`, the `/branches` fetch +
  `injectBranchNavItems`/`deduplicateBranchNavs` in `loadMessages`,
  `handleBranchSend`. Replace with `handleRewindSend(text, userMsgIdx)`:
  `POST /coddy/sessions/{id}/rewind` → on `200`: drop `streamShadowBySidRef`
  tail for the session (so `mergeTranscriptPreferLocalSuffix` cannot
  resurrect removed rows) → `await loadMessages`-style refresh →
  `streamResponses(text, files)` **in the same session**. On failure: restore
  the draft + editing state and surface the error the same way the failed
  branch create did (UI-log error row; keep `editingUserMsgIdx` so retry is
  one click).
- `App.stopQueue.test.tsx`, `App.reasoningSelection.test.tsx` — drop the
  `/branches` fetch stubs.
- i18n `en.ts` / `ru.ts` — remove `chat.branchPrev`, `chat.branchNext`,
  `app.branchCreationNoSessionId`; keep `messages.editMessage` (or whatever
  key the pencil's aria label uses — verify); add `app.rewindFailed`-style
  key if the failure path needs wording.
- `styles.css` — remove `.msg-user-edit` absolute/hover rules (~L3187-3222)
  and `.branch-nav*` (~L3224-3264).

## Pencil relocation

- In `UserMessage.tsx`: delete the absolutely-positioned `.msg-user-edit`
  button inside `.msg-user`; render an edit button inside `.msg-user-foot`,
  **before** `MessageCopyIconButton`, always visible (the foot row is already
  always rendered). Reuse the `.msg-copy-icon-btn` chrome (bare icon, hover
  link-violet, `title` tooltip + `aria-label` = `messages.editMessage` or the
  existing edit key); a pencil SVG sized like the 16px copy glyph. Keep a
  `data-testid` (`msg-user-edit` or `msg-user-edit-<id>` — check what tests
  assert).
- Order in the foot: `[edit][copy][time]` — left of copy, per the operator.
- Update `UserMessage.test.tsx`, `userFootCss.test.ts` (it pins the foot
  structure), `transcriptRowContainmentCss.test.ts` if it asserts the old
  classes, and any MessageList-level tests referencing `branch_nav`.

## New tests / specs

- `internal/session/rewind_test.go`:
  - truncation at index 0 → empty history; at index k → prefix kept, same
    session id, `messagesRev` bumped, `messages.json` rewritten on persist;
  - `background_wake` rows count as user messages (same counting as UI);
  - out-of-range → `ErrRewindOutOfRange`;
  - subagent / scheduler sessions → read-only refusal;
  - cleanup: `branches.json` + `diffs/` deleted, `pending_permission.json`
    cleared when its tool call was dropped, `tool_calls/` orphans pruned,
    `ui_log` entries of dropped turns removed (pin the off-by-one);
  - a later `AddMessage` appends at the rewind point (new turn continues in
    place).
- `features/session_rewind.feature` + `external/httpserver/bdd_rewind_test.go`
  (happy path only):
  - stored session with 2 user messages → `POST /rewind {1}` →
    `GET /messages` returns only the first turn, same `sessionId`;
  - rewind then a new prompt continues in the same session (the harness's
    stub runner produces the reply; assert no new session id appears).
- `server_test.go`-adjacent unit tests for 400/404/409 mappings.
- UI vitest: `UserMessage` pencil-in-foot (always rendered, order before
  copy); an `App`-level test that the edit-send posts `/rewind` (not
  `/branches`) and keeps `sessionId`.

## Docs sweep (same PR)

- `docs/surfaces/web-ui.md` — rewrite **Message editing and conversation
  branches** → **Message editing** (pencil → draft → send → same-session
  rewind; no navigator); update the test list.
- `docs/features/sessions.md` — bundle table: drop `branches.json` +
  `diffs/`; rewrite the "Editing a sent message" section; drop the branch
  paragraph + `screenshot-fullhd-branches.png` embed; delete paragraph: drop
  the branches.json retraction sentence; update the test list.
- `docs/reference/http-api.md` — replace the two `/branches` rows with
  `/rewind`; update DELETE description.
- `DESIGN.md` — replace **Branch navigator (edited messages)** with an
  "Editing a sent message" section (pencil in `.msg-user-foot`, always
  visible, same-session rewind); drop `branch_nav` from the transcript block
  types list; fix the `background_wake` bullet and the chevron bullet that
  mention the navigator; fix the memoized-rows note if it names it.
- `README.md` — "branches from an edited message" → rewind wording.
- `docs/assets/screenshot-fullhd-branches.png` — delete (docs-check fails on
  unused assets). New screenshot of the foot pencil for the updated
  section(s) (`<feature>-<state>-dark-1280.png` naming).
- `openapi.go` — covered above.
- `git grep -nI 'branch'` sweep for leftovers in `docs/`, `examples/`,
  comments, i18n, `AGENTS.md`/`.cursor/rules` (workspace/git-branch hits
  stay, obviously).
- `docs/plans/` — untouched (design records). This file lands as
  `docs/plans/session-edit-rewind.md` in the worktree before the PR.
- `make docs` for regenerated pages; `make docs-check` must pass (unused
  asset would fail).
- No config schema keys touched → steps 7/8 (schema + site) not needed.
  `make site-docs-check` after doc edits.

## Edge cases / races (for reviewers to weigh in)

- Another client sends a prompt between our `/rewind` and the resend → the
  new message lands at the tail after the truncated prefix; acceptable and
  linear (same race existed across two requests today).
- A second tab watches the session during rewind → `session_rewound` event
  refetches; without it, stale tail until next load.
- Rewind a session that was itself created by the old branch flow → it is an
  ordinary session; rewind just works.
- Archived session → composer is blocked anyway; endpoint does not gate on
  `archived` (same as branches today) — confirm.
- `messagesRev`/`since_rev` — relay frames written pre-rewind describe
  removed content; the initiating client re-reads messages after rewind, so
  `?since_rev=` attaches only for watchers that already had the tail (rare,
  benign: frames replayed are for a still-running turn, which a rewind
  refusal prevents).
- Deleting a *session* never prunes anything now — `PruneBranchRefs` gone.

### Review amendments (cursor auto + qwen3.8-high + swe-2-high — all verified against code)

- **Client resurrection guard:** after `200` call `streamShadowBySidRef.current.delete(sessionId)` (whole entry, not tail) then `loadMessages(sessionId, { freshLoad: true })` — `freshLoad` is what skips `itemsRef.current` as `localForMerge`. Also drop the session's pending records from `permissionPromptSessionStore` (localStorage), else `restorePermissionPrompts` resurrects a synthetic `permission_prompt` for a truncated `toolCallId`.
- **In-memory UILog:** prune `s.UILog` under `s.mu` inside `TruncateMessagesBeforeUserN` (keep `userTurnIndex <= n`; `ui_log` is 1-based, `userMessageIndex` 0-based — the `<= n` rule is confirmed correct by `appendUILog`'s clamp at `ui_log.go:81`), `touchPersist` then persists both messages.json and ui_log.json.
- **Turn refusal:** reuse the existing `sessionTurnActive()` helper (`external/httpserver/composer_stream_http.go` ~L138: `SessionTurnActiveInProcess` + `session.TurnLockHeld(dir)`); 409 when either reports activity. `deleteSessionBundle` cancels-and-awaits rather than refuses — do not cite it as precedent.
- **Retry UX:** keep `editingUserMsgIdx`/draft/`editingFiles` until the `/rewind` POST returns 200 (today's onSend clears them before the async call — move the clear after success, restore on failure).
- **Removal inventory additions:** `external/httpserver/server_test.go` L265+L283 (route must-list + method map → `/rewind`), `external/httpserver/subagents_http_test.go` L131+L137 (POST `/branches` on child → `/rewind`), `features/background_wake_web_ui.feature` L22 ("a branch after a wake" scenario — rework to rewind), `external/ui/bdd_turn_progress_ui_test.go` L140-142 (drives `branchInject` — repoint), i18n `chat.branchLabel` (en L929, ru L946) in addition to `chat.branchPrev/Next`, `docs/nav.yaml` summary line, `docs/README.md` L46, `external/ui/src/ui/messages/transcriptRowContainmentCss.test.ts` L42 (pins `.msg-user-edit` — must update), `.msg-user--editable` class on the bubble (becomes dead weight — drop).
- **`session_rewound` event:** server `publishSessionRewound` modeled on `publishConfigReloaded` (`events_hub.go:195` — direct `s.events.publish`, no `ServerEventsHub.track`/`hello` replay — a joining tab refetches anyway); document `event: session_rewound` in openapi `/coddy/events` section; cover in `events_http_test.go` like `config_reloaded`.
- **Verified OK (don't touch):** `snapshotRevByTranscript` WeakMap — keyed on transcript-array identity, a fresh load yields a new array (no stale `since_rev`); queue is in-memory-only and empty when no turn runs; `tool_calls/` prune is nice-to-have (walk `llm.Message` assistant `ToolCalls` + `ToolCallID` fields for the keep-set); `ErrSchedulerSessionReadOnly` exists; `bgWG` — verify `captureAndStoreTurnDiff` is the only `bgWG.Add` caller before removing field + `Wait()`.
- **Optional hardening:** re-assert `!SessionTurnActiveInProcess(id)` under `s.mu` right before the cut (closes the admit-after-check race; cheap, take it).

## Implementation order (BDD, small commits)

1. `internal/session/rewind.go` + `rewind_test.go` (red → green).
2. `coddy_rewind.go` + route + `features/session_rewind.feature` +
   `bdd_rewind_test.go` + openapi entry (red → green).
3. Remove Go branch/diff machinery (`branches.go`, `turn_diff.go`,
   `coddy_branches.go`, server call sites, prune-on-delete, tests, old
   feature file + harness, openapi cleanup).
4. SPA: rewind send flow + pencil-in-foot + all branch code removal +
   vitest updates + i18n + styles.
5. Docs + assets + README/DESIGN sweep; `make docs`.
6. `make test`, `make lint`, screenshots (before/after pencil, 1280 +
   390 dark), PR.
