# Coddy embedded UI specification

This page captures the original UI requirements and the intended end state. It is a functional spec and a design contract.

## Constraints

- UI ships as static assets embedded into the `coddy` binary (build tag `http`).
- Runtime has no auth and no API key checks for the UI.
- UI must work over the same origin as `coddy http`.
- UI copy is English.
- Favicon matches [coddy.dev](https://coddy.dev/) (**`/coddy-favicon.svg`**, same mark as **`docs/assets/coddy-logo-mark-flat.svg`**, plus PNG/ICO fallbacks embedded with the SPA).

## Appearance (light / dark theme)

- **Default:** dark theme on first visit.
- **Cookie:** **`coddy_ui_theme`** with values **`dark`** or **`light`** (path **`/`**, **`SameSite=Lax`**).
- **Toggle:** **Settings** (**`#/settings`**) → **Appearance** → **Dark** / **Light** (**`data-testid="theme-toggle-dark"`**, **`theme-toggle-light`**).
- **Settings sub-panels (Appearance / Skills) are mutually exclusive** — opening one closes the other. Only one sub-panel may be expanded at a time.
- **Persistence:** switching theme writes the cookie and sets **`document.documentElement.dataset.theme`**; reload must keep the chosen theme.
- **CSS contract:** **`--text`** and **`--bg`** on **`[data-theme="light"]`** are **`#18181b`** and **`#f8f8fa`**; glass panels use **`rgba(255, 255, 255, 0.9)`** (not dark tint). Dark defaults remain on **`:root`** / **`[data-theme="dark"]`**.

## Environment (local / remote server)

- **Workspace-row chip:** an environment selector sits in the composer workspace-context row above the input, next to the folder / branch / worktree chips (**`EnvironmentChip.tsx`**, rendered inside **`.composer-context-row`**, styled as a **`.workspace-chip--env`**, **`data-testid="composer-env-btn"`**), Claude-Code style — **not** in Settings. The chip shows **`Local`** or the remote's name. It opens a portal menu (**`data-testid="composer-env-menu"`**, mode-menu family; bottom sheet on mobile) with an **Environment** section (**Local**) and a **Remote** section (configured remotes + **`+ Add remote…`**).
- **Select = connect:** choosing **Local** or a remote connects **immediately** (no confirm step) and reloads; there is no per-select token prompt. A **bearer token** is entered only in **`+ Add remote…`** (name / URL / token) and remembered per-remote.
- **Reachability dots:** each remote shows a status dot probed on menu open — **green** reachable+authorized (a cross-origin **`GET /v1/models`**), **red** unreachable / CORS-blocked / unauthorized, **amber** while probing. **Local** is always green.
- **Purpose:** point the UI at a remote, already-running **`coddy http`** server, or use the local one. Offered remotes come from the local server's **`httpserver.remotes`** (**`[{name, url}]`**); **`+ Add remote…`** takes an ad-hoc name/URL/token.
- **Client-side state:** the active env lives in **`localStorage`** key **`coddy_env`**; per-remote tokens in **`coddy_env_tokens`**. Never persisted to server config; leave empty for a remote without auth. Workspace **folder recents** are namespaced per environment (**`envStorageSuffix()`**) so each remote remembers its own last paths; **models** and defaults come from the remote's **`GET /v1/models`** after the reload.
- **Mechanism:** a global **`fetch`** shim (**`external/ui/src/ui/env/remoteEnv.ts`**, installed in **`main.tsx`**) rewrites same-origin API requests (**`/v1/*`**, **`/coddy/*`**, **`/openapi*`**) to the selected remote base URL and adds **`Authorization: Bearer <token>`**. Local mode is a transparent pass-through. Selecting an entry persists the choice and reloads so all state re-fetches from the chosen backend; the SPA shell always loads from the local origin, so you can always switch back to **Local** from the chip even if the remote is down.
- **CORS:** the remote must allow the UI's origin via **`httpserver.cors`** (see [http-api.md](http-api.md)). SSE re-attach (**`GET /coddy/sessions/{id}/composer-stream`**) is fetched (not `EventSource`), so the bearer header applies; that route also accepts **`?access_token=`** for external `EventSource` clients.
- **Failure surfacing (issue #60):** a `fetch()` to a remote that is unreachable / refused / TLS-or-DNS-failed / CORS-blocked rejects with a `TypeError` (no `Response`); the send flow's final `catch` now distinguishes that from the user's own `AbortError` and emits an error `system_notice` (**`remoteSendErrorMessage`**), and a readable `401/403` gets an auth-specific message (**`remoteHttpErrorMessage`**) instead of a bare status. Pure helpers live in **`external/ui/src/ui/env/remoteErrors.ts`**.
- **Active-env health (issue #60):** a shared monitor (**`external/ui/src/ui/env/activeHealth.ts`**, started in **`main.tsx`**) probes the *selected* environment's **`GET /v1/models`** on load, on a 30 s interval, and on window focus. The composer chip dot is driven by that health (green up / red down / amber checking, **`.env-status`**), and **`EnvHealthBanner`** shows a persistent alert with a **Switch to Local** action when the active remote is down or unauthorized, so the app never silently renders empty against a dead backend.

## Layout

Desktop layout

- **Brand** is **typography only** (**Coddy** and **agent**). **No** circular logo or icon before the brand text, regardless of older reference images that include a circle.
- Desktop nav is a **vertical panel** with rounding on the **right** edge (not a full-height center-pill). On **`min-width: 1920px`**, the wide rail header includes an icon with **horizontal lines** used **only** to **collapse** to narrow rail, not as a global navigation drawer.
- Left rail opens **chat history** from **History** under the brand; brand click goes to the **start screen** (**new chat**).
- **Brand**, **History**, **Scheduler** (when linked), **Settings**, and each row in the **History** list use real fragment **`href`** values (**`#/`**, **`#/history`**, **`#/scheduler`**, **`#/scheduler/new`** (new job editor), **`#/settings`**, **`#/s/<sessionId>`**) so **middle-click** or **Ctrl/Cmd-click** opens a **new browser tab** on the same origin while another tab can keep streaming.
- Sessions list is **always** a **drawer overlay** with backdrop at **all** breakpoints and rail widths (**no** inline column beside the rail that would shrink the chat area). The panel heading and related chrome use the copy **History**.
- Optional rail **narrow versus wide** (icons plus labels) only when **`min-width: 1920px`**, persisted in **`coddy_nav_rail`** cookie (**`narrow`** default)
- Main chat area with streamed assistant output
- Right rail is out of scope for the current milestone

Wide screens

- **`min-width: 1920px`** may enable the rail widen control and cookie-backed layout (**see DESIGN.md**). **History** remains a **floating drawer** next to the measured nav column (**`--rail-shell-track-width`**); do not fix **`left`** with a static pixel constant for wide rails.

Mobile layout

- On mobile the left rail becomes a top bar to preserve horizontal space; the top bar is **`position: fixed`** at the viewport top (**`shell-main`** is padded with **`--coddy-mobile-top-inset`**) while **`body`** scrolls the chat.
- On mobile the brand stays on a single line.

Header links

- GitHub link to `https://github.com/coddy-project/coddy-agent` (**new tab**, `rel=noopener`).
- API docs link to `/docs/` (**new tab**, `rel=noopener`).
- Links live in the nav rail for this milestone.

Narrow-rail tooltips (desktop)

- When the rail has **no** wide labels, **hover tooltips** reinforce icon meaning (example **New Chat** on the brand, **History** on history). **Wide labeled rail** hides those tooltips; labels are the affordance.
- After opening **History**, the history trigger's tooltip must **not** stay visible if the pointer still hovers the rail (see **DESIGN.md**).

## Sessions

- Session id is generated client side only after the first message is sent from a new chat.
- Session id is persisted in the URL fragment.
  - Recommended format `#/s/<sessionId>`
- Unsent composer text may be kept as a client-only draft session.
  - Draft sessions use `#/draft/<draftId>` and are stored in `localStorage` under `coddy_draft_sessions_v1`.
  - History rows show a `Draft:` title prefix.
- Session id is sent in the `X-Coddy-Session-ID` header for chat transport.
- Session id validation matches `internal/session/ValidateFolderSessionID`.
- Session persisted files live under the session directory and are deleted together when the session is deleted.
  - `tool_calls/` tool call history
  - `stats.json` token usage totals

### Parallel sessions and generation cancel

- Several sessions may **stream at once**, each with its own **`POST /v1/responses`** and **`X-Coddy-Session-ID`**. The app keeps a **per-session shadow** transcript so rapid hash switches do not mis-route SSE updates; see **`pickStreamMutationBase`** in **`external/ui/src/ui/chat/streamMutationBase.ts`**.
- **Stop** uses **`POST /coddy/sessions/{id}/cancel`** and **`AbortSignal`** on the streaming **`fetch`**. The server persists **partial** assistant **`content`** for that turn when tokens had already arrived. **`GET /coddy/sessions/{id}/messages`** may return an older snapshot briefly; the UI **merges** with local shadow or visible rows when the response is only a prefix (**`mergeTranscriptPreferLocalSuffix`**, **`keepLocalTranscriptIfServerEmpty`** in **`external/ui/src/ui/chat/transcriptServerSnapshot.ts`**). The transcript is cleared on fetch failure **only** when the failed load targets the **currently viewed** session so Stop does not wipe the chat.

Session title

- UI shows the session title in the chat header.
- When the title is missing, UI shows `New chat`.
- Title is editable inline. On blur the UI saves via `PATCH /coddy/sessions/{id}`.

### Per-session model

- **New chat** defaults **Model** from cookie **`coddy_llm_model`**, then **`default_agent_model`** from **`GET /v1/models`**, then the first YAML row.
- **Opening a session** restores **Model** from **`GET /coddy/sessions/{id}/messages`** field **`model`** (session override on disk), not from the cookie.
- Changing **Model** writes the cookie (default for the next **New chat**) and **`PATCH`** **`selectedModelId`** on the active session. ReAct turns still send **`metadata.model`** on **`POST /v1/responses`**.
- **Many models / long names** — backend ids are **`vendor/model`**. When more than one vendor is configured the menu groups rows under an uppercase vendor header and each row shows only the model name (full id stays in the row tooltip). On desktop the list scrolls with a ~5-row cap. When there are **more than 5** backends a **filter input** appears at the top (auto-focused) that matches the vendor, model name, or full id (case-insensitive); **Enter** picks the first match, **Escape** closes, and an empty result shows a “No models match …” notice. Filter/group/threshold logic is in **`chat/llmModelMenu.ts`** (unit-tested in **`llmModelMenu.test.ts`**; menu wiring covered by **`ComposerModelMenu.test.tsx`**).
- **Mobile sheet** — on narrow/mobile shells (the **`max-width: 1199px`** shell-stack breakpoint) the **Mode** / **Model** / **Reasoning** menus open as a **full-width bottom sheet** over a dimmed scrim — the same pattern as the slash (**`/`**) and **`@`** pickers — instead of a cramped anchored dropdown. The filter and grouping still apply inside the sheet. Desktop keeps the anchored dropdown.

### Per-session reasoning level

- A **Reasoning** selector appears in the composer next to **Model** **only** when the active model exposes **`reasoning_levels`** from **`GET /v1/models`** (reasoning models such as gpt-5 / o-series / Claude thinking models). Levels are derived from **`models[].reasoning_levels`** (auto-detected from the model id when unset) and propagated through **`ModelInfo.reasoningLevels`** → **`llmReasoningLevels`** in **`App.tsx`** → **`Composer`**.
- **New chat** defaults the level from cookie **`coddy_llm_reasoning`**, then the model's **`reasoning_default`**, then **`medium`** (or the first offered level). **Opening a session** restores it from **`GET /coddy/sessions/{id}/messages`** field **`selectedReasoning`**. Switching to a model that does not offer the current level clamps it to a valid one (see **`pickReasoningLevel`** in **`chat/reasoningSelection.ts`**).
- Changing the level writes the cookie and **`PATCH`** **`selectedReasoning`** on the active session; ReAct turns also send **`metadata.reasoning`** on **`POST /v1/responses`** so a brand-new session applies it on the first turn.

### Per-session workspace (folder / branch / worktree chips)

- A chip row renders at the top of the composer card (**`WorkspaceChips.tsx`**, helpers in **`chat/workspaceContext.ts`**): **folder chip** (workspace basename, full path in tooltip), **branch chip** (current git branch; only when the workspace is a git repository), and a **worktree checkbox**.
- Context loads from **`GET /coddy/workspace/context`** with **`X-Coddy-Session-ID`** whenever the viewed session changes; without a session the server default cwd is shown.
- **Chosen once**: folder + branch + worktree are set before the conversation starts. Once the transcript has messages the chips lock (**`workspaceLocked`** — controls disabled, menus closed) and the server answers **409** to **`POST .../workspace`**.
- **Folder chip** opens the **Recent** menu (Claude Desktop style): MRU folders from **`localStorage`** **`coddy_workspace_recents_v1`** (**`chat/workspaceRecents.ts`**), current workspace marked with **✓**, then **`Open folder…`** at the bottom which opens the **folder browser modal** (**`WorkspaceFolderModal.tsx`**) fed by **`GET /coddy/workspace/folders?path=`**: rows navigate into folders, **`..`** goes up, **Open** picks the currently browsed folder, **Cancel** dismisses. Picking calls **`POST /coddy/sessions/{id}/workspace`** **`{"path"}`** — the session cwd switches and persists; skills, project rules, and slash commands re-derive from the new cwd.
- **Branch chip** opens the branch list (current first, marked selected). Picking one posts **`{"branch", "worktree": <checkbox>}`**: in-place checkout by default, a dedicated worktree under **`<home>/worktrees/<repo>/`** when the checkbox is on, or a jump to the worktree that already has the branch checked out (including back to the main checkout).
- **Worktree checkbox** (**`composer-worktree-checkbox`**, real **`input[type=checkbox]`**) is the worktree preference; when the session already runs inside a linked worktree it shows checked and disabled.
- **Pre-session (draft/home)**: picks are stored client-side, previewed via **`GET /coddy/workspace/context?path=`**, and applied to the new session id on first send before **`POST /v1/responses`**. Switching to another session drops pending picks.
- Errors (missing folder **400**, git conflicts / locked workspace **409**) keep the current chips; the context is re-fetched to stay truthful.
- Automated checks: **`chat/workspaceContext.test.ts`**, **`chat/workspaceRecents.test.ts`** (helpers), **`chat/WorkspaceChips.test.tsx`** (chips, menus, modal, lock); backend behavior is specified executable in **`features/workspace_switching.feature`** (godog).

## Session list

- **History** panel lists sessions via `GET /coddy/sessions` (still a **drawer**, not a persistent second column).
- Pagination uses `limit` and `cursor`, with **infinite scroll** for older rows.
- Optional **`q`** query string (**title substring or first **`user`** message content substring only**, case insensitive; **not** full-chat search). Search input updates use client debouncing.
- Indicators
  - A spinner appears on rows for sessions that are still generating in the background.
  - A violet dot appears only when a background session completed while it was not the active chat.
  - A question mark icon appears when a session is waiting for user permission.
- CRUD
  - Rename via `PATCH /coddy/sessions/{id}` setting `title`.
  - Delete via `DELETE /coddy/sessions/{id}`.
  - Create new chat starts on the home screen. Session id is created only on first send.

Session rename UX

- Title rename is done only in the chat header.
- On blur the UI saves via `PATCH /coddy/sessions/{id}`.

Session delete UX

- Each row has a trash icon button.
- Clicking delete shows one confirm dialog and then calls `DELETE /coddy/sessions/{id}`.
- If the deleted session is **not** the one currently shown in the main chat, remove it from the list (and refresh from the server) and **keep the History drawer open**. Do not change the URL or clear the transcript for the session that stayed on screen.
- If the deleted session **is** the one currently shown, navigate to **new chat** (empty start screen, session hash cleared), **close** the History drawer, and clear composer-related state as for a normal home transition.
- For a short interval after the user confirms delete, **ignore** shell **backdrop** pointer-driven close so a stray event from the native confirm does not dismiss History or alter the route.

## Chat transport

- Primary transport is `POST /v1/responses`.
- `stream: true` uses SSE.

Mode selection

- UI lets the user select a mode from `GET /v1/models` (at minimum `agent` and `plan`).
- Selected mode is sent as `model` field in `POST /v1/responses`.

SSE payloads

- Default SSE lines stream OpenAI like deltas.
- Named SSE events
  - `tool_call`
  - `tool_call_update`
  - `plan`
  - `token_usage`
  - `usage_update` (`used` / `size` for the current model context; emitted again after compaction)
  - Default (no `event:`): chat completion chunk deltas, including `delta.content` and optional `delta.reasoning_content`

## Composer primary action (`#btn-send`)

Context ring and breakdown popover

- **Hover** on **`.composer-context-tip-host`**: compact tooltip (percent, input/output/total, max context) unchanged.
- **Click** opens **`ContextBreakdownPopover`** beside the ring on wide viewports (**`context-breakdown-menu--portal`**); on stacked shell (**`max-width: 1199px`**) it uses the same bottom sheet + scrim as slash / **`@`** pickers (**`context-breakdown-menu--sheet`**, **`slash-sheet-backdrop`**). **Escape** or **Close** dismisses; hover tooltip returns when closed.
- Legend keys map to **`contextBreakdown`** on **`GET /coddy/sessions/{id}/stats`** (`systemPrompt`, `toolDefinitions`, `rules`, `skills`, `mcp`, `conversation`). Live **`usage_update`** SSE replaces the displayed total immediately (including after `/compact` or automatic compaction), then the UI refreshes the detailed stats. Vitest: **`Composer.test.tsx`** (`click context ring opens breakdown popover`) and **`consumeComposerSse.order.test.ts`** (`usage_update replaces the displayed current context after compaction`).

Shape and glyphs

- The control sits to the **right** of the context ring (**`.composer-icon`** on **`Composer.tsx`**).
- The hit target is a **perfect circle**: equal **width** and **height**, **`border-radius: 50%`**, **`box-sizing: border-box`** (currently **42×42px** in **`styles.css`**). Do **not** ship a rounded square or squircle for this control unless the visual spec explicitly changes again.
- **Play** (**idle**, draft non-empty): Unicode triangle **`▶`**, enlarged vs body text (**`~22px`** glyph via **`composer-send-glyph`**), slight horizontal nudge for optical centering.
- **Stop** (**while streaming**): filled square **`.composer-stop-square`** (**14x14px**, centered in the **42px** circle). Stays in **`composer-bar-actions`** on the right, next to the context ring.
- **Disabled** idle state when textarea is whitespace-only (**`:disabled`** on **`composer-send-play`**).

Behavior (unchanged summary)

- **Enter** submits when idle and not generating; **`Shift+Enter`** newline. No submit while **`generating`**.
- **Stop**: **`POST /coddy/sessions/{id}/cancel`** + **`fetch`** **`AbortSignal`**. The server may append a **partial** assistant message for that turn. **`GET /coddy/sessions/{id}/messages`** can lag; the bundled UI merges server rows with local shadow or on-screen items (**`transcriptServerSnapshot.ts`**). Details in **`DESIGN.md`** (**Multi-session streaming and Stop**) and **`docs/http-api.md`**.

Regression

- Automated UI checks (**Playwright MCP** or **`@playwright/test`**) MAY assert **`#btn-send`** **`offsetWidth`** **≈** **`offsetHeight`** and computed **`border-radius`** **≥ half** **`min(width,height)`** (within sub-pixel tolerance).

## Composer file attachments (multimodal)

- The paperclip button (**`data-testid="composer-file-input"`** hidden `<input type="file">` triggered by a visible icon button) appears in the composer **only** when the active model has **`multimodal: true`** from **`GET /v1/models`**. The flag is derived from **`models[].multimodal`** in YAML config and propagated through **`ModelInfo.multimodal`** → **`llmModelMultimodal`** in **`App.tsx`** → **`Composer`** prop.
- Attached files are held in **`attachedFiles: File[]`** state on **`Composer`**. Preview chips appear above the composer input showing file name and type icon.
- On send, **`App.tsx`** reads each file as a data URL via **`FileReader`** and includes **`inline_files: [{name, data_url}]`** in the **`POST /v1/responses`** body.
- **Agent / plan turns**: the server writes each file to **`~/.coddy/sessions/<id>/assets/`** (permissions **`0o444`**) and injects a **`<coddy_session_assets>`** XML block into the user message so the agent can **`read`** or **`cp`** those paths. Duplicate asset names get **`_1`**, **`_2`** suffixes (see `internal/session/assets.go` **`SavePartsToAssets`**).
- **Direct YAML model turns**: each file becomes an **`image_url`** content part sent inline to the provider.
- The user bubble strips the XML annotation via **`stripCoddyAttachmentsForUserDisplay`** in **`stripCoddyAttachments.ts`** and shows file chips (**`msg-user-files`** / **`msg-user-file-chip`** CSS classes). **`parseSessionAssetFiles`** re-derives chip metadata on page reload.
- After a **`PUT /coddy/config`** save in Settings, **`App.tsx`** bumps **`modelsEpoch`** → re-fetches **`/v1/models`** so the attachment button appears or disappears without a page reload.

| Case | Expected | Automated check |
|------|----------|-----------------|
| FA1 | Paperclip visible only when `llmModelMultimodal` is true | `Composer.test.tsx` |
| FA2 | File chips render in user bubble after send | `stripCoddyAttachments.test.ts` |
| FA3 | Chips persist on reload via `parseSessionAssetFiles` | `stripCoddyAttachments.test.ts` |

## Composer slash skills and mirror caret

Authoritative narrative and visual tokens live in **`DESIGN.md`** (slash picker, mirror contract, verification table). This section is the functional contract for regression.

Wire and draft

- **`textarea#composer`** holds **plain text** only. Invoked skills appear as **`/<name>`** tokens (space after picker selection). The UI **must not** persist **`[/<name>](coddy-skill:<name>)`** in the draft.
- First user turn on **`POST /v1/responses`** carries the same plain slash tokens as the composer value (no client-side markdown injection for skills in the request body).

Picker and segmentation

- Menu visibility and **`prefix`** derive from **`slashMenuDraftAtCaret`** in **`external/ui/src/ui/skills/draftSlash.ts`** (line-start or whitespace before **`/`**, optional suffix, not inside fences or blockquotes).
- Mirror highlighting uses **`segmentComposerSlashSpans`** in **`external/ui/src/ui/skills/segmentComposerSlashSpans.ts`** (mid-line **`/`** supported; **`x/foo`** is not a command token).

Mirror and caret alignment

- Non-empty drafts: textarea text is drawn **transparent**; **`.composer-mirror-inner`** shows the visible line including **`.composer-skill-chip-inline`** (**`data-testid="composer-skill-chip"`**).
- Composer chips **must not** use horizontal **padding**, **margin**, or a **border** that changes inline width. Use **`box-shadow`** for outline. **`font-family`**, **`font-size`**, **`line-height`**, **`font-weight`**, **`letter-spacing`** on chip and **`#composer`** must match so the caret lines up (**`ResizeObserver`** syncs scrollbar gutter).

Transcript vs composer

- **`user_message`** bubbles render **plain text** only (**`msg-user-body`**, **`white-space: pre-wrap`**). No Markdown pipeline, no transcript skill chips (**`coddy-skill-span`**). Slash tokens such as **`/path/to`** and YAML blocks stay exactly as persisted, with line breaks preserved.
- Composer mirror chips (**`composer-skill-chip`**) apply **only** while editing **`#composer`**, not in the transcript.
- Persisted user turns may carry hydrated attachments as **`coddy_attachment`** XML with **`path`**, **`name`**, and CDATA file bodies (**`internal/agent`**). **`stripCoddyAttachmentsForUserDisplay`** replaces each XML block with a compact **`@path`** **only when** that path is **not** already present as an **`@`** mention in the surrounding text (**avoids duplication** because the persisted turn already repeats the **`@`** in the user text plus the hydrated block).

Verification use cases

| ID | Expectation | Primary automated check |
| --- | --- | --- |
| UC1 | One chip for **`asdfasf /find-skills asdfasdf`**, plain **`textarea.value`** | **`external/ui/src/ui/chat/Composer.test.tsx`** (`composer highlights plain slash token as chip while editing`) |
| UC2 | Mid-line menu open after whitespace | **`draftSlash.test.ts`** (`slashMenuDraftAtCaret works after whitespace mid-line`) |
| UC3 | **`x/foo`** no chip for **`/foo`** | **`segmentComposerSlashSpans.test.ts`** (`segmentComposerSlashSpans skips letter before slash`) |
| UC4 | Line-leading **`/foo`** chip | **`segmentComposerSlashSpans.test.ts`** (`segmentComposerSlashSpans line start slash`) |
| UC5 | **`stripCoddySkillMarkdownLinks`** on legacy paste | **`segmentComposerSlashSpans.test.ts`** (`stripCoddySkillMarkdownLinks restores plain slash token`) |
| UC6 | User bubble keeps **`hi /demo there`** plain (no **`coddy-skill-span`**) | **`UserMessage.test.tsx`** |
| UC7 | Multiline YAML / paths keep **`\\n`** layout in **`user-message-body`** | **`UserMessage.test.tsx`** |
| UC7b | Display-only **`slugSlashes`** (plain **`/`** and legacy mix) | **`segmentComposerSlashSpans.test.ts`** (`slugSlashesForUserBubbleMarkdown …`; composer / legacy only, not transcript) |
| UC8 | Live **`coddy http`**: **`fontFamily`** parity chip vs **`#composer`**, caret **`selectionStart === value.length`** at EOL after fill | **Playwright MCP** **`browser_evaluate`** after **`make build TAGS="http ui"`** |
| UC9 | User bubble hides **`coddy_attachment`** bodies, shows **`@path`** only | **`UserMessage.test.tsx`**, **`stripCoddyAttachments.test.ts`** |

## Composer **`@`** workspace files

- **`textarea#composer`** keeps plain **`input`** including literal **`@path`** text. **`POST /v1/responses`** adds **`attachments`** (**`path`** only) parsed by **`extractAtFileAttachments`** in **`external/ui/src/ui/skills/draftAt.ts`** for **`agent`** / **`plan`** only. Server-side **`HydratePromptContentBlocks`** uses **`ExtractAtFilePathsFromText`** (**`internal/session/at_paths_extract.go`**) after filling empty **`resource`** bodies so **`@path`** literals inside **`type: text`** blocks become extra **`resource`** rows when that path is not already hydrated (**matches HTTP **`attachments`** without duplicating**).
- **`@`** menu uses **`GET /coddy/workspace/files`** with **`dirs=true`** so **`kind`** **`dir`** rows drill down. Choosing a **`dir`** inserts **`@`** + **`path_rel`** (often ending in **`/`**) without hydrating file body. Choosing a **`file`** inserts **`@`** + **`path_rel`** plus a trailing ASCII space where appropriate. **`Composer`** defers two **`updatePickerMenus`** ticks after a row choice so the workspace dropdown does not immediately reopen (trailing space and **`MENU_PATH_CHAR`** still satisfy **`atMenuDraftAtCaret`** until the user edits again).
- Empty **`@`** prefix (caret right after **`@`**) loads recent rows from **`localStorage`** (**`workspaceAtRecents`**), keyed by **`sessionId`** (or **`__no_session__`** before the first assigned id), with no extra banner line (**`Type after @ to search`** only when the list is empty). Entries come from **`@`** row picks and **`extractAtFileAttachments`** on successful profile sends (**`migrateWorkspaceAtRecents`** merges when the client generates or the server rotates **`X-Coddy-Session-ID`**).
- Fenced code blocks and Markdown blockquote lines suppress **`@`** menu parity with **`draftSlash`** ( **`inMarkdownFenceBeforeCaret`**, **`blockquoteLine`** ).
- Mirror **`@`** styling uses **`segmentComposerMirrorSpans`** (**`composer-at-chip-inline`**, **`data-testid="composer-at-chip"`**). **`listAtPathSpans`** (**`draftAt.ts`**) chips every completed **`@path`** atom even when prose follows (**`draftAt`** parity with **`extractAtFileAttachments`**), while text after the caret that is still inside **`MENU_PATH`** stays on the active token until the **`atMenuDraftAtCaret`** lexer breaks out.
- **`@`** search with zero matches keeps the picker open (**`No files`**) instead of collapsing the menu (**`composer-at-chip-inline`** hides for **`atNoMatch`**, same **`atIdx`**, **`prefix`** as the stale filter).
- Stacked-shell viewports (**`(max-width: 1199px)`**) render workspace and slash pickers as a **`slash-menu--sheet`** with **`slash-sheet-backdrop`** so the panel is usable on phones.
- Picker subtitle uses **`workspacePickRowSubtitle`** - second column shows **`parent/`** only when **`path_rel`** is nested, root entries omit it (empty string).

| Case | Expected | Automated check |
| --- | --- | --- |
| AT1 | Spaces inside paths ( **`readme copy.md`** ) work in picker draft and hydrate when attached | **`draftAt.test.ts`**, **`session/promptfiles_test.go`** (**`hello world.txt`**) |
| AT2 | **Prefix** substring filter (**case-insensitive**), empty **prefix** returns empty **`items`** on server | **`TestCoddyWorkspaceFilesGetPagingAndPrefixes`** |
| AT3 | Prose **`see @note.txt`** does not merge **`and`** into the path segment | **`draftAt.test.ts`** (**`extractAtFileAttachments`** connector words) |
| AT4 | **`@`** inside **`session/prompt`** text alone still hydrates (no duplicate when **`attachments`** or **`resource`** already has body text) | **`TestHydratePromptContentBlocksExpandsAtInText`**, **`at_paths_extract_test.go`** |
| AT5 | Picker second column shows **`parent/`** for nested **`path_rel`**, empty at workspace root (**`workspacePickRowSubtitle`**) | **`workspacePickRowSubtitle.test.ts`** |

## Transcript message types

The chat transcript renders a flat list of UI message blocks. Each block has a `type` and a minimal set of required fields.

- `user_message`
  - Plain user input text (**no Markdown**; **`pre-wrap`** preserves line breaks).
- `thinking`
  - Renders model reasoning as a lightweight disclosure row.
  - Status `in_progress` shows label `thinking...` and a spinner.
  - Status `completed` shows label `thinking` and preserves the text for review.
  - Multiple `thinking` blocks may appear in one turn (reasoning can resume after tool calls).
- `tool_call`
  - A single tool execution row, same disclosure chrome as **thinking** / **memory** (**chevron**, **`thinking-label`** with the tool name or kind, **`thinking-dur`** for duration or **`-`**).
  - While **`pending`** or **`in_progress`**, the summary label uses a **`...`** suffix (for example **`read_file...`**). **`startedAtMs`** drives a live duration until the tool finishes.
  - Details show arguments and streamed result when expanded. The **result** body is plain text only (rendered like **`<pre>`**, **no** Markdown pipeline), monospace, muted grey (**`.tool-result-raw`**). If **`resultPreviewTruncated`** is false / **`resultWasTruncated`** unset, no **Load more** link and no fixed-height viewport (block height follows content). If truncated (19 content lines plus **`...`**), apply the capped viewport (~20 lines), **overflow-y** hidden until **Load more**. **Load more results** (**`data-testid="tool-result-more-link"`**) performs **GET `/coddy/sessions/{id}/tool-calls/{toolCallId}`**, then **overflow-y auto** and **Hide** (**`data-testid="tool-result-hide-link"`** ); **Hide** restores the clipped preview without a second GET while **fullResultText** stays in memory.

## Tool call card (bundled SPA, current)

Authoritative behaviour matches **`DESIGN.md`** tool timeline plus this checklist.

| Concern | Current behaviour |
| --- | --- |
| Component | **`ToolCallMessage.tsx`** - **`thinking-row coddy-tool-call-row`**, **`details.thinking-details.coddy-tool-details`**, **`data-testid`**: **`tool-details-{toolCallId}`** |
| Summary | Same pattern as **thinking** (**`thinking-summary`**, **`thinking-left`**, **`thinking-chevron`**, **`thinking-label`**, **`thinking-dur`**), **`aria-label="Tool summary"`** |
| Args | **`pre.tool-block`**, **`aria-label="Tool arguments"`** (inside **`thinking-body coddy-tool-call-body`**) |
| Result | **`div`** with **`tool-block tool-result tool-result-raw`**, **`aria-label="Tool result"`**, inner **`pre.tool-result-pre`** |
| Markdown | Not used for tool **result** or **user** bubbles; **assistant** still uses Markdown per below |
| List merge | **`App.tsx`** **`loadMessages`** merges **`GET /coddy/sessions/{id}/tool-calls`** rows into **`resultText`**, **`resultWasTruncated`**, timing |
| Full text | First **Load more** only - **`GET /coddy/sessions/{id}/tool-calls/{toolCallId}`**, use JSON **`result`** (same object includes **`meta`**, **`args`**) |
| CSS | **`styles.css`**: **`.coddy-tool-call-row`**, **`.coddy-tool-call-body`**, **`thinking-details:not([open])` body hidden**, plus **`.tool-result-raw`** and viewport / toggle classes above |

- `assistant_message`
  - Final assistant output text for the turn, after tool calls.

## Live token usage

- UI must show token counters while the agent is working.
- Counters update when SSE event `token_usage` arrives.
- Update granularity is per completed backend model call, not per generated token.
- UI restores token counters after restart via `GET /coddy/sessions/{id}/stats`.

## Markdown rendering

- Tool outputs are excluded; they stay raw monospace text (**`ToolCallMessage`**).
- **User** messages are plain text with preserved line breaks (**`UserMessage`**).
- **Assistant** messages may contain Markdown.
- UI renders Markdown with fenced code blocks and syntax highlighting.
- Each code block has a copy button that copies only that block content.

## Markdown line editor (shared)

Implemented as **`MarkdownLineEditor`** (`external/ui/src/ui/markdown/MarkdownLineEditor.tsx`). Used for:

- Scheduler job **`body (markdown)`** (`SchedulerJobEditorSheet`, default **`minRows`** **10**).
- Plan document card markdown mode (`PlanDocumentSection`, **`minRows`** **4**, class **`md-line-editor--plan`**).

Behaviour (see **`DESIGN.md`**, **Markdown line editor**):

- Full parent width; editor height follows content (minimum logical rows); **no** scrollbar on the inner **`textarea`**.
- Gutter shows one number per **logical** line (`\n`-separated). Wrapped visual lines leave **blank** gutter cells (no duplicate numbers).
- Caret logical line: highlight spans **all** visual rows of that line; active gutter number tinted.
- Wrap measurement uses a hidden probe with the same font and text width as the textarea; visual rows = **`ceil(height / lineHeight)`**.
- Long unbreakable tokens wrap (**`overflow-wrap: anywhere`**); no horizontal scroll inside the editor.

Automated checks:

- `external/ui/src/ui/markdown/MarkdownLineEditor.test.tsx`
- `external/ui/src/ui/markdown/markdownLineGutter.test.ts`

## Plan document card (plan mode transcript)

Transcript type **`plan_document`** renders **`PlanDocumentSection`** in the main chat column (not a right rail).

Data and API:

- Persisted in **`messages.json`**; hydrated fields include **`slug`**, **`name`**, **`overview`**, **`content`**, optional **`body`**, **`path`**, **`discarded`**.
- Body edit: **`PUT /coddy/sessions/{id}/plans/{slug}`** with **`{ "body": "<markdown>" }`** (debounced autosave).
- Discard: **`DELETE /coddy/sessions/{id}/plans/{slug}`** sets **`discarded: true`**; card remains visible, controls disabled.
- Run plan: client triggers implementation run (metadata / prompt; see **`docs/acp-protocol.md`**).

UI requirements:

- Collapsed: title, one-line description, **Discard** and **Run plan** in footer; title **`title`** tooltip = absolute plan file path when known.
- Expanded: **Preview** default (rendered markdown via **`Markdown`**); eye toggle switches to **`MarkdownLineEditor`**.
- Content pane grows with document length for **both** preview and markdown (**no** inner max-height scroll on the pane).
- Expanded desktop (**`min-width: 640px`**): title row and action buttons share the top row; body full width below.
- Editor body excludes YAML frontmatter (client **`planEditorBody`**); preview uses the same body text.

Automated checks:

- `external/ui/src/ui/chat/PlanDocumentSection.test.tsx`

## Plan and todo list (legacy rail)

- Optional right-rail plan entries (if present in a build) use **`GET /coddy/sessions/{id}/plan`**, **`PUT`**, **`POST .../plan/archive`**.
- Distinct from the **`plan_document`** transcript card above.

## Long term memory

Memory tree roots

- `global`
- `workspace`

Tree API

- `GET /coddy/sessions/{id}/memory/tree`
  - Without `root` returns the roots list.
  - With `root` and optional `path` lists children.
- Only `.md` and `.txt` files are listed.
- Path traversal must be rejected.

File API

- `GET /coddy/sessions/{id}/memory/file` reads.
- `PUT /coddy/sessions/{id}/memory/file` writes.

## Swagger

- Swagger UI is served under `/docs/`.
- OpenAPI spec is served under `/openapi.yaml` and `/openapi.json`.
- Swagger UI assets must be embedded, no CDN.

## Development workflow

- Edit TypeScript sources under `external/ui/src/`.
- Use `npm --prefix external/ui run dev` to iterate without rebuilding the Go binary.
- Build and sync embed assets with `npm --prefix external/ui run build:go`.
- **`make build TAGS="http ui"`** runs the UI build step (**make ui-build**) before linking the embedded bundle.

## Reference images

Store the provided design reference images under `docs/assets/`.

When describing a specific element, link to the relevant image file.

- Full HD UI tour (README): `docs/assets/screenshot-fullhd-start.png`, `screenshot-fullhd-chat.png`, `screenshot-fullhd-history.png`, `screenshot-fullhd-scheduler.png`, `screenshot-fullhd-scheduler-job.png`, `screenshot-fullhd-settings.png`, `screenshot-fullhd-settings-skills.png`, `screenshot-fullhd-settings-appearance.png`
- Mobile UI tour (README): `docs/assets/screenshot-mobile-start.png`, `screenshot-mobile-chat.png`
- Home layout: `docs/assets/ref-home-1.png`, `ref-home-2.png`, `ref-home-3.png`
- Home scroll state: `docs/assets/ref-home-scroll.png`
- Composer state: `docs/assets/ref-home-composer.png`
- Left rail icon states: `docs/assets/ref-rail-states.png`
- Chat history view: `docs/assets/ref-history.png`
- Chat transcript view: `docs/assets/ref-chat.png`
- Flow montage: `docs/assets/ref-flow.png`

## UI test scenarios

These scenarios are intended to be automated via Playwright against the Vite dev server.

- Desktop navigation has no width toggle
  - Given viewport width is at least 1024px
  - When the app loads
  - Then `data-testid="nav-menu"` is visible
  - And `data-testid="nav-toggle-width"` is not present

- Sessions are drawer only
  - Given any desktop viewport
  - When the app loads
  - Then `data-testid="sessions"` is not visible
  - When user clicks `data-testid="nav-menu"`
  - Then `data-testid="sessions"` becomes visible
  - When user clicks `data-testid="sessions-close"`
  - Then the sessions drawer is hidden

- Mobile uses top bar and single line brand
  - Given viewport width is at most 1199px
  - When the app loads
  - Then the nav width toggle is not present
  - And the nav rail height is 78px
  - And sessions can still be opened from the menu button

- Tool calls survive restart
  - Given a session has tool calls executed
  - When the user reloads the page
  - Then tool call cards are visible in the transcript
  - And expanding a tool card shows args and raw grey **result** preview
  - And if the server marked the preview truncated, **Load more results** then **Hide** behave as in the table above; if not truncated, there is no **Load more** row and no **`tool-result-viewport--tall`** on the result panel

- Tool result truncation (Playwright MCP)
  - Given a persisted session whose tool output on disk exceeds the preview line cap
  - When the user opens the tool card and clicks **Load more results**
  - Then the link becomes **Hide**, full lines are available inside the same max-height scrollable panel, and **`.tool-result-viewport--scroll`** has **`scrollHeight`** greater than **`clientHeight`**
  - When the user clicks **Hide**
  - Then the preview shows the capped text ending in **`...`**, **`overflow-y`** is hidden on **`.tool-result-viewport--clip`**, and **Load more results** appears again

- Token usage survives restart
  - Given a session has non zero token usage
  - When the user reloads the page
  - Then the token usage HUD shows the persisted totals

- Memory copilot row (Playwright MCP)
  - Given **`memory.enabled: true`** on the **`coddy http`** process and at least one Markdown file under global or workspace memory so recall can run
  - When the user sends a chat message that completes a full ReAct turn
  - Then an element with **`data-testid="memory-copilot-row"`** appears after that user bubble for the turn (grey **memory** foldout, same visual language as **thinking** per `DESIGN.md`)
  - When the user opens the details element
  - Then the streamed **memory** body shows the text merged into the main agent prompt for that turn (and optional saved-note preview when the copilot wrote `coddy_memory_save`)

For Playwright MCP against a live gateway, start **`make build TAGS="http ui"`** then **`./build/coddy http`** with a disposable **`--home`** so config can enable memory; open **`http://127.0.0.1:<port>/`**, navigate to a session, send a prompt, assert the snapshot contains **memory-copilot-row** and folded body text after expand.
