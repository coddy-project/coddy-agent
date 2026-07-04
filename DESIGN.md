# Coddy Agent UI Specification

Purpose: authoritative reference for the embedded SPA built from `external/ui/`. Tokens and layouts live here before CSS tweaks land in production stylesheets.

## Design references

Store the design reference images under `docs/assets/` and link to the specific file when describing a pixel sensitive UI detail. Navbar parity with Cursor - style mockups lives in [`docs/assets/INDEX.md`](docs/assets/INDEX.md) (see Navbar section).

## Foundations

### Color

| Token | Hex | Usage |
|-------|-----|-------|
| background | `#121212` | main canvas |
| nav rail | `#252525` | icon rail |
| session list | `#1E1E1E` | secondary column |
| accent | `#9333EA` | actions, pills, emphasis |
| text primary | `#FFFFFF` | default copy |
| text muted | `#9CA3AF` | captions, timestamps |
| user bubble | `#2D2D2D` | outgoing chat |

### Light and dark theme

- **Default:** dark (`data-theme="dark"` on **`<html>`**).
- **Persistence:** cookie **`coddy_ui_theme`** (`dark` | `light`), same lifetime pattern as **`coddy_nav_rail`**.
- **Bootstrap:** inline script in **`external/ui/src/index.html`** applies the cookie before first paint; **`main.tsx`** calls **`bootstrapUiThemeFromCookie()`** on load.
- **Toggle:** **Settings** drawer (**`#/settings`**) → **Appearance** tab → theme swatch grid (**`AppearanceThemePicker`**, **`data-testid="theme-swatch-<id>"`**). Theme selection applies immediately and is client-side only (no config save).
- **CSS:** semantic tokens on **`:root`** / **`[data-theme="dark"]`**; **`[data-theme="light"]`** overrides **`--text`**, **`--bg`**, glass, canvas gradients, etc. Foreground tints use **`color-mix(in srgb, var(--text) …%, var(--coddy-blend-base))`** (**`transparent`** on dark, **`#ffffff`** on light) so text stays opaque and readable on each canvas. Hero headline gradient uses **`--coddy-hero-muted-mid`** / **`--coddy-hero-muted-end`** (no light-gray stops on a light background).

| Token (light) | Hex | Usage |
|---------------|-----|-------|
| background | `#F8F8FA` | main canvas |
| text primary | `#18181B` | default copy |
| text muted | `#52525B` | captions |
| glass panel | `rgba(255,255,255,0.9)` | composer, drawers (light frost, not dark tint) |

### Frosted glass panels

Floating **composer** card, **History** drawer chrome, **skills** slash menu, **Mode**, and **Model** dropdowns share **`--coddy-glass-panel-*`**: tint plus **`backdrop-filter`** on that surface **only**, so frosting stays **inside** the panel outline. Dimming overlays behind History or the slash sheet use **`--coddy-overlay-scrim-bg`** (**no** fullscreen blur behind the overlay).

### Typography and spacing

- System stack: **`system-ui`**, `-apple-system`, **`Segoe UI`**, **`sans-serif`**
- Comfortable padding: **`12px`** grid, radius **`12px`** (pill buttons **`999px`**)
- Density tuned for dashboards: desktop layout (single navigation style + fluid chat). Sessions are a drawer overlay.

### Token usage HUD

Muted footer row under composer shows **`input` / `output` / `total`** counts from streamed **`token_usage`** SSE payloads. Numbers update after each backend LLM pass (between tool executions), not per model token emitted over the wire.

Token usage totals are persisted per session and restored after restart.

## Layout

Left-to-right zones:

1. **Nav rail**: **History** opens the session list overlay; **Scheduler** opens the cron jobs drawer (requires **`coddy http`** built with **`http,scheduler`** and scheduler enabled). Brand goes to the empty start screen. **Brand is text only** (**Coddy** plus **agent**), **no** circle or logo mark before the label, even if a reference mockup shows one. Optional **narrow vs wide rail** (**icons only vs icons plus labels**) on viewports **`min-width: 1920px`**, persisted in **`coddy_nav_rail`** cookie (**`narrow`** default). **Brand**, **History**, **Scheduler**, and **Settings** use real **`href`** fragment targets so **middle-click** or **Ctrl/Cmd-click** opens a **new tab** on the same origin for parallel chats. GitHub and API docs links are **not** in the rail — they appear only in the **start-screen footer** (see Repo links below).
2. **Session list**: **always a drawer overlay** with a dimming backdrop. It must **not** consume a second grid column or shrink the chat canvas (no inline sessions column beside the rail at any breakpoint). **Panel chrome title copy is History** (not "Chats"). There is **no** global hamburger that opens a separate app menu; the **stacked-lines control** in the wide rail header **only** collapses the rail to the narrow (icons-only) layout, matching the references. Each row is a real **`href`** to **`#/s/<sessionId>`** so **middle-click** opens that chat in a **new tab**.
3. **Chat canvas**: on **`min-width: 1200px`**, editable title and transcript share **`#messages`** with **`overflow-y: auto`**, and **`.chat-bottom`** is **`position: absolute`** with **`--coddy-chat-scrollbar-gutter`** padding so the composer does not cover the scrollbar track. The sticky title uses **`.chat-title-column`** (**`max-width: 920px`**, centered) so the title bar matches the composer stripe. The pinned title region (**`.chat-scroll-sticky-head`**) carries an **opaque** canvas backing (**`background-color: var(--coddy-canvas-gradient-top)`**) so the title stays fixed at the top of the scrollport while transcript text scrolls **behind** it — no see-through bleed. The shield matches the canvas top color in every theme, so it reads as the title card floating over the page rather than a separate bar. On **`max-width: 1199px`** (phones, tablets, and smaller desktops), **`body`** scrolls (native scrollbar); **`.rail-column`** (top bar with brand and links) is **`position: fixed`** to the **viewport top** (**`.shell-main`** gets **`padding-top: var(--coddy-mobile-top-inset)`** so content clears it). The chat title row (**`.chat-scroll-sticky-head`**) is **`position: sticky`** with **`top: var(--coddy-mobile-title-sticky-top)`** (**`--coddy-mobile-top-inset` plus `--coddy-mobile-chat-stack-gap`**, same **12px** token as title **`padding-bottom`**) so spacing under the rail matches title-to-first-message rhythm. Only **`.rail-pill`** is frosted. In active chat, **`.chat-bottom`** is **`position: fixed`** to the viewport bottom so the composer stays on screen while **`chat-scroll-tail`** reserves space, **`ChatScreen`** uses **`window`** for stick-to-bottom, and the skills slash menu uses the same **`slash-menu--portal`** path as desktop (**`createPortal`**).

The right insights rail is removed for the current milestone.

### Settings drawer (tabbed master–detail)

The **Settings** drawer (**`#/settings`**, **`Settings.tsx`**) is **tabbed**, not one long sheet. Tabs are derived from the live config JSON Schema (**`deriveSettingsSections`** over **`/coddy/config/schema`**): each top-level property is a tab (label from its **`title`**), the rarely edited tail (**`scheduler`**, **`prompts`**, **`instructions`**, **`logger`**, **`sessions`**, **`gateways`**) folds into a single **System** tab, and a client-side **Appearance** tab is placed **first** (the default tab). There is no separate Appearance/Skills flyout or **`settings-dock-cluster`** side panel — both are tabs now.

- **Saving**: there is **no per-section save button**. The drawer footer keeps only **Reload** (**`data-testid="settings-reload"`**) and **Save all** (**`data-testid="settings-save"`**, PUTs the whole config after `/coddy/config/validate`). **Appearance** is client-side only and never touches the config PUT.
- **Layout**: on **`min-width: 1200px`** the drawer is wider (**`min(960px, …)`**) with a vertical section rail on the left (**`.settings-nav`**) and the content panel on the right (**`.settings-tabs-layout`** is `display:flex; row`). On **`max-width: 1199px`** (same breakpoint as the rest of the shell, via **`isMobileShell`** / **`shellBreakpoint.ts`**) the nav is a **2-wide tile grid** master–detail (**`SettingsTileGrid`**, **`.settings-tile-grid`**): each tile (**`data-testid="settings-tile-<id>"`**) shows the section **title** on top and a short 3–5 word **description** below (**`SECTION_DESCRIPTIONS`** in **`settingsSections.ts`**) styled after the Scheduler job rows. The description clamps to two lines with an ellipsis (**`-webkit-line-clamp: 2`**) and the full text shows in a native **`title`** tooltip on hover. Tapping a tile opens that section with a **← Back to sections** control (**`.settings-mobile-back`**); the desktop rail (**`SettingsNav`**) is unchanged.
- **List sections** (**LLM Providers**, **Logical Models**, **MCP Servers**) are **master–detail** (**`SettingsArraySection`**): a list of named buttons (labelled by **`name`** / **`model`**) with **Add**/**Remove**; **Add** or selecting a row hides the list and shows the item form, with **← Back to list**.
- **Logical Models** model field (**`ModelField`**) fetches the chosen provider's models from **`GET /coddy/providers/{name}/models`** for a pick-list, always with a manual-entry fallback. **ReAct Agent** and **Long-term Memory** default-model fields (**`ModelPicker`**) pick from configured logical models or accept manual entry.
- **Skills** tab (**`SkillsSection`**) combines the schema-driven **`skills.dirs`** editor with the installed-skills list (enable/disable via **`/coddy/skills`**).
- Object sections render their sub-schema fields directly (the tab already names the section); custom model editors are injected via the **`SchemaForm`** **`fieldOverride`** hook, not by forking the generic renderer.

### Session identifier in URL

`#/s/<sessionId>` survives reload/share as long as the browser hits the **same Coddy http instance** backing the **`sessions`** root hash. SPA keeps **`X-Coddy-Session-ID`** synced with whichever id anchors the fragment.

### Multi-session streaming and Stop

- The SPA may run **more than one** **`POST /v1/responses`** at a time, each with its own **`X-Coddy-Session-ID`**, while the user switches **`#/s/...`** quickly. Each session keeps a **shadow transcript** in memory so streamed rows from session **A** are never appended to session **B**. Routing uses **`pickStreamMutationBase`** in **`external/ui/src/ui/chat/streamMutationBase.ts`**.
- **Stop** (**`#btn-send`** as stop) calls **`POST /coddy/sessions/{id}/cancel`** then aborts the streaming **`fetch`**. The server **persists** assistant tokens already received for that turn when cancel lands mid-stream (**`internal/llm`** stream implementations return a partial **`Response`** with **`context.Canceled`** wrapped, then **`internal/agent`** **`Run`** appends **`RoleAssistant`** before surfacing **`StopReasonCancelled`**).
- Right after Stop, **`GET /coddy/sessions/{id}/messages`** can briefly omit or shorten the in-progress assistant row versus what is already on screen. **`loadMessages`** merges the server snapshot with the **local shadow** or **visible items** when the server list is a strict prefix of local (or the last **`assistant_message`** is a shorter prefix of local); see **`mergeTranscriptPreferLocalSuffix`** in **`external/ui/src/ui/chat/transcriptServerSnapshot.ts`**. A full page reload still converges once persistence matches **`messages.json`**.

### Scheduler hash routes

- The scheduler jobs drawer footer is a single **Add job** control (**plus icon**, native **`title`** tooltip), **right-aligned** in the drawer (no manual **Refresh** button, list still reloads when the drawer opens and after saves). The job editor uses the same **`sessions-head`** / **`sessions-close`** chrome as **History** and the scheduler list. The job editor footer uses **pause or resume**, **delete** as icon buttons with the same **`title` / `aria-label`** pattern; on **`max-width: 1199px`** those actions are **end-aligned** for reach. While the drawer stays open, the client **polls `GET /coddy/scheduler/jobs` about every 12 seconds** (silent, no list loading chrome) so **running**, **next_run_utc**, and **paused** stay in sync with the server.
- Each scheduler job row uses **two lines** - **job_id** on the first line with either the **paused** badge or **`Next … (UTC)`** beside it (same line, muted), then **description** on the second line. The row body is a real **`href`** to **`#/scheduler/jobs/<job_id>`** so **middle-click** opens that job in a **new tab**.
- The job editor footer keeps **Resume** or **Pause** and **Delete** on the **left** for shorter pointer travel.
- **`#/scheduler`** opens the **Scheduler** jobs list drawer. **`#/scheduler/new`** opens the list with the **new job** editor (Add job sets this hash). **`#/scheduler/jobs/<job_id>`** opens that drawer with the **job editor** docked **next to** the list on desktop (**no** fullscreen scrim over the list). Encode **`job_id`** in the path segment when it contains special characters. The job row open in the editor uses the same **`session-item active`** highlight as **History** for the current chat. On **`max-width: 1199px`**, the **`.scheduler-dock-cluster`** matches **History** (**same `left` / `right` / `top` / `bottom` inset pattern**, full viewport height between insets). The jobs list alone fills that height. When the job editor is open, **`.scheduler-dock-cluster-editor-active`** hides the list and shows only the editor at **full cluster height** so it covers the list (**stacked overlay**, not a short bottom sheet). The cluster sits **above** the shared dim **`.backdrop`** (**`z-index: 70`**) so controls stay clickable while the drawer is open.
- **`#/history`** opens the **History** drawer alone. On **`min-width: 1200px`**, opening **History** while **Scheduler** is already open keeps both drawers by adding **`?history=1`** to the scheduler hash (example **`#/scheduler/jobs/<job_id>?history=1`**). Choosing another chat from the list keeps the drawer open by using **`#/s/<sessionId>?history=1`** while **History** stays visible. The main chat shell still uses the shared dim **backdrop** when a drawer is open.
- Deleting the **active** chat from **History** moves the shell to the **new chat** home (empty start screen), clears the session route, and **closes** the **History** drawer. Deleting **another** row removes it from the list and **keeps** the **History** drawer open; the URL and transcript stay on the chat that was on screen. After **`window.confirm`**, the client **briefly ignores** shell **backdrop** closes so a stray pointer event does not dismiss **History** or change the route. The row **trash** control calls **`stopPropagation`** on **`click`** before **`deleteSession`** so an **`async`** delete cannot bubble to the row and accidentally **`pickSession`** the deleted id.
- Field edits in the job editor **auto-save** with a short debounce (no separate **Save** button) without a footer status line. **`job_id`** is editable in the editor; changing it renames the on-disk job via PATCH. **Pause**, **Resume**, and **Delete** stay explicit.
- The URL still carries **one** primary route at a time for **`#/s/...`** vs **`#/scheduler...`** vs **`#/history`**; the optional **`history=1`** query only augments scheduler (or session) URLs for the dual-drawer desktop case.
- **`404`** from **`GET /coddy/scheduler/jobs`** means the server build has no scheduler HTTP surface; **`503`** means **`scheduler.enabled`** is false for that process. The drawer shows a plain-language line instead of crashing.
- The job **`body (markdown)`** field uses the shared **`MarkdownLineEditor`** (see **Markdown line editor** below). Gutter, active-line highlight, wrap-aware numbering, and content-driven height match the plan card markdown pane.

### Markdown line editor (shared)

Single implementation: **`MarkdownLineEditor`** in **`external/ui/src/ui/markdown/MarkdownLineEditor.tsx`**. Gutter math lives in **`external/ui/src/ui/markdown/markdownLineGutter.ts`**. Scheduler imports the same module via **`external/ui/src/ui/scheduler/MarkdownLineEditor.tsx`** (re-export only). Styles: **`.md-line-editor`** and **`.md-line-editor--plan`** in **`external/ui/src/styles.css`**.

**Consumers**

- Plan document card, markdown mode (**`PlanDocumentSection`**).
- Scheduler job editor **`body (markdown)`** (**`SchedulerJobEditorSheet`**).

**Layout**

- Horizontal flex: **gutter** (line numbers) + **stack** (highlight backdrop + **`textarea`**).
- Uses the **full width** of the parent. The **`textarea`** has **no vertical or horizontal scrollbar** (**`overflow: hidden`**, **`overflow-wrap: anywhere`**). **Height grows with content** (plus a **minimum logical row** count). When the surrounding UI needs scrolling (scheduler editor scroll region, long chat transcript), the **outer** container scrolls, not the inner editor.

**Line numbers**

- Numbers mark **logical** lines (split on `\n`).
- When a logical line **wraps** to multiple visual rows, only the **first** visual row shows a number; wrapped continuation rows keep an **empty** gutter cell at the same row height.
- When the document has fewer logical lines than **`minRows`**, pad the gutter with numbered blank rows up to **`minRows`** (scheduler default **10**, plan card **4**).

**Active line**

- The logical line that contains the caret is highlighted across **every** visual row it occupies: one **`md-line-editor-hl-band`** per visual row with **`is-current`** (semi-transparent background). The matching gutter number uses **`is-active`**.

**Measurement**

- A hidden probe (**`md-line-editor-measure`**, same font and wrap rules as the textarea) measures each logical line at the textarea **text width** (inside horizontal padding).
- Visual row count per logical line: **`ceil((measuredHeight - 1) / lineHeightPx)`** (see **`measureLineVisualRows`**). Gutter rows and highlight bands share the fixed band height **`--md-editor-line-px`** set on the editor root from computed line height.

**Typography**

- Default (scheduler): **12px**, line height **1.45** (**`--md-editor-fs`**, **`--md-editor-lh`**).
- Plan variant (**`.md-line-editor--plan`**): **13px**, line height **1.5**.

**Tests**

- **`external/ui/src/ui/markdown/MarkdownLineEditor.test.tsx`**
- **`external/ui/src/ui/markdown/markdownLineGutter.test.ts`**
- Plan card: **`external/ui/src/ui/chat/PlanDocumentSection.test.tsx`**

### Plan mode plan document card

**Component**: **`PlanDocumentSection`** (**`external/ui/src/ui/chat/PlanDocumentSection.tsx`**). Rendered from **`plan_document`** transcript items (**`MessageList`**).

**Collapsed**

- **Title**: frontmatter **`name`** or **`slug`**. **`title` attribute** (native tooltip) shows absolute file **`path`** when known (for example **`…/plans/<slug>.plan.md`**).
- **Description**: one line from **`overview`** or the first non-empty body line.
- **Footer**: **Discard** and **Run plan** stay visible; expand/collapse is on the header button only.

**Expanded**

- **Left accent**: **`box-shadow: inset 2px 0 0`** orange on **`.plan-document-card--expanded`** (not discarded).
- **Header**: title toggles expand; optional **Saving…** or save error after debounced PUT.
- **Body pane** (**.plan-document-pane**): one region for content. **Eye control** top-right (**`Toggle preview`**, **`aria-pressed`** when preview is on). **Default: Preview** ( **`Markdown`** on body text). Toggle switches to **Markdown** (**`MarkdownLineEditor`**, **`className="md-line-editor--plan"`**, **`minRows={4}`**, spellcheck enabled).
- **No fixed-height clip** on the pane: **preview** and **markdown** both **grow with content**; **no inner scrollbar** on **`.plan-document-pane-inner`**. The chat column scrolls when the card is tall.
- **Footer**: **Discard** (text link) and **Run plan** (orange, ▶ icon). **`min-width: 640px`**: CSS grid places **head** and **actions** on one row, **body** full width below; actions stack in a column on the right.

**Editing**

- The editor shows **markdown body only** (YAML frontmatter stripped via **`planEditorBody`** in **`planContent.ts`**). Autosave **`PUT /coddy/sessions/{id}/plans/{slug}`** with JSON **`{ "body": "…" }`**, about **600ms** debounce.

**Discard**

- **`DELETE`** marks the plan **`discarded`** in session state; the card **stays** in the transcript, muted (**`.plan-document-card--discarded`**), controls disabled. Server excludes discarded slugs from the plan-mode system prompt.

**Run plan**

- Starts implementation via session prompt metadata (see **`docs/acp-protocol.md`**, **Run plan**).

**Tests**

- **`external/ui/src/ui/chat/PlanDocumentSection.test.tsx`**

### Responsive breakpoints

- Below **`min-width: 1920px`**: rail width toggle hidden; History opens the same **drawer + backdrop** as on larger viewports.
- At **`min-width: 1920px`**: user may widen the rail (**arrow**). Sessions remain a **drawer overlay** whether the rail is narrow or wide (wide rail changes label density only, not session placement).
- Mobile (**top bar**) keeps compact rail only; drawer for sessions history.

### Sessions drawer placement (implementation contract)

- **Horizontal alignment**: The **left edge** of the drawer is **`rail-column` right edge + gutter** (~**`--nav-floating-gutter`**). Do **not** hardcode **`left`** in **`px`** for "wide navbar" guesses (wide **`fit-content`** width varies).
- **Measured track width**: SPA sets **`--rail-shell-track-width`** on **`.shell`** to **`rail-column.offsetWidth`** (ResizeObserver in **`NavRail`**) before computing drawer **`left`** and **`width`** so narrow and labeled-wide rails stay flush with **`--nav-floating-gutter`** after the nav column.
- **CSS fallback**: When the variable is not yet set inline, **`--rail-shell-track-width`** defaults on **`.shell`** to **`calc(var(--rail-pill-track) + var(--rail-column-pad-end))`**.

### Narrow-rail hover tooltips

- Shown **only** when the rail is **narrow** (no wide labels column). Labels visible in wide rail substitute for tooltips; do not show floating tip rows there.
- **Copy**: brand area **New Chat**, **History** nav control **History**, **Scheduler** nav control **Scheduler**, external links match their labels. Reference accent chrome in **`docs/assets/ref-navbar-narrow-tooltips-accent.png`**.
- **While a control owns an open overlay** (example **History** with the list visible and **`.is-active`** on the trigger), **hide that row's tooltip** even if the mouse still hovers (**nav stacking can sit above backdrop**). Same for **Scheduler** when its drawer is open.
- Tooltip **horizontal offset** must use the **same gutter math** as the History drawer (**column padding + nav floating gutter (+ border shim where needed)**), not a shorter offset from icon-only **`rail-tip-host`** width alone.

### Nav rail panel and wide layout (design contract)

- **Panel shape (desktop)**. The nav is a **tall rectangular column** along the **left viewport edge** with **rounding only on the right** (straight left edge flush with the browser). Avoid a centered **full-height capsule** that does not meet the edge.
- **Wide rail width**. Pill width is **content-driven** (**`fit-content`**) with a sensible **max-width** cap, not a legacy fixed pixel width guess.
- **Wide header brand**. **Coddy agent** is **one horizontal line** (**Coddy** + muted **agent**). Keep **breathing room to the right** of the label (**extra padding-right** on the brand control) so copy does not sit against the inner right edge of the panel.
- **Labeled rows (History, Scheduler)**. Rows **share the same width** within the column (stretch to the **widest** row). Each row is a **single interactive surface** (icon + label), not a small icon hit target plus detached text.
- **Icon column alignment**. The first grid track for row icons matches the **collapse** toggle footprint (**44px** wide control). **Horizontal padding** on row hits stays **balanced** (avoid oversized **padding-left** and cramped **padding-right** at the label end).
- **Collapse vs global menu**. The **stacked-lines** control **only** narrows the rail. It is **not** a global app navigation drawer (see Nav rail item 2 above).

### Nav rail icons (implementation contract)

- **Collapse (hamburger glyph)**. Use **three equal-length** horizontal lines (**no** shorter third line). Prefer a **compact symmetric** **`viewBox`** (for example **20×20**), **round** line caps, and stroke weight that reads at **18px** output size.
- **Expand (narrow rail at XL)**. Keep a **chevron / chevron-pair** style control that reads as **widen rail**, not a second burger menu.
- **Rendering**. Small rail SVGs should opt into **`shape-rendering` tuned for crisp curves** (for example **`geometricPrecision`**) and **`flex-shrink: 0`** so flex layout does not squash glyphs.
- **Regression art**. Use hover captures under **`docs/assets/`** when checking outline and press states.

Sessions search uses **`GET /coddy/sessions?q=...`** (**title or first persisted user message substring only**); list uses infinite scroll toward older pages.

Desktop navigation wider than Full HD optionally shows labels on the expanded rail (**cookie** remembers preference).

Sessions list interactions

- Session list supports open on row click.
- Session list shows a small trash icon on hover.
- Renaming is done only in the chat header.

## Components

### Repo links

Repo links appear as plain text links (**`GitHub | API docs`**) in a fixed footer at the bottom of the **start screen** (hero / empty state only — not shown once a chat is active). GitHub (**`https://github.com/coddy-project/coddy-agent`**) and **API docs** (**`/docs/`**) both open in a **new tab** (`target="_blank" rel="noopener"`). Implemented via **`.hero-footer`** (inside the `isEmpty` branch of `ChatScreen`) with `position: fixed; bottom: 16px` centered across the viewport.

### Tool timeline

Captured via SSE (**`tool_call`**, **`tool_call_update`**). Rendered like **thinking** and **memory**: a **`thinking-row`** foldout with **chevron**, **tool name**, and **duration** (**`thinking-dur`** in the summary row alongside the label).

The expanded body lists **arguments** (when known) and **result** (**raw** monospace, **no Markdown**, per the preview rules below).

- Tool arguments arrive via `tool_call_update` status `in_progress` where `content[0].content.text` is raw JSON args.
- Tool result arrives via `tool_call_update` status `completed` or `failed` where `content[0].content.text` matches the HTTP user preview rules (**raw** text, **no Markdown**): the first **19** content lines, then a twentieth row that is only **`...`**, when the output is longer; **`_meta.coddy.toolResultPreview`** marks truncation. Outputs that are not truncated skip the fixed-height viewport and **Load more** (natural-height grey mono panel). When truncated, the fixed-height panel shows the clipped preview with **no vertical scrollbar**. A text link **Load more results** (not a filled button) performs **GET `/coddy/sessions/{id}/tool-calls/{toolCallId}`** once, fills the same panel with the full saved body at the same max height, enables **overflow-y** scrolling, and turns the link into **Hide**. **Hide** restores the clipped preview without another request while the full text stays in memory for this session.

#### Tool card UI (bundled SPA, current)

Implementation lives in **`external/ui/src/ui/messages/ToolCallMessage.tsx`**.

- **Layout** - Outer **`thinking-row coddy-tool-call-row`**; **`details`** uses **`thinking-details coddy-tool-details`**. **`summary.thinking-summary`** with **`thinking-left`** ( **`aria-label="Tool summary"`** ): **`thinking-chevron`**, **`thinking-label`** (tool title or kind, **`...`** suffix while **`pending`** / **`in_progress`** ), **`thinking-dur`** (**finished** durations from **`meta.json`**, **live elapsed** while in flight when **`startedAtMs`** is set, placeholder **`-`** when unknown). Transcript stacking uses **`messages-inner` `gap`** like other **`thinking-row`** blocks (avoid tool-only asymmetric margins). Expanded body **`thinking-body coddy-tool-call-body`** wraps **`pre.tool-block`** arguments (pretty-printed JSON when parseable) and **`div.tool-block.tool-result.tool-result-raw`** with **`pre.tool-result-pre`** for output (**always raw plaintext**, never the Markdown renderer).
- **Viewport** - Truncated runs ( **`resultWasTruncated`** from list / SSE) also add **`tool-result-viewport tool-result-viewport--tall`**, clipped with **`tool-result-viewport--clip`** or scrollable with **`tool-result-viewport--scroll`** after **Load more**. Short non-truncated runs omit **`--tall`** so height follows content (**no fake tall box**, **no Load more row**).
- **Controls** - **Load more results** (**`data-testid="tool-result-more-link"`** ) and **Hide** (**`data-testid="tool-result-hide-link"`** ) are styled as text links (**`tool-result-text-link`**), in **`tool-result-toggle-row`**, under the result panel.
- **Full body** - The SPA obtains the saved full string only via **GET `/coddy/sessions/{sessionId}/tool-calls/{toolCallId}`** (JSON **`result`** ). **`App.tsx`** wires **`onFetchToolCallFull`** to that endpoint and merges **`fullResultText`** into transcript state (**`external/ui/src/ui/App.tsx`** ).

Tool call history is persisted per session under `tool_calls/` so it can be restored after restart.

### Transcript message types (technical)

Assistant messages keep the footer row **inside** the same padded box as the prose so the copy control shares the transcript inset. **User** copy and time sit **below** the grey bubble (outside the bubble contour), still **bottom-right** under that bubble. **Copy message** is a **bare icon** (no filled tile on hover); hover uses **link-violet** tint like markdown anchors; the browser **`title`** tooltip shows **Copy message** after a short hover like other native hints. Raw persisted text is copied on click. Timestamps use **`created_at`** (RFC3339 UTC): visible label is **local hour and minutes** only; hovering shows full calendar date, seconds, and timezone offset in the native **`title`** tooltip. Assistant prose uses the full transcript column width.

The transcript UI is a flat list of message blocks (no nested threads). The runtime list lives in `external/ui/src/ui/chat/types.ts`.

Current block types:

- `user_message`
  - Raw user input text (**plain**, **`pre-wrap`**; no Markdown or transcript skill chips).
- `thinking`
  - Streaming model reasoning deltas (`delta.reasoning_content`) rendered as a disclosure row.
  - `thinking...` while in progress, `thinking` when completed.
  - **Summary row layout** - elapsed time stays immediately beside the **thinking** word, not pushed to the far right of the chat column. In **`ThinkingMessage.tsx`**, **`.thinking-dur`** nests inside **`.thinking-left`**; **`external/ui/src/styles.css`** sets **`.thinking-left { gap: 0 5px; }`** between the label and the timer. Do not use **`justify-content: space-between`** on **`summary.thinking-summary`** for that spacing. Avoid a wide summary flex that sends the timer to the opposite edge of the transcript.
  - Multiple `thinking` blocks can appear in one user turn. If the model resumes reasoning after tool calls, the UI starts a new `thinking` block and preserves ordering.
- `tool_call`
  - Tool execution timeline block (SSE `tool_call` and `tool_call_update`, enriched from `/coddy/sessions/{id}/tool-calls`).
  - Summary row matches **thinking** (**chevron**, **tool name**, **duration** beside the label). Details show args and tool **result**. Results are **raw plain text** in a monospace, muted grey panel (**no Markdown**). When **`resultWasTruncated`** is false (output fits the preview cap), the result block grows with content only (no fixed tall viewport, no **Load more results**). When truncated, the capped viewport and **Load more results** / **Hide** match the tool timeline above (REST fetch only on first **Load more results**).
  - Duration label is computed from persisted `tool_calls/<id>/meta.json` `startedAt` and `finishedAt` when available, with live **`startedAtMs`** updates while **`in_progress`**.
- `assistant_message`
  - Final assistant output for the turn. UI keeps it last and reconciles it from **`GET /coddy/sessions/{id}/messages`** when streaming ends or after a refetch. After **Stop** mid-stream, that **`GET`** can lag the partial row already on screen; **`mergeTranscriptPreferLocalSuffix`** (see **Multi-session streaming and Stop** above) preserves visible text until the server catches up.

Ordering rules:

- `thinking` blocks appear wherever reasoning arrives in the stream.
- `tool_call` blocks appear where tool events arrive.
- Final `assistant_message` is appended after tools and any subsequent `thinking` blocks.

### Composer pill

Muted **Auto** pill tracks future modality toggles; UI copy stays English everywhere.

### Composer context meter

Ring to the **left** of **Send** in **`Composer.tsx`**. Implemented by **`ContextUsageRing`**: inner stroke always visible; outer progress arc only when usage **> 0**, flat color (no gradient or shadow), fill from **12 o'clock** clockwise. Colors use **`--coddy-context-ring-inner`** and **`--coddy-context-ring-fg`** (both themes in **`styles.css`**). Dark outer arc **`#f5f3ff`** (logo stroke); light outer arc **`var(--accent)`**.

- Do **not** put a percent label **on** the ring. Percentages and counters belong **only** in the tooltip (**`rail-tip`** family), above the ring, centered, wide enough via **`composer-context-tip`** CSS.
- Idle home (**`contextIdle`**): inner ring only (no outer arc); tooltip **`No context usage yet`** plus **`Max context …`** only (no **`Model …`** line).
- Active session: arc fills from stats; the tooltip may include usage lines but **never** a **`Model …`** line that duplicates **Mode** (the mode dropdown).
- **Click** (or **Enter** / **Space** when focused) on **`.composer-context-tip-host`** opens **`ContextBreakdownPopover`** (**`data-testid="context-breakdown-popover"`**): summary percent, stacked bar, legend (**System prompt**, **Tool definitions**, **Rules**, **Skills**, **MCP**, **Conversation**). **Escape** or **Close** dismisses; hover tooltip returns when closed. Data from **`GET /coddy/sessions/{id}/stats`** field **`contextBreakdown`** (estimated tokens per category).

See **`.cursor/rules/ui-spa.mdc`** for the full wording.

### Composer primary action (**Send** **/** **Stop**)

- Control **`#btn-send`** (**`.composer-icon`**) sits **directly right** of the context ring (**`.composer-context-tip-host`**).
- **Circular button** (**not** pill or squircle): fixed equal **width** and **height**, **`border-radius: 50%`**, **`box-sizing: border-box`**. Intended diameter **42px** in production CSS (**may track token scale**, but stays a **circle**).
- **Glyphs** live in **`composer-send-glyph`**. **Play** state uses **`~22px`** **▶**; **stop** uses **`.composer-stop-square`** (**14×14px** filled block, centered in the circle). Ring + stop stay **right-aligned** in **`composer-bar-actions`** (same row as mode tabs). Keep contrast high (**`composer-send-play`** vs **`composer-send-stop`**).
- Idle **disabled** when message field empty; streaming shows **stop** affordance (see **`docs/ui.md`**, section **Composer primary action**).

Composer mode selector

- **`GET /v1/models`** merges Coddy profiles and YAML backends in one list. Split by **`owned_by`**: **`coddy`** means session profiles **`agent`** and **`plan`** only. Any other **`owned_by`** marks a configured **`models[].model`** row (YAML backend).
- **`Mode`** lists only **`agent`** and **`plan`**. Default is **`agent`**. **`plan`** uses the orange outline treatment.
- Selected mode is sent as top-level **`model`** in **`POST /v1/responses`**.

Composer YAML **`models[].model`** selector

- **`Model`** sits immediately next to **`Mode`** in **`Composer.tsx`**. It lists only YAML backend rows (**`owned_by`** is not **`coddy`**). Opens **down** on the empty start screen (**`isEmpty`**) and **up** when docked over an active chat (same **`opens-down`** / **`opens-up`** convention as **`Mode`**).
- Default for **new chat** follows cookie **`coddy_llm_model`** when valid, else **`default_agent_model`** from **`GET /v1/models`**, else the first YAML row (**`Path=/`**, long **`Max-Age`** on the cookie).
- Opening an existing session restores **Model** from **`GET /coddy/sessions/{id}/messages`** field **`model`** (per-session override on disk), not from the cookie. Changing **Model** updates the cookie (default for the next **New chat**) and **`PATCH`** **`selectedModelId`** on the active session.
- For ReAct (**`agent`** / **`plan`**), the UI sends **`metadata.model`** with the selected YAML **`id`**; the context-meter **`max_context_tokens`** for the ring follows that YAML row.
- **Long model lists** — backend ids are **`vendor/model`**. When more than one vendor is present the menu groups rows under an uppercase **vendor header** (**`mode-menu-group-label`**) and rows show only the model name (the full id stays in the row **`title`**). On desktop the list is capped to roughly **5 rows** and scrolls (**`mode-menu-scroll`**, **`max-height: min(175px, 50vh)`**). When there are **more than 5** backends a **filter input** (**`mode-menu-filter`**, auto-focused) appears pinned above the scroll; it matches the query against the vendor, the model name, or the full id (case-insensitive). **Enter** selects the first match, **Escape** closes the menu, and an empty result shows a “No models match …” notice. The desktop menu width is constrained (**`mode-menu--llm`**). Helper logic lives in **`chat/llmModelMenu.ts`**.
- **Mobile sheet** — on narrow/mobile shells (**`isMobileShell`**, the **`max-width: 1199px`** shell-stack breakpoint) the **`Mode`** / **`Model`** / **`Reasoning`** menus render as a **full-width bottom sheet** (**`mode-menu--sheet`**), the same family as the slash / **`@`** picker sheet, over a dimmed scrim (**`mode-menu-backdrop--scrim`**) instead of the cramped anchored dropdown. The sheet drops the desktop width cap (full width up to **560px**, centered) and gives the scroll a taller **`46vh`** cap. Desktop keeps the anchored **`mode-menu--portal`** dropdown.
- **`Reasoning`** sits immediately next to **`Model`**, shown **only** when the active model row exposes a non-empty **`reasoning_levels`** in **`GET /v1/models`** (reasoning models). Same dropdown styling and **`opens-down`** / **`opens-up`** convention as **`Mode`** / **`Model`**; the button label is the current level capitalized (e.g. **`High`**), default label **`Reasoning`**.
- Default for **new chat** follows cookie **`coddy_llm_reasoning`** when valid for the model, else the model's **`reasoning_default`**, else **`medium`** (or the first offered level). Opening a session restores it from **`GET /coddy/sessions/{id}/messages`** field **`selectedReasoning`**. Switching **Model** clamps the level to one the new model offers. Changing it updates the cookie and **`PATCH`** **`selectedReasoning`**; ReAct turns also send **`metadata.reasoning`**.

Composer does not show tools toggles in this milestone.

### Slash commands picker (skills)

When the caret sits on the current composer line on a **`/`** that is **line-start or preceded by whitespace**, with optional `[a-zA-Z0-9_-]*` typed after it, and outside Markdown fences or blockquotes, the UI loads **`GET /coddy/slash-commands`** with a **100ms** debounce, required **`page=1`** and **`page_size=30`**, and optional **`prefix`** from typed characters after **`/`** (works mid-line, for example `say /foo`). Menu open/close rules match **`slashMenuDraftAtCaret`** in **`external/ui/src/ui/skills/draftSlash.ts`**.

- **Automation** uses **`data-testid="slash-command-menu"`**, per-row **`data-testid={`slash-command-row-${name}`}`**, and **`data-testid="slash-command-more"`** for paging.
- **Desktop** (**`slash-menu--floating`**) attaches above the textarea inside **`composer-card`**. **`Mobile`** (**narrow width**, match roughly **`max-width: 720px`**) renders a dimming backdrop (**`slash-sheet-backdrop`**) plus a bottom sheet (**`slash-menu--sheet`**).
- Choosing a row replaces the typed **`/`…** segment with **`/<name> `** (plain **`#composer`** value and wire text to **`POST /v1/responses`**). The UI **never** stores **`[/<name>](coddy-skill:<name>)`** in the composer draft.
- While the draft is non-empty, **`Composer`** draws a **mirror layer** (see **Caret sync** below) and highlights slash tokens parsed by **`segmentComposerSlashSpans`** (**`external/ui/src/ui/skills/segmentComposerSlashSpans.ts`**) with **`span.composer-skill-chip-inline`** (**`data-testid="composer-skill-chip"`**).
- **`user_message`** bubbles show persisted text as-is (**`UserMessage`**, **`msg-user-body`**, **`white-space: pre-wrap`**). Skill mirror chips apply only in **`#composer`**, not in the transcript.
- **`Escape`** closes the menu; **`Enter`** confirms the first row when results are loaded and the menu is open (same turn as **`/`** autocomplete).

#### Composer mirror and caret sync (contract)

The textarea uses **transparent** glyphs when the draft is non-empty; the user-visible line is the **mirror** (`.composer-mirror-inner`) that must be **pixel-aligned** with **`#composer`** for the same string. The **caret** position is computed **only** by the textarea engine on the raw characters, so any styling in the mirror that **changes horizontal advance** of the same code points **breaks** perceived caret placement.

Rules for **`.composer-skill-chip-inline`** (composer only):

- **MUST** use the **same effective font metrics** as **`#composer`**: **`font-family`**, **`font-size`**, **`line-height`**, **`font-weight`**, **`letter-spacing`**, **`font-style`** inherited or explicitly matched (current gate: **400** weight on both mirror and textarea in **`styles.css`**).
- **MUST NOT** add **horizontal** **`padding`**, **`margin`**, or a **`border`** that participates in the inline box width. Use **`box-shadow: 0 0 0 1px …`** for a ring and **`padding: 0`**, **`margin: 0`** so chip width tracks the underlying `/name` glyphs.
- **MUST** keep **`scrollbar-gutter: stable`** on **`#composer`** and mirror **`padding-right`** adjusted for scrollbar width (**`ResizeObserver`** in **`Composer.tsx`**) so wrapped lines do not drift.

Transcript chips (**.md .coddy-skill-chip**) are **not** bound by this contract; they may use monospace, heavier weight, and pill padding because they are not paired with a transparent textarea.

Full browser checks against a running **`coddy http`** instance (including a **mobile viewport**) use **Playwright MCP** in Cursor, Codex or any other code agent you use. This repository does not ship **`@playwright/test`** as an npm dependency.

**Frosted glass (Playwright MCP smoke)** - after **`npm run dev`** under **`external/ui/`** (or **`coddy http`** with **`make build TAGS="http ui"`**), use **`browser_tabs` / `browser_navigate`** to the SPA, then **`browser_evaluate`** **`getComputedStyle(...).backdropFilter`** and **`.backgroundColor`** on:

| Target | **`backdrop-filter`** | **`backgroundColor`** (example) |
| --- | --- | --- |
| **`.composer-card`** | **`blur(…) saturate(…)`** from **`--coddy-glass-panel-backdrop`** | tinted rgba from **`--coddy-glass-panel-bg`** |
| **`.sessions.drawer`** (open **History**) | same as composer row | same |
| **`.mode-menu`** (open **Mode**) | same | same |
| **`.slash-menu-surface`** (inside **`data-testid="slash-command-menu"`**) | same | same; scroll **`slash-menu-scroll`** only (**`slash-menu-surface`** carries blur). On **desktop** (viewport **`> 720px`**) the menu root classes include **`slash-menu--portal`** and the node renders under **`document.body`** so **`backdrop-filter`** sees chat behind the composer. The mobile bottom sheet stays inside **`composer-card`**. **`--coddy-z-slash-command`** keeps slash UI stacking **below** History **`backdrop`** and **`sessions.drawer`**. |
| **`.backdrop`** (History open) | **`none`** | dim from **`--coddy-overlay-scrim-bg`** only |
| **`.slash-sheet-backdrop`** (slash sheet on **narrow** viewport, **`max-width: 720px`**) | **`none`** | dim only |

Docked chat (transcript visible) uses the same **`.composer-card`** rule as the hero composer.

**`.messages-inner`** uses **`padding: 0`** so bubbles line up with **`#composer`** horizontal inset (composer card still spans the full **`max-width: 920px`** track).

**Corner radius** for composer, History drawer, **`slash-menu-surface`**, **`mode-menu`**, and the bottom sheet chrome uses **`--coddy-glass-panel-radius`** (**`18px`**) so skills dropdown reads as the same family as composer and History.

#### Slash skills verification use cases

Use these to regress behaviour after CSS or **`Composer`** edits. **Vitest** rows are under **`external/ui/src`**.

| ID | Scenario | Expected | Automated check |
| --- | --- | --- | --- |
| UC1 | Type `asdfasf /find-skills asdfasdf` in **`#composer`** | One mirror chip **`/find-skills`**, **`textarea.value`** exactly that plain string (no markdown) | **`external/ui/src/ui/chat/Composer.test.tsx`** · `composer highlights plain slash token as chip while editing` |
| UC2 | Open slash menu mid-line | Menu draft open; **`prefix`** from chars after **`/`** | **`external/ui/src/ui/skills/draftSlash.test.ts`** · `slashMenuDraftAtCaret works after whitespace mid-line` |
| UC3 | Token **`x/foo`** | Whole token plain text slice (no chip for **`/foo`**) | **`external/ui/src/ui/skills/segmentComposerSlashSpans.test.ts`** · `segmentComposerSlashSpans skips letter before slash` |
| UC4 | Line-leading **`/foo`** | Single **`slash`** segment **`/foo`** | **`segmentComposerSlashSpans.test.ts`** · `segmentComposerSlashSpans line start slash` |
| UC5 | Strip legacy **`a [/demo](coddy-skill:demo) b`** | Output **`a /demo b`** | **`segmentComposerSlashSpans.test.ts`** · `stripCoddySkillMarkdownLinks restores plain slash token` |
| UC6 | User bubble **`hi /demo there`** | Plain text, no **`coddy-skill-span`** | **`external/ui/src/ui/messages/UserMessage.test.tsx`** |
| UC7 | Multiline YAML in user bubble | Line breaks preserved in **`user-message-body`** | **`external/ui/src/ui/messages/UserMessage.test.tsx`** |
| UC7b | Display-only slug transform (composer / legacy helpers) | Plain **`/`** → autolink form in **`slugSlashesForUserBubbleMarkdown`** unit tests only | **`segmentComposerSlashSpans.test.ts`** |
| UC8 | Live **`coddy http`** after **`make build TAGS="http ui"`**, **`#composer`** with **`/coddy_slash_demo`** | **`textarea.value`** plain; **`fontFamily`** chip **===** **`#composer`**; EOL **`selectionStart === value.length`** | **Playwright MCP** · **`browser_navigate`**, **`browser_fill_form`**, **`browser_evaluate`** |

### Markdown

**Assistant** messages may contain Markdown. **User** bubbles do not (plain **`pre-wrap`** text).

- **`coddy-skill:`** chips appear only in the **composer mirror** while editing, not in persisted user bubbles.
- Render fenced code blocks with syntax highlighting.
- Each code block has a copy button in the top right corner that copies only the block contents.

### Memory tree (deferred explorer)

A file-tree over combined **global** (**`memory.dir`** / `$CODDY_HOME/memory`) and **workspace** (`<cwd>/memory`) remains out of scope for this milestone.

### Memory copilot transcript

When **`memory.enabled`** is true, each user turn can show a **`memory`** grey foldout styled like **thinking** (`thinking-row` / `thinking-details` / `thinking-body`), placed **after** that user bubble and **before** the main assistant stream for the same turn. Expanded content has **Recalled** and **Memorized** subheads, optional streamed reasoning and answer text, duration in the summary row, and **`data-testid="memory-copilot-row"`** for automation.

### Component boundaries

The UI should be implemented as small React components with folder-enforced hierarchy.

- `ui/layout/Shell`
- `ui/nav/NavRail`
- `ui/sessions/SessionsSidebar`
- `ui/chat/ChatScreen`
- `ui/messages/MessageList`
  - `ui/messages/UserMessage`
  - `ui/messages/AssistantMessage`
  - `ui/messages/ThinkingMessage`
  - `ui/messages/MemoryCopilotMessage`
  - `ui/messages/ToolCallMessage`
- `ui/chat/PlanDocumentSection`
- `ui/markdown/MarkdownLineEditor`

### Session overflow menu (`…`)

Opens lightweight rename/delete UX (prompt-first until richer modals arrive).

## States

- Idle composer: bordered textarea.
- Streaming assistant: progressively grows final assistant bubble; token HUD updates concurrently.
- Error streaming: surfaced inside assistant transcript with HTTP status text fallback.

## Non-goals for this milestone

Server-side SSR routes per session, BFF auth, CDN-hosted Swagger, and editing **`agentMemory`** via REST remain out-of-scope (`session.json` slot remains agent-managed).

## Dev workflow

To iterate on UI without rebuilding the Go binary:

Backend:

```bash
make build TAGS=http
./build/coddy http --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

Frontend:

```bash
npm --prefix external/ui install
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```

