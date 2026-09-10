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

### Localization and language

- **Picker:** one native language select **directly under the theme swatch grid** in **Settings → Appearance** (**`AppearanceLanguagePicker`**, **`data-testid="appearance-language-select"`**). It renders **Auto** followed by every locale in **`UI_LOCALE_IDS`**; the current registry supplies **English** and **Русский**. The select fills the available settings width and remains usable on narrow shells. It lives inside `.appearance-sheet-body`, so it inherits the centered placeholder guard covered by `appearanceSettingsLayout.test.tsx`.
- **Persistence:** cookie **`coddy_ui_lang`** stores a registered locale id (currently `en` or `ru`), with the same lifetime/flags as **`coddy_ui_theme`** (path `/`, `SameSite=Lax`, `; Secure` on https, 1-year `Max-Age`). **Auto** stores no cookie and resolves from **`navigator.language`** on each load; the cookie is cleared when Auto is chosen. Purely client-side — no config save (matches the Appearance tab convention).
- **Bootstrap:** **`main.tsx`** calls **`initLocale(bootstrapUiLocaleFromUrlOrCookie())`** before React mounts; resolution order is **`?lang=<registered-id>`** (URL, also persisted to the cookie) > cookie > **`navigator.language`**. Sets **`document.documentElement.lang`**.
- **i18n engine:** **`external/ui/src/ui/i18n/`** — `translate(key, params)` / `t`, a locale store with `useSyncExternalStore` subscriptions, and **`I18nProvider`** + **`useT()`**. **`locales.ts`** is the single registry for supported locale ids, picker label keys, and dictionaries; **`main.tsx`** wraps the app and shared confirmation provider in **`I18nProvider`**. Dictionaries use dot-notation keys grouped by area. **`useT()` works without a provider** (falls back to `translate`), so components render in tests without wrapping; default-English values match the former hardcoded literals exactly so existing English-asserting tests keep passing.
- **Dictionary contract:** every registered dictionary must contain the same keys and interpolation tokens as the default locale. Add or change the key in every dictionary in the same patch; `messagesParity.test.ts` validates the contract and prevents a newly registered locale from silently drifting.
- **Counted copy:** anything that reads "N things" goes through `translatePlural` / `tp(key, count)`, not a `count === 1` ternary in the component. The dictionary stores one entry per CLDR category under `key.category` (`tasks.chip.running.one` / `.other` in English; `.one` / `.few` / `.many` / `.other` in Russian), the entry always receives `{count}`, and each locale supplies exactly the categories `Intl.PluralRules` says it can produce — the parity test derives the expected set from that, so Russian declensions cannot silently regress to a single form.
- **Reactivity:** the controlled select value is driven by local `choice` state (cookie-derived), because picking **Auto** can resolve to the already-active locale (no locale-store notification); label translation re-renders via the locale store subscription.
- **Coverage:** Appearance + Settings surfaces are translated (Settings shell, sections, MCP, Skills, CodexAuth, ModelField/Picker, Combobox), the schema-driven settings fields are translated as well (labels and descriptions of providers, models, agent, tools, subagents, memory, compaction, and the System group, resolved by section id and field path in `settings/schemaI18n.ts`, falling back to the schema's own text), and the conversation surfaces are translated too: nav rail, hero title, composer (modes, model picker, attachments, slash/@ menus, environment and folder modals), message rendering (thinking, tool calls, memory, compaction, copy controls), permission and question prompts, plan document card, History sidebar, scheduler drawer and job editor, background tasks panel, the env health banner, and the swarm screen with its topology graph. Shared destructive confirmations for drafts, chats, and scheduler jobs are translated as well.

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

1. **Nav rail**: **History** opens the session list overlay; **Scheduler** opens the cron jobs drawer (requires **`coddy serve`** built with **`http,scheduler`** and scheduler enabled). Background tasks are **not** in the rail: they belong to one chat, so their opener sits at the end of that chat's transcript (see **Background tasks panel**). Brand goes to the empty start screen. **Brand is text only** (**Coddy** plus **agent**), **no** circle or logo mark before the label, even if a reference mockup shows one. Optional **narrow vs wide rail** (**icons only vs icons plus labels**) on viewports **`min-width: 1920px`**, persisted in **`coddy_nav_rail`** cookie (**`narrow`** default). **Brand**, **History**, **Scheduler**, and **Settings** use real **`href`** fragment targets so **middle-click** or **Ctrl/Cmd-click** opens a **new tab** on the same origin for parallel chats. GitHub and API docs links are **not** in the rail — they appear only in the **start-screen footer** (see Repo links below).
2. **Session list**: **always a drawer overlay** with a dimming backdrop. It must **not** consume a second grid column or shrink the chat canvas (no inline sessions column beside the rail at any breakpoint). **Panel chrome title copy is History** (not "Chats"). There is **no** global hamburger that opens a separate app menu; the **stacked-lines control** in the wide rail header **only** collapses the rail to the narrow (icons-only) layout, matching the references. Each row is a real **`href`** to **`#/s/<sessionId>`** so **middle-click** opens that chat in a **new tab**.
3. **Chat canvas**: on **`min-width: 1200px`**, editable title and transcript share **`#messages`** with **`overflow-y: auto`**, and **`.chat-bottom`** is **`position: absolute`** with **`--coddy-chat-scrollbar-gutter`** padding so the composer does not cover the scrollbar track. The vignette above the docked composer remains a local transcript shield; in the light theme it blends toward **`--coddy-canvas-gradient-bottom`** instead of using the dark-theme black tint. The sticky title uses **`.chat-title-column`** (**`max-width: 920px`**, centered) so the title bar matches the composer stripe. The pinned title region (**`.chat-scroll-sticky-head`**) carries an **opaque** canvas backing (**`background-color: var(--coddy-canvas-gradient-top)`**) so the title stays fixed at the top of the scrollport while transcript text scrolls **behind** it — no see-through bleed. The shield matches the canvas top color in every theme, so it reads as the title card floating over the page rather than a separate bar. On **`max-width: 1199px`** (phones, tablets, and smaller desktops), **`body`** scrolls (native scrollbar); **`.rail-column`** (top bar with brand and links) is **`position: fixed`** to the **viewport top** (**`.shell-main`** gets **`padding-top: var(--coddy-mobile-top-inset)`** so content clears it). The chat title row (**`.chat-scroll-sticky-head`**) is **`position: sticky`** with **`top: var(--coddy-mobile-title-sticky-top)`** (**`--coddy-mobile-top-inset` plus `--coddy-mobile-chat-stack-gap`**, same **12px** token as title **`padding-bottom`**) so spacing under the rail matches title-to-first-message rhythm. Only **`.rail-pill`** is frosted. In active chat, **`.chat-bottom`** is **`position: fixed`** to the viewport bottom so the composer stays on screen while **`chat-scroll-tail`** reserves space, **`ChatScreen`** uses **`window`** for stick-to-bottom, and the skills slash menu uses the same **`slash-menu--portal`** path as desktop (**`createPortal`**).

The right insights rail is removed for the current milestone.

### Settings drawer (tabbed master–detail)

The **Settings** drawer (**`#/settings`**, **`Settings.tsx`**) is **tabbed**, not one long sheet. Tabs are derived from the live config JSON Schema (**`deriveSettingsSections`** over **`/coddy/config/schema`**): each top-level property is a tab (label from its **`title`**), the rarely edited tail (**`scheduler`**, **`prompts`**, **`instructions`**, **`logger`**, **`sessions`**, **`gateways`**) folds into a single **System** tab, and a client-side **Appearance** tab is placed **first** (the default tab). There is no separate Appearance/Skills flyout or **`settings-dock-cluster`** side panel — both are tabs now.

- **Saving**: there is **no per-section save button**. The drawer footer keeps only **Reload** (**`data-testid="settings-reload"`**) and **Save all** (**`data-testid="settings-save"`**, PUTs the whole config after `/coddy/config/validate`). **Appearance** is client-side only and never touches the config PUT.
- **Layout**: on **`min-width: 1200px`** the drawer is wider (**`min(960px, …)`**) with a vertical section rail on the left (**`.settings-nav`**) and the content panel on the right (**`.settings-tabs-layout`** is `display:flex; row`). On **`max-width: 1199px`** (same breakpoint as the rest of the shell, via **`isMobileShell`** / **`shellBreakpoint.ts`**) the nav is a **2-wide tile grid** master–detail (**`SettingsTileGrid`**, **`.settings-tile-grid`**): each tile (**`data-testid="settings-tile-<id>"`**) shows the section **title** on top and a short 3–5 word **description** below (**`SECTION_DESCRIPTIONS`** in **`settingsSections.ts`**) styled after the Scheduler job rows. The description clamps to two lines with an ellipsis (**`-webkit-line-clamp: 2`**) and the full text shows in a native **`title`** tooltip on hover. Tapping a tile opens that section with a **← Back to sections** control (**`.settings-mobile-back`**); the desktop rail (**`SettingsNav`**) is unchanged.
- **List sections** (**LLM Providers**, **Logical Models**) are **master–detail** (**`SettingsArraySection`**): a list of named buttons (labelled by **`name`** / **`model`**) with **Add**/**Remove**; **Add** or selecting a row hides the list and shows the item form, with **← Back to list**.
- **Codex provider authentication**: when a provider row has **`type: codex`**, **`api_base`**, **`api_key`**, and **`api_key_command`** controls are replaced by **Sign In with ChatGPT**. The device-flow card shows the one-time code, a link to the official verification page, waiting/completed/error state, and **Sign Out** for Coddy-managed credentials. The token is server-side only.
- **Logical Models** model field (**`ModelField`**) fetches the chosen provider's models from **`GET /coddy/providers/{name}/models`** for a pick-list, always with a manual-entry fallback. **ReAct Agent** and **Long-term Memory** default-model fields (**`ModelPicker`**) pick from configured logical models or accept manual entry.
- **Logical Models** reasoning-levels field (**`ReasoningLevelsField`**, rendered through **`SchemaForm`**’s **`fieldOverride`** hook on path **`reasoning_levels`**) owns the three states that key can be in and names the current one in a status line (**`data-testid="reasoning-levels-status"`**): **key absent** = levels auto-detected from the model id, **`[]`** = the composer reasoning selector is hidden for this model, **non-empty** = exactly these levels. An action row above the list carries **Fetch reasoning levels** (**`data-testid="reasoning-levels-fetch"`**, **`GET /coddy/config/reasoning-levels?model=&provider_type=`** for the id currently in the form and the type of the provider row it points at in the unsaved document, so it works before either entry is saved) and, whenever the key is present, **Use auto-detected** (**`data-testid="reasoning-levels-auto"`**) which drops the key again. A fetch that detects nothing leaves the field untouched rather than writing **`[]`**, because that would hide the selector instead of restoring detection. The status line always describes the list on screen first (override / hidden) and only falls back to fetch feedback (nothing detected / error) while the key is absent; retyping the model id or editing the list by hand clears that feedback and abandons the request in flight, so an answer that lands after the id changed, after a manual edit, or after the row was removed is dropped; an answer that does land goes through the newest **`onChange`** the form handed the field, never the one captured when the request started, so sibling fields edited meanwhile keep their values.
- **Skills** tab (**`SkillsSection`**) combines the schema-driven **`skills.dirs`** editor, a **config-backed remote-sources editor**, and the installed-skills list. Remote sources are rendered by **`SchemaForm`**'s **`fieldOverride`** hook (path **`sources`**) as **`SourcesEditor`**: each row is a source input with a per-marketplace **Sync** icon (**`POST /coddy/skills/sync?source=`**) and a trash **Remove**; the footer has **Add** (left) and **Sync all** (right, **`POST /coddy/skills/sync`**). Editing the rows mutates **`skills.sources`** in the config form (persisted on **Save**, since **`SkillsJSON`** round-trips **`sources`**). Each installed-skill row shows its **version** (**`.skills-list-item-version`**) and a **`remote`** badge when synced; an **iOS-style enable switch** (**`.skill-switch`**, theme-tinted via **`var(--accent)`** when on) toggles via **`/coddy/skills/{name}/enable|disable`**; a **Delete** icon (**`IconTrash`**) calls **`DELETE /coddy/skills/{name}`** and is **disabled for bundled read-only skills** (row **`readonly`** flag). When a skill is behind, a **Download-update** icon (**`IconDownload`**, tooltip naming the target version) appears and calls **`POST /coddy/skills/{name}/update`**.
  - **Sizing / alignment**: inputs inside an array row (**`skills.dirs`** and the sources editor) use **`box-sizing: border-box; min-height: 40px`** (**`.settings-array-row .settings-input`**) so they match the **40px** **`.settings-btn-icon`** buttons beside them and their bottoms line up; the source input fills its field (**`.settings-array-row-field > .settings-input { width: 100% }`**). Trailing controls in a skill row never shrink (**`.skills-list-item > button { flex: 0 0 auto }`**), so **Update / switch / Delete** keep a uniform **40px** regardless of description length. The enable switch is **38×22** with a 16px thumb (**`.skill-switch`**, the same control **`SwitchField`** wraps below).
  - **Sync feedback**: a successful sync flashes a checkmark on the button itself for **~1.6s** (**`is-synced`** class, tinted **`var(--accent)`**) — **Sync all** shows **Completed**, a per-marketplace **Sync** swaps its glyph to **`IconCheck`** — instead of a separate status line.
  - **Scroll stability**: enable/disable and sync refresh the installed list **without unmounting it**. The **Loading…** placeholder renders **only** on the initial empty load (**`loadInstalled(firstLoad)`** gates the **`loading`** flag), so a refresh never collapses list height and the Settings scroll position is preserved across a toggle.
  - **Installed skills box**: the installed list sits inside a **`.settings-fieldset`** (legend **Installed skills**) so it matches the framed sections above; it also sets **`min-inline-size: 0`** (**`.skills-installed-box`**) because a **`<fieldset>`** defaults to **`min-content`** inline size and the **`white-space: nowrap`** skill descriptions would otherwise widen the box past the panel and add a horizontal scrollbar. At its top, an **install control** (**`.skills-install`**, **`position: relative`** anchor) is a filter input (**`.skills-install-input`**, 40px, full width) that lazily loads **`GET /coddy/skills/available`** on focus and, while a query is typed, shows a **floating** dropdown (**`.skills-install-results`**, **`position: absolute`** with the shared **`--coddy-glass-panel-*`** tokens) of matching **not-installed** marketplace plugins. The dropdown **floats over** the installed list — it never reflows the rows beneath it — and is **capped at 10 matches** (**`INSTALL_MENU_LIMIT`**, pure filter in **`installableMatches.ts`**); a broader query appends a muted **`+N more — refine your search`** hint (**`data-testid="skills-install-more"`**) rather than silently truncating. Each result carries a **Download/install** icon (**`IconDownload`**, tooltip **Install &lt;name&gt;**) that calls **`POST /coddy/skills/install`** **`{source,plugin}`** and then refreshes the list. The text field is not cleared after an install.
  - **After install (no scroll)**: the list is **not scrolled** to the newly installed skill — the floating menu leaves the scroll position untouched. The new row briefly **flashes** (**`.skills-list-item.is-just-installed`**, **`justInstalled`** for ~2.4s), the **`Installed &lt;name&gt;.`** status line confirms, and the skill is usable from the composer **`/`** menu immediately because the server drops its slash cache on install (**`invalidateSlashCache`**). A just-installed plugin is dropped from the available dropdown optimistically.
- **MCP servers** tab (**`MCPSection`**, section kind **`mcp`** in **`settingsSections.ts`**) is **API-driven** (**`/coddy/mcp*`**), styled after Cursor's MCP settings; it never edits the settings document (**Save all** does not touch it). The list (**`.mcp-list`**) shows every merged server — **config.yaml `mcp_servers`** and the global **`~/.coddy/mcp.json`** (scope **`global`**), overlaid with the project **`./.coddy/mcp.json`** (scope **`local`**) — one row per server (**`.mcp-list-item`**): a **chevron** expander (**`.mcp-expand-btn`**, rotates 90° when open), a **status dot** (**`.mcp-status-dot`**: green **`is-connected`**, red **`is-error`**, gray **`is-disabled`**, amber **`is-unsupported`**, amber **`is-needs_approval`**, red **`is-denied`**; tooltip carries the probe error or the trust verdict), a server glyph, the name with a **scope badge** (**`global`** / **`local`**, reusing **`.skills-list-item-badge`**; the tooltip names the owning file via **`originLabel`** — config.yaml, `~/.coddy/mcp.json`, or `./.coddy/mcp.json`) plus an uppercase transport badge for non-stdio entries, and a dimmed **monospace command line** (**`.mcp-command`**; shows the probe error text for error/unsupported rows). Trailing controls: a **shield trust button** for project-local rows only (**`gated: true`**, **`data-testid="mcp-trust-{name}"`**, **`.settings-btn-approve`** amber while unapproved) posting **`POST /coddy/mcp/{name}/trust|untrust`**, the shared **`Switch`** (server-level enable via **`POST /coddy/mcp/{name}/enable|disable`**), a **pencil Edit** and **trash Delete** — both **disabled for `readonly` rows** (config.yaml-defined servers are edited in the config sections; tooltips say so). All trailing controls are **`flex: 0 0 auto`** (**`.mcp-list-item-head > button`**) so long command/error text cannot squeeze the 40px icon buttons.
  - **Workspace trust**: a project-local declaration lives in the checkout, so a row with **`status: needs_approval`** is **reported, never probed** — it lists no tools, keeps its monospace command line visible (that is what gets approved), and renders a note (**`.mcp-trust-note`**, **`data-testid="mcp-trust-note-{name}"`**) naming the **`source_path`** it came from plus a **`<dl>`** of the declaration the approval covers (**`.mcp-trust-facts`**, built by **`declarationFacts`** in **`mcpServerJson.ts`**): **transport**, **`runs`** (command + args) or **`contacts`** (url), the **names** of env vars and headers, and the **workspace**. Values are never rendered - the decision is about which variables reach the child, not about what is in them. The shield button records the approval, bound to the workspace and the row's **`fingerprint`**; it flips to a withdraw action once **`trusted`**, and is **disabled** under **`status: denied`** (**`mcp.project_trust: deny`**). Global rows carry no shield at all.
  - **Per-tool switches**: expanding a row reveals **`.mcp-tools`** — indented rows (**`.mcp-tool-row`**, dashed separators) with the tool name in monospace, its description, and a **`Switch`** per tool (**`POST /coddy/mcp/{name}/tools/{tool}/enable|disable`**). Tool switches are disabled while the server itself is off. Disabled rows dim their text but keep the switch crisp (same rule as skills).
  - **MCP discovery fieldset**: the tab opens with a **second, separate** **`.settings-fieldset`** (**`.mcp-discovery-box`**, legend **MCP discovery**) sitting **above** the server list box. It explains why project-local entries are gated and carries the **`mcp.project_trust`** **`select`** (**`data-testid="mcp-project-trust"`**, **`ask`** / **`allow`** / **`deny`**, capped at **`420px`**) posting **`POST /coddy/mcp/project-trust`** on change. It lives **here**, next to the servers it governs, rather than as its own settings section; like the rest of the tab it is **not** part of **Save all**. Both fieldsets share the panel's inline edges. The per-server **shield renders only under `ask`** (**`showsTrustControl`** in **`mcpServerJson.ts`**): under **`allow`** every project server starts anyway and under **`deny`** none does, so an inert shield would imply a per-server choice the policy has already taken.
  - **Toolbar**: **Add server** (opens the editor card prefilled with **`MCP_SERVER_TEMPLATE`**) on the left, a **refresh** icon (**`GET /coddy/mcp?refresh=1`**, re-probes) on the right (**`.mcp-toolbar`**, space-between).
  - **JSON editor card** (**`.mcp-editor`**, **`MCPEditorCard`**): Cursor-style editing of one mcp.json entry — a name input (only when adding; names with **`__`**, spaces, or path separators are rejected client-side, mirroring the server), a **scope radio picker** (**`.mcp-editor-scope`**, only when adding, default **Local**: local saves to `./.coddy/mcp.json`, global to `~/.coddy/mcp.json`; the footer note echoes the target file), a monospace **`<textarea>`** (**`.mcp-editor-json`**) holding the entry JSON (**`command`/`args`/`env` object/`disabled`/`disabledTools`**), validation via the pure helpers in **`mcpServerJson.ts`** (do **not** inline them), and **Save** (**`PUT /coddy/mcp/{name}?scope=`**) / **Cancel** actions. Editing an existing row renders the card inline beneath that row with the scope pinned to the row's owning file.
  - **Scroll stability**: same **`loadServers(firstLoad)`** gating as skills — refreshes never unmount the list.
- **Boolean switch fields** (**`SwitchField`**, **`external/ui/src/ui/settings/SwitchField.tsx`**): every on/off setting in a settings form - the schema-driven **`SchemaForm`** booleans (**`models[].multimodal`**, **`models[].stream`**, **`tools.background.enable`**, **`memory.enable`**, gateway **`enabled`** flags, ...) and the Skills **auto-discovery** row - renders through this one component, never as a bare **`Switch`** beside a **`<span>`**. It lays the shared **`Switch`** (**`.skill-switch`**, **38×22**, 16px thumb), the label (a real **`<label htmlFor>`**, **`.settings-switch-field-label`**, **0.9rem**, **`var(--text)`**, so clicking the text toggles the control) and the optional description (**`.settings-switch-field-desc`**, reusing **`.settings-field-desc`** typography) on a **two-column grid** (**`.settings-switch-field`**: **`grid-template-columns: auto minmax(0, 1fr)`**, **`column-gap: 8px`**, **`row-gap: 4px`**, **`align-items: center`**).
  - **Alignment contract**: the label's vertical centre matches the switch's centre (**within 1px**) and the label starts **8px** after the switch. The description lives in the **label column** (**`grid-column: 2`**), so its left edge equals the label's left edge at every viewport and never depends on the switch width or a hand-tuned indent. The checkbox-era **`padding-left: 28px`** rule (**`.settings-field-desc-below-checkbox`**) is gone and must not come back: an indent expressed as padding drifts the moment the control changes size, which is exactly how the description ended up under the switch instead of under its label.
  - **Naming**: the visible label names the control through **`aria-labelledby`**; pass **`ariaLabel`** only when the visible text is state copy (**Enabled** / **Disabled**, as on Skills auto-discovery). **`Switch`** enforces the precedence itself: with **`ariaLabel`** set it emits no **`aria-labelledby`**, so a direct caller cannot invert it. The former **`.settings-row-inline`** helper is deleted with its last consumers: used without **`.settings-row`** it fell back to inline flow (no gap, label baseline on the switch's bottom edge), which is how this bug happened, and there is no inline-flex variant to reach for.
  - **Coverage**: **`SwitchField.test.tsx`** pins the DOM (one grid container, label click toggles, explicit **`ariaLabel`** wins) and, since jsdom does no layout, the exact source rules: **`grid-template-columns: auto minmax(0, 1fr)`**, **`column-gap: 8px`**, **`row-gap: 4px`**, **`align-items: center`**, the child-selector description rule with **`grid-column: 2`** and **`margin: 0`**, and the absence of both legacy classes. **`SchemaForm.switch.test.tsx`** and **`SkillsSection.autoDiscovery.test.tsx`** pin the two call sites. The live check measures **`getBoundingClientRect()`** of switch, label, and description on **1280px** and **390px** (see **`docs/ui.md`**, **Settings: boolean switch fields**). Reference captures: **`docs/assets/settings-switch-field-after-models-rows-1280.png`** (wide), **`docs/assets/settings-switch-field-after-models-390.png`** (phone), **`docs/assets/settings-switch-field-after-skills-rows-1280.png`** (state-copy label); the matching **`settings-switch-field-before-*`** files show the defect this contract prevents.
- Object sections render their sub-schema fields directly (the tab already names the section); custom model editors are injected via the **`SchemaForm`** **`fieldOverride`** hook, not by forking the generic renderer.

### Session identifier in URL

`#/s/<sessionId>` survives reload/share as long as the browser hits the **same Coddy http instance** backing the **`sessions`** root hash. SPA keeps **`X-Coddy-Session-ID`** synced with whichever id anchors the fragment.

### Multi-session streaming and Stop

- The SPA may run **more than one** **`POST /v1/responses`** at a time, each with its own **`X-Coddy-Session-ID`**, while the user switches **`#/s/...`** quickly. Each session keeps a **shadow transcript** in memory so streamed rows from session **A** are never appended to session **B**. Routing uses **`pickStreamMutationBase`** in **`external/ui/src/ui/chat/streamMutationBase.ts`**.
- The shadow transcripts form a **small LRU** (**`ShadowTranscriptCache`** in **`external/ui/src/ui/chat/sessionTranscriptCache.ts`**, cap **`SHADOW_TRANSCRIPT_CACHE_CAP`** = 3 non-pinned entries). The **viewed** session and every session with a **live composer stream** (own **`POST`** or a relay attach) are **pinned**; the least recently used entries beyond the cap are dropped whenever a session is picked, the user goes home, a stream finishes, or **`loadMessages`** stores a fresh snapshot. An evicted session is simply **re-fetched** from **`GET /coddy/sessions/{id}/messages`** on the next visit, the path a finished dialog already takes today, so eviction costs no extra request. A **`loadMessages`** response that lands after the viewer moved to another session only refreshes that session's shadow; it never paints under the current route.
- Message rows (**`UserMessage`**, **`AssistantMessage`**, **`ThinkingMessage`**, **`ToolCallMessage`**, **`SystemNoticeMessage`**, **`CompactionMessage`**, **`MemoryCopilotMessage`**) and **`Markdown`** are **`React.memo`** components, so unchanged rows skip rendering and markdown re-parsing while a token streams. **`MessageList`** itself still maps the transcript on every update, and the branch navigator, plan document, permission and question rows are not memoized. Handlers passed into the rows go through **`useStableHandler`** (**`external/ui/src/ui/components/useStableHandler.ts`**) to keep their identity across shell re-renders.
- Off-screen transcript rows skip layout and paint through **`content-visibility: auto`** with **`contain-intrinsic-size: auto 120px`** (last rendered height is reused, so **`scrollHeight`** and stick-to-bottom stay stable). The rule is **opt-in per row type** (**`.msg-assistant-stack`**, **`.thinking-row`**, **`.msg-system-stack`**): **`content-visibility: auto`** also enables **paint containment** while a row is visible, which clips anything drawn outside the row box. User rows (hover **edit** button at **`left: -32px`**) and the permission, question and plan cards (box shadows past their wrapper) therefore stay uncontained. Contained rows carry **`padding: 4px`** with a matching **`margin: -4px`** and **`width: calc(100% + 8px)`** (global **`border-box`**), so keyboard focus rings on edge controls (**`2px`** outline plus **`2px`** offset on the copy buttons, the edge-to-edge **`<summary>`** ring) paint inside the containment box while content position and line wrapping stay exactly as without containment. Covered by **`transcriptRowContainmentCss.test.ts`**.
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
- **`404`** from **`GET /coddy/scheduler/jobs`** means the server build has no scheduler HTTP surface; **`503`** means **`scheduler.enable`** is false for that process. The drawer shows a plain-language line instead of crashing.
- The job **`body (markdown)`** field uses the shared **`MarkdownLineEditor`** (see **Markdown line editor** below). Gutter, active-line highlight, wrap-aware numbering, and content-driven height match the plan card markdown pane.

### Background tasks panel

**`#/s/<sessionId>/tasks`** opens the panel and **`#/s/<sessionId>/tasks/<task_id>`** opens it on one task. The route hangs off the **session segment** on purpose: a task belongs to one chat, so an address that does not carry the chat reloads into a panel with no session behind it. Closing the panel writes the plain **`#/s/<sessionId>`** back.

The panel is **docked inside the session**, to the right of the transcript, rather than floating over the shell like **History** or **Scheduler**. A background task belongs to the conversation that started it, so being part of that conversation is what tells the operator which session a process came from; there is no session label to add. Implementation lives in **`external/ui/src/ui/tasks/`** (**`BackgroundTasksPanel.tsx`**, pure helpers in **`taskStatus.ts`**, REST client in **`api.ts`**).

- **Placement.** **`.bgtasks-panel`** is **`position: fixed`** against the right viewport edge (14px inset, full height), **380px** wide, using the shared **`--coddy-glass-panel-*`** tokens. On **`min-width: 1200px`** the chat column yields that width (**`.shell-main.shell-tasks-open`** pads **`#messages`** and **`.chat-bottom`**), so the composer and transcript stay centred in what is left instead of hiding underneath. It carries **no backdrop**: it sits beside the chat, it does not block it.
- **Polling, not SSE.** A background task outlives the turn that started it, so the SSE stream cannot keep the panel honest. The shell polls **`GET /coddy/sessions/{id}/background-tasks`** every **2.5s** while anything runs and every **15s** otherwise (**`tasksPollIntervalMs`**), and the open detail pane refreshes on the same tick. A poll against an unreachable server degrades to a normal error result, never an unhandled rejection once per tick.
- **Ordering** is **purely by start time, newest first** (**`sortTasksByStart`**), in both sections. Running tasks are **not** floated to the top: they already have their own section, and mixing two orderings makes a list that never sits still to read.
- **Running** (**`.bgtask-card`**) is a card per live task: a **status dot** (**`.bgtask-dot--running|success|danger|warning|muted`**, the same vocabulary as the MCP server rows; the running dot pulses and the pulse is dropped under **`prefers-reduced-motion`**), the monospace command, **elapsed · est. · overdue**, a **Stop** control reusing **`composer-run-icon--stop`**, and a progress bar drawn **only** while running **and** when the model supplied **`expected_seconds`**. With no estimate there is no bar: the UI never implies knowledge it does not have. A **subagent run** (**`kind: "agent"`**, started by **`spawn_agent`**) is the same card with an **`agent`** badge (**`.bgtask-kind-badge`**) after the label, which the pool already writes as **`agent <name>: <description>`**; it has no command to show, so the label is the whole title. The timing line of an agent task never carries an **`exit N`** segment: the pool's exit code for a subagent run is synthetic, so the status alone says how it ended (**`taskTimingLine`** skips it via **`isAgentTask`**, and the same helper feeds the running card, the finished detail pane and the transcript chip).
- **Finished N** is a **counter**, not a list (**`.bgtask-section-toggle`**). Expanding it renders one scannable line per task (**`.bgtask-finished-row`**: dot, monospace command or agent label, the **`agent`** badge on subagent rows, outcome, clock) capped at **`FINISHED_RENDER_CAP`** (40) with a muted note naming what stays on disk. This is how "keep every log" and "do not load the app" hold at once: the history is counted, rows render on demand, and a task's output is fetched only when it is opened.
- **Clear** (**`.bgtask-section-action`**) appears only when there is history, and drops the finished tasks of this session via **`DELETE /coddy/sessions/{id}/background-tasks`**. Running tasks are untouched.
- **Detail pane** replaces the sections inside the same panel (**← Back to tasks**) and shows the command, any error, and the captured output, following the tail unless the reader scrolls up. A dropped in-memory window is flagged **truncated**. For an agent task the command block gives way to **`.bgtask-detail-agent`**: the **Subagent** name and an **Open transcript** button that routes to **`#/s/<child session id>`** through the same in-place session open as a History pick (the panel closes, the hash changes); the button is disabled, with a title saying why, until the row carries **`agent.session_id`**. The output pane is unchanged: it is the child's live progress log and ends with the **`=== subagent report ===`** block once the run settles.
- **Empty state** names the chat, not the app: *No background tasks in this chat yet*.
- **Phones** (**`max-width: 1199px`**) give the panel the screen between the standard insets — there is no room to sit beside a transcript, and the output is what the operator came for. Finished rows grow to a **40px** touch target.
- **Opener** (**`.bgtask-chip`**, **`BackgroundTasksChip.tsx`**) sits at the **end of the transcript**, under the last message and above the composer — not in the nav rail. The tasks belong to this chat and the thing that started them is directly above, so that is where the opener belongs. It reads **`N running tasks`** while work is in flight (**`is-running`**, accent border and tint) and falls back to **`N background tasks`** so the history stays reachable once everything has finished. A chat that never ran anything renders **no chip at all**.

### Background task ticker card (transcript)

A **run_command** call with **`background: true`** returns immediately, so its tool row would otherwise read as a finished call with nothing to show. When a transcript tool call matches a task's **`tool_call_id`**, **`ToolCallMessage`** adds a live chip to the **collapsed** summary row (**`.tool-bgtask-chip`**, dot plus **`Running · 30s · est. 2m`**) so the state is visible without expanding anything, and the expanded body gains **Open in Tasks** and, while running, **Stop**. The chip reports the final state (**Succeeded**, **Timed out**, **Stopped**, **Orphaned**) once the task ends. The mapping comes from the same poll that feeds the drawer (**`backgroundTasksByToolCallId`** in **`App.tsx`**), so the transcript and the drawer can never disagree.

### Subagent transcript (read-only)

A child session (**`sub_<hex>`**) is the transcript of one delegated run. **`GET /coddy/sessions/{id}/messages`** marks it with **`subagent {parentSessionId, name, taskId}`** and **`readOnly: true`**; an ordinary session carries neither field, so their absence means a normal chat (**`parseSubagentTranscriptMeta`** in **`chat/subagentTranscript.ts`**). Either field alone is enough to lock the composer.

- **Composer slot.** **`ChatScreen`** renders **`SubagentReadOnlyNotice`** (**`.subagent-readonly-notice`**) in place of the **`Composer`**, in both the hero and the docked layout: *Read-only transcript of subagent `<name>`. Prompts go to the parent chat.* plus an **Open parent chat** link. The notice reuses the composer's glass card tokens so the bottom of the column keeps its weight, and it sits inside **`.chat-bottom-inner`**, so the transcript's bottom reserve is measured from it exactly as from the composer.
- **Nothing is sent.** With no composer there is nothing to submit; the shell additionally drops **`onSend`**, retry, message editing and the plan card's **Run plan** / **Discard** for such a session, because the server answers **409** to every prompt against a child. The plan handlers are withheld, not stubbed (the same conditional spread as **`onEdit`**), so a **`plan_document`** card on a child renders without its footer and with a read-only markdown editor (**`.plan-document-card--readonly`**) instead of showing controls that do nothing.
- **Back to the parent.** The link is a real **`#/s/<parentSessionId>`** href (middle-click and Ctrl/Cmd-click open a tab); a plain click goes through the same in-place session open as a History pick.
- **Header.** A child has no History row to name it, so the chat header falls back to *Subagent `<name>`* (**`chat.subagentTitle`**) instead of *New chat*.
- **Reaching it.** Child sessions are hidden from History. They are reached from the Tasks panel (**Open transcript**) or by URL; the shell fetches a **`sub_*`** id even though it is absent from the sessions list. While the child runs the live state is served, afterwards the bundle, so the transcript shows the child's tool calls and its final answer through the ordinary renderer.

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

**Read-only transcript**

- **`onRunPlan`** and **`onDiscard`** are optional. **`MessageList`** forwards **`onPlanDocumentRun`** / **`onPlanDocumentDiscard`** only when the shell provides them, and a subagent child transcript provides neither (exactly like **`onEdit`**). With no handler at all the card carries **`.plan-document-card--readonly`**, renders **no footer**, keeps the preview and the eye toggle, opens the **`MarkdownLineEditor`** as **`readOnly`**, and schedules no autosave **`PUT`**. Each footer button is also rendered only when its own handler exists, so no stubbed handler can leave a dead **Run plan** or **Discard** on the screen.

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

The expanded body uses the same tool-specific visual preview as the permission gate, followed by a separate **Result** panel when the tool returned output. It never repeats the approval actions. Results stay **raw** monospace text with **no Markdown**. Terminal structured cards that fully express their result suppress redundant boilerplate: completed todo updates keep their saved plan snapshot, and a completed **`plan_exit`** card presents a theme-aware **Plan mode → Agent mode** transition with a semantic-success checkmark and the localized completion message instead of the raw `switched session to agent mode` string.

- Tool arguments arrive via `tool_call_update` status `in_progress` where `content[0].content.text` is raw JSON args.
- Tool result arrives via `tool_call_update` status `completed` or `failed` where `content[0].content.text` matches the HTTP user preview rules (**raw** text, **no Markdown**): the first **19** content lines, then a twentieth row that is only **`...`**, when the output is longer; **`_meta.coddy.toolResultPreview`** marks truncation. Outputs that are not truncated skip the fixed-height viewport and overflow toggle (natural-height grey mono panel). When truncated, the fixed-height panel shows the clipped preview with **no vertical scrollbar**. The shared **More…** button performs **GET `/coddy/sessions/{id}/tool-calls/{toolCallId}`** once, fills the same panel with the full saved body at the same max height, enables **overflow-y** scrolling, and turns into **Less**. **Less** restores the clipped preview without another request while the full text stays in memory for this session.

#### Tool card UI (bundled SPA, current)

Implementation lives in **`external/ui/src/ui/messages/ToolCallMessage.tsx`**.

- **Layout** - Outer **`thinking-row coddy-tool-call-row`**; **`details`** uses **`thinking-details coddy-tool-details`**. **`summary.thinking-summary`** with **`thinking-left`** ( **`aria-label="Tool summary"`** ): **`thinking-chevron`**, **`thinking-label`** (tool title or kind, **`...`** suffix while **`pending`** / **`in_progress`** ), **`thinking-dur`** (**finished** durations from **`meta.json`**, **live elapsed** while in flight when **`startedAtMs`** is set, placeholder **`-`** when unknown). Transcript stacking uses **`messages-inner` `gap`** like other **`thinking-row`** blocks (avoid tool-only asymmetric margins). Expanded **`thinking-body coddy-tool-call-body`** is transparent and contains the shared **`PermissionToolPreview`** without nested copy or approval actions. A returned output follows in **`tool-call-result-card`** with a compact Result header and **`pre.tool-result-pre`** body (**always raw plaintext**, never the Markdown renderer).
- **Viewport** - Truncated results ( **`resultWasTruncated`** from list / SSE) add **`tool-result-viewport tool-result-viewport--tall`**, clipped with **`tool-result-viewport--clip`** (capped at ~20 lines, **overflow-y** hidden) or scrollable with **`tool-result-viewport--scroll`** after **More…**. Large **`write`** / **`write_file`** code previews and **`apply_patch`** / **`edit`** diffs use the shared measured **`permission-preview-viewport`** cap (**190px**, **170px** on phones): the toggle appears only when the rendered body overflows, expansion scrolls inside the tool card, and **Less** returns the viewport to the top. Short results and previews follow content naturally without a fake tall box or toggle row.
- **Controls** - Result **More…** (**`data-testid="tool-result-more"`**) / **Less** (**`data-testid="tool-result-less"`**) and preview overflow controls reuse the shared left-aligned **`tool-overflow-toggle`** tab button, attached flush to the panel's bottom border. Phone layouts increase the button's minimum height to **36px** for a more comfortable touch target.
- **Full body** - The SPA obtains saved full strings only via **GET `/coddy/sessions/{sessionId}/tool-calls/{toolCallId}`**. **`App.tsx`** wires **`onFetchToolCallFull`** to that endpoint and merges JSON **`result`** into **`fullResultText`** plus full **`args`** into **`argsText`**. Restored **`apply_patch`**, **`write`** / **`write_file`**, and **`edit`** cards auto-fetch in any status when the 200-character list **`argsPreview`** is incomplete JSON, so their structured previews match live SSE cards after reload instead of rendering an empty **`+0 −0`** header. Reconciles via **`loadMessages`** keep already-complete args (**`pickRicherToolArgs`** in **`toolCallArgs.ts`**) instead of re-truncating them, and complete args re-arm the one-shot fetch so a later truncation recovers again. The preview overflow toggle re-measures on the enclosing **`details`** **`toggle`** event (collapsed foldouts measure zero while hidden).

- **Spawn agent arguments** - `spawn_agent` uses `SpawnAgentCard` inside the existing disclosure body instead of JSON when `agent` and `prompt` are available. The header pairs a small outlined robot icon in an accent-tinted tile with the agent name and optional description. A clock badge on the right labels the supplied positive `timeout_seconds` as `Timeout Ns`; it is separate from elapsed execution time in the disclosure summary and wraps below the identity on narrow widths. The prompt occupies an inset rounded panel, preserving line breaks and wrapping long words as plain text. Both themes use semantic foreground and surface tokens. Results and their More / Less controls retain the standard tool behavior, with the result visually attached below the agent card. Truncated history arguments are fetched once per incomplete preview, including running calls; a later truncated reconciliation can fetch again. Unavailable or malformed arguments keep the readable argument fallback. Labels follow the active English/Russian UI locale.

Tool call history is persisted per session under `tool_calls/` so it can be restored after restart.

### Tool permission gate

**PermissionPromptSection** renders only for tool calls that actually require approval. Read-only calls do not get a placeholder card, status checkmark, or “runs without a permission prompt” message.

- The header contains one short action question and one technical tool-id badge. Do not repeat the raw tool name in the question or preview header.
- Buttons keep the backend option labels and order: **Allow**, **Allow always**, optional **Always allow `<program>`**, **Reject**.
- The **Always allow `<program>`** option appears **only** for **run_command**, and only when the command is a single plain invocation (**`internal/permission.ProgramGrant`** refuses anything carrying shell metacharacters or a leading **`VAR=`** assignment). Its label names the exact allowlist entry that will be stored — **`curl`** for a bare program, **`git status`** for a multiplexer — so the operator approves the string that is actually saved. **Allow always** keeps its narrow exact-command meaning; the wider grant is never implied.
- Four buttons must still wrap cleanly at phone width: the row wraps rather than shrinking any button below its touch target.
- The preview uses the matching transcript **tool_call.argsText** when available; this preserves structured arguments when the ACP permission rationale contains only prose.
- **apply_patch** shows a color diff with old/new gutters, additions, deletions, hunk context, and the affected path. **edit** derives the same diff treatment from **oldString** and **newString**, retaining up to two unchanged lines on either side when present.
- **run_command**, **write**, **mkdir**, **touch**, **mv**, **rm**, and **rmdir** use compact operation-specific previews instead of raw **Arguments:** JSON. Unknown permission tools fall back to a monospace body.
- Preview bodies have a collapsed height cap. **More…** appears only when DOM measurement confirms overflow; it keeps the same bounded viewport, enables internal vertical scrolling, and changes to **Less**. The shared tab button is left-aligned under the viewport and gets a taller touch target on phones. Short content has no toggle.

Implementation: **external/ui/src/ui/chat/PermissionPromptSection.tsx**, **PermissionPromptPreview.tsx**, and **permissionToolPreview.ts**.

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
  - Summary row matches **thinking** (**chevron**, **tool name**, **duration** beside the label). Expanded details reuse the permission card's structured tool preview without approval controls: diffs keep colored old/new gutters; filesystem and shell calls show paths, operation metadata, or commands instead of raw args JSON; **read**, **grep**, **glob**, and **print_tree** use the same compact language. Unknown tools retain a styled monospace fallback. Results are **raw plain text** in the muted **Result** section (**no Markdown**); when both preview and result exist, their touching borders and shared outer corners form one continuous execution card. When **`resultWasTruncated`** is false (output fits the preview cap), the result block grows with content only (no fixed tall viewport or overflow toggle). When truncated, the capped viewport and shared **More…** / **Less** button match the tool timeline above (REST fetch only on the first **More…**).
  - Duration label is computed from persisted `tool_calls/<id>/meta.json` `startedAt` and `finishedAt` when available, with live **`startedAtMs`** updates while **`in_progress`**.
- `assistant_message`
  - Assistant prose for a turn, split into **one or more** bubbles (a new bubble opens whenever text resumes after a `tool_call` / `thinking` — see **Ordering rules** below). Each is reconciled from **`GET /coddy/sessions/{id}/messages`** when streaming ends or after a refetch. After **Stop** mid-stream, that **`GET`** can lag the partial row already on screen; **`mergeTranscriptPreferLocalSuffix`** (see **Multi-session streaming and Stop** above) preserves visible text until the server catches up.

- `system_notice`
  - A UI-only row from the session's `uiLog` (never sent to the model), rendered by **`SystemNoticeMessage`** with the uppercase **SYSTEM** label, a monospace `pre-wrap` body, the copy control and the timestamp. The action row (**`.msg-system-foot`**) sits below the bordered card and is inset by the card's horizontal padding (**`14px`**), so its copy control starts at the same x as the one under an assistant row; covered by **`systemNoticeFootCss.test.ts`**. Two levels: **`error`** (a failed request or turn; red family, `role="alert"`, and a **refresh** control on the last row that re-runs the turn) and **`notice`** (information the operator should see once, such as a project hooks file held until approved; blue family via **`.msg-system-notice`** / **`.msg-system-stack-notice`** in both dark and light themes, `role="status"`, **no refresh control** even on the last row). Rows of any other level are dropped by the client rather than mis-rendered.

Ordering rules:

- The transcript renders in **arrival order** (pure array position), during streaming and after completion alike — the list is never re-sorted and tools are never grouped above the reply.
- `thinking` blocks appear wherever reasoning arrives in the stream.
- `tool_call` blocks appear where tool events arrive.
- Assistant text is **segmented**: text that resumes after a `tool_call` or `thinking` row opens a **new** `assistant_message` bubble, so prose interleaves with tools/thinking in the order the model emitted them (preamble → tool → follow-up → tool → …). During streaming this is enforced by **`consumeComposerSse.ts`**: new `tool_call` / `thinking` rows are appended at the **end** (not spliced above the reply) and mark the segment dirty; the next text delta drains the pending tool queue, then opens a fresh assistant segment (**`currentAssistantId`**), with **`lastAssistantId`** returned for the finalize/`syncAssistantFromServer` fill. This matches the chronological ordering **`loadMessages`** reconstructs from **`GET /coddy/sessions/{id}/messages`** on completion (which mints one assistant bubble per server message via **`stableAssistantItemId`**). The **live** stream and the **reloaded** transcript therefore show the **same** interleaving — there are not two different behaviors.

### Branch navigator (edited messages)

Editing a user message forks the conversation instead of replacing the old answer, so the transcript needs a way to say "this point has more than one continuation".

- The control is a transcript block type, **`branch_nav`**, rendered by **`BranchNavigator.tsx`** **under** the user bubble it belongs to: **`‹`** button, an **`n/m`** label, **`›`** button (**`.branch-nav`**, **`.branch-nav-btn`**, **`.branch-nav-label`**).
- It is **not** a bubble and carries no frosted panel: **`align-self: flex-end`** puts a **4px**-gap row on the user side of the column with **8px** below it, so it reads as transcript metadata rather than content.
- Buttons are **22×22px** ghost tiles - transparent fill, hairline **`--text` 14%** border, **55%** muted glyph - that tint on hover. The label is **12px**, **50%** muted, **`min-width: 28px`** and centered so switching branches never shifts the row.
- Arrows are **disabled** (**`opacity: 0.3`**), not hidden, at the first and last branch. Accessible names are **Previous branch** / **Next branch**; the label announces **Branch n of m**.
- Exactly **one** navigator per branch point, even after a reconcile that re-injects the list (**`deduplicateBranchNavs`**).
- A branch point whose other threads were deleted renders **no** navigator: the server stops reporting a fork with fewer than two surviving sessions, so the row disappears instead of showing **`1/1`**.
- The pencil that creates a branch is the user bubble's own **`.msg-user-edit`** control (**Edit message**): a **26×26px** borderless icon parked **outside** the bubble's left edge (**`left: -32px`**, vertically centered), **`opacity: 0`** until the message stack is hovered or the button takes focus. It never occupies bubble width.

Functional contract, endpoints, and the leaf-resolution rule: **`docs/ui.md`** (**Message editing and conversation branches**).

### Composer pill

Muted **Auto** pill tracks future modality toggles; UI copy stays English everywhere.

### Composer workspace chips (folder / branch / worktree)

A chip row renders inside **`.composer-context-row`** — the **first child** of **`.composer-card`**, above attachments and the field — mirroring Claude Desktop's workspace chips. The folder / branch / worktree chips are produced by **`WorkspaceChips.tsx`** (helpers in **`chat/workspaceContext.ts`**) inside **`.composer-context-chips`**; data from **`GET /coddy/workspace/context`** (session header, or **`?path=`** preview before a session exists).

- **One wrap flow** — **`.composer-context-chips`** is **`display: contents`**, so the environment chip, folder, branch, worktree, and the improve-prompt control are all direct flex items of the single **`flex-wrap`** **`.composer-context-row`**. Chips wrap **individually**: only what overflows moves down, and the worktree checkbox trails the branch until a long branch pushes it to the next line. Do **not** re-nest the chips in their own flex box — that wraps the whole group as a unit (on mobile: environment alone, then folder+branch, then worktree).
- **Folder chip** (**`composer-workspace-chip`**) — folder icon plus workspace basename (full path in **`title`**). Click opens the **Recent** menu (**`workspace-folder-menu`**, **`mode-menu`** family, Claude Desktop style): a **`Recent`** header, MRU folder rows from **`localStorage`** **`coddy_workspace_recents_v1`** (**`chat/workspaceRecents.ts`**, cap 8) with the current workspace marked by a **✓** (**`is-selected`**), a separator, and **`Open folder…`** at the bottom.
- **Open folder…** opens the project-styled **folder browser modal** (**`WorkspaceFolderModal.tsx`**, **`workspace-modal`**, centered over a dim backdrop): editable path field, **`..`** row, subfolder rows from **`GET /coddy/workspace/folders`** (row click **navigates into** the folder), footer **New folder** / **Cancel** / **Open** (picks the currently browsed folder). The browser starts at the **parent** of the current workspace.
  - **Path field** (**`workspace-modal-path`**, an **`input`** on the **`.mode-menu-filter`** token recipe — never **`direction: rtl`**, which breaks the caret): **Enter** browses to the typed path. While it holds a path that has not been visited, the primary button reads **Go** and navigates; otherwise it reads **Open** and picks.
  - **New folder** (**`workspace-modal-btn--lead`**, first in the footer, pushed away from the **Cancel** / **Open** pair by **`margin-right: auto`**) opens an inline name row **between the path field and the list** (**`workspace-modal-row--new`**: folder glyph, **`workspace-modal-new-name`** field on the path-field token recipe, **Create folder** and a **×**). It is a **`flex: none`** sibling of the list, deliberately **not** a row inside it: a child of the scrollport scrolls away under the operator, and on the shortest viewport the checker drives (**1024x300**, where the list has already shrunk to zero) it would be taller than the port that holds it. For the same reason it is tighter than a folder row (**`padding: 2px 8px`**) - it is chrome, and chrome is what a short dialog runs out of. The row is a form, so it does not take the row hover. **Enter** / **Create folder** posts **`POST /coddy/workspace/folders`** and the dialog adopts the answer — the listing of the **new folder** — so **Open** picks it without another step. **Escape** / **×** closes the row. Disabled on the **drive level** and while the row is open; **Create folder** is disabled on an empty name. A taken name (**409**) keeps the row and its text and reports the clash in the same inline error slot as a failed listing.
  - **Drive level** (Windows) — a drive root has no parent directory, so the server answers **`"parent": ":drives:"`** and **`?path=:drives:`** lists the volumes (**`drives: true`**, drive glyph rows, `No drives` empty state, **Open** disabled). This is the only way out of a drive in the SPA; hosts without drive letters never see the level.
  - **Height and scroll** — the dialog is a column flex box bounded by **`min-height: min(262px, 60dvh)`** and **`max-height: min(60dvh, 520px)`** (each with a **`vh`** line first, for engines without **`dvh`**; Safari keeps its dynamic chrome inside **`vh`** but outside **`dvh`**). Head, path row and actions are **`flex: none`**; the folder list (**`workspace-modal-list`**) is the **only** scrollport and carries **`min-height: 0`**, so it — and nothing else — gives up height on a short window. A floor on the list instead would push the action row past **`max-height`**, where the dialog's **`overflow: hidden`** clips it beyond reach (issue #159). The list contains its own **`overscroll-behavior`**, so a wheel gesture past the last folder does not scroll the page behind the modal, and styles a thin scrollbar on the settings-list recipe.
- **Branch chip** (**`composer-branch-chip`**) — git branch icon plus the current branch; rendered **only** when **`is_git_repo`**. Click opens the branch list (**`workspace-branch-menu`**), current branch first and marked **`is-selected`**. Picking a branch calls **`POST /coddy/sessions/{id}/workspace`** with **`{"branch", "worktree": <checkbox>}`**.
- **Worktree checkbox** (**`composer-worktree-chip`** label + real **`input[type=checkbox]`** **`composer-worktree-checkbox`**) — the **open-branch-switches-in-a-worktree** preference. When the session already runs inside a worktree it is **checked and disabled** (state, not a choice). Branches already checked out in another worktree jump there regardless of the checkbox.
- **Chosen once** — folder, branch, and worktree are fixed at session start. As soon as the conversation has messages the chips **lock** (**`workspaceLocked`**: controls disabled, menus do not open); the server enforces the same rule with **409** on **`POST .../workspace`**.
- **Pre-session (draft/home)** — chips show the server-default workspace. Choices are kept client-side (**pending**) and previewed via **`GET /coddy/workspace/context?path=`**; the first send applies them to the fresh session id (**`POST .../workspace`**) before **`POST /v1/responses`**. Navigating to another session drops pending choices.
- Menus follow the **`Mode`**/**`Model`** conventions: anchored **`mode-menu--portal`** (**`opens-down`** on the hero, **`opens-up`** when docked) on desktop, full-width bottom sheet (**`mode-menu--sheet`**) on narrow shells.

### Composer attachments (multimodal ingress)

Attachments row (**`.composer-attachments`**) renders **inside** **`.composer-card`**, below workspace chips and above the field. All ingress paths are gated on the selected model's **`multimodal`** flag (paperclip picker, clipboard paste in **`textarea#composer`**, drag & drop onto **`.composer-card`**). Functional contract and regression table: **`docs/ui.md`** (**Composer file attachments (multimodal)**).

- Chips (**`.composer-attachment-chip`**, 140px, 10px radius) show the file icon plus name; `image/*` files swap the icon for a **28×28 object-fit-cover thumbnail** (**`.composer-attachment-thumb`**, local object URL revoked on remove/unmount). Locked edit-mode chips stay icon-only (metadata has no bytes).
- The **sent user bubble** echoes image attachments with an **optimistic 26×26 thumbnail** (**`.msg-user-file-thumb`**) from a `previewUrl` blob URL created at send time (`optimisticUserFiles.ts`). Once the server snapshot includes **`files[].preview_url`**, that durable session thumbnail replaces the blob and the blob URL is revoked; reloads keep the image preview.
- Pasted clipboard images get deterministic names **`pasted-<n>.<ext>`** (browsers hand every clipboard image over as `image.png`).
- Paste/drop against a non-multimodal model attaches nothing and flashes the inline notice **`.composer-attach-hint`** (**`role="status"`**, auto-clears) below the attachments row.
- Switching to a non-multimodal model does not discard existing files: chips use **`.composer-attachment-chip--disabled`** (muted, grayscale, dashed border), attachment-only Send is disabled, and text sends omit while retaining them for a later multimodal selection.
- The server repeats this capability check against the effective YAML model and drops any client-supplied **`inline_files`** when **`multimodal`** is false, so disabled files cannot reach the provider or session assets through a stale/custom client.
- While files are dragged over the card it carries **`.composer-card--dragover`** (inset ring accent) as the drop-target affordance.
- **Attachment-only send** is valid: an attachment alone unlocks **Send** (see **Composer primary action**).

### Composer context meter

Ring to the **left** of **Send** in **`Composer.tsx`**. Implemented by **`ContextUsageRing`**: inner stroke always visible; outer progress arc only when usage **> 0**, flat color (no gradient or shadow), fill from **12 o'clock** clockwise. Colors use **`--coddy-context-ring-inner`** and **`--coddy-context-ring-fg`** (both themes in **`styles.css`**). Dark outer arc **`#f5f3ff`** (logo stroke); light outer arc **`var(--accent)`**.

- Do **not** put a percent label **on** the ring. Percentages and counters belong **only** in the tooltip (**`rail-tip`** family), above the ring, centered, wide enough via **`composer-context-tip`** CSS.
- Idle home (**`contextIdle`**): inner ring only (no outer arc); tooltip **`No context usage yet`** plus **`Max context …`** only (no **`Model …`** line).
- Active session: arc fills from stats; live **`usage_update`** SSE replaces the current total immediately and refreshes detailed stats, so manual and automatic compaction reduce the displayed window without a reload. The tooltip may include usage lines but **never** a **`Model …`** line that duplicates **Mode** (the mode dropdown).
- **Click** (or **Enter** / **Space** when focused) on **`.composer-context-tip-host`** opens **`ContextBreakdownPopover`** (**`data-testid="context-breakdown-popover"`**): summary percent, stacked bar, legend (**System prompt**, **Tool definitions**, **Rules**, **Skills**, **MCP**, **Conversation**). **Escape** or **Close** dismisses; hover tooltip returns when closed. Data from **`GET /coddy/sessions/{id}/stats`** field **`contextBreakdown`** (estimated tokens per category).

See **`.cursor/rules/ui-spa.mdc`** for the full wording.

### Context popover usage section and usage banner

The account quota behind the selected model's provider (today: `neuraldeep`,
the hub's `GET /v1/limits` read by the server, see `docs/plans/neuraldeep-usage.md`).
The composer carries no extra control for it: the numbers live where Claude
Desktop keeps its plan limits, under the context window in the context
popover, and a banner speaks up only when something needs the user.

- **Usage section** (`UsageSection.tsx`, **`.context-usage`**,
  **`data-testid="context-usage"`**) is the last block of the context
  popover (`ContextBreakdownPopover.tsx`, floating on a wide shell, a bottom
  sheet on a stacked one), rendered only when the selected model's provider
  reports usage (its row keeps the **Usage limits panel** switch on,
  `providers[].usage_limits_panel`; off, the section and the banner stay
  away and nothing is read). A head row names the provider and plan (`NeuralDeep · Pro`, the hub's tier id with a capital)
  and, when there is one, a **note** on the right (**`.context-usage-note`**):
  `Usage limit reached · resets 20:59` or the cause of a block no clock lifts
  (**error** tone), `Auto-resuming at 20:59` while the agent waits for the
  reset and the key-rejected hint (**warn** tone), the unlimited-option note
  for a model that bypasses the windows, the stale note when the latest read
  failed. Below it one **meter per metered window** (the session, the week,
  the day only when it is above zero), each a label in the reader's language
  (`week` / `неделя`; a duration the hub chose, `3h`, stands as is), the
  reset time in the reader's clock and the percent **used** on the right,
  and a 6 px track (**`.context-usage-track`** / **`.context-usage-fill`**,
  accent fill; amber from 80 %, red when exhausted or blocked, keyed by
  **`data-tone`**). The wallet line (**`.context-usage-foot`**) closes the
  block; a rejected key shows the note alone. Tone text is lifted on every
  dark theme (**`html:not([data-theme="light"])`**, as `color-scheme` is).
- **Banner** (`UsageBanner.tsx`, **`.usage-banner`**,
  **`data-testid="usage-banner"`**) renders **above the composer card** in
  both the hero and the docked layout, only at 80 % of a window (**warn**
  tone, `You've used 85% of your NeuralDeep 3h limit · resets 20:59`), on a
  block (**error** tone: a timed block names its reset, `Usage limit reached
  · Resets 20:59`, the others name their cause), and while the agent waits
  for the reset (**warn** tone, `Usage limit reached · Auto-resuming at
  20:59`). The **×** control (32 px, a 44 px hit area below 1200 px)
  dismisses it for that provider row, window and period (**`localStorage`**
  `coddy_usage_banner_dismissed`); a new period shows it again. Wording
  mirrors Claude Desktop's limit notice.
- Data flow (`useProviderUsage.ts`): REST `GET /coddy/providers/{name}/usage`
  at session open and model change (a cache read on the server), a refresh
  after every finished turn of the viewed session, one hub read after a
  window's reset, one cache read when the server deferred a refresh
  (`refreshPending`/`refreshInSec`), one follow-up when a passed reset still
  shows; `provider_usage` frames on `GET /coddy/events` replace the snapshot
  between turns, and the same frame on the turn stream carries the
  auto-resume countdown. Nothing polls otherwise. Snapshots order by the
  server's `fetchedAt`: a REST answer issued before a pushed frame, or a
  frame that crossed a later read, never brings older numbers back, and
  only the latest read issued applies. A row that answered "unsupported" is
  left alone for five minutes. The snapshot is account-wide; the model's
  selector suffix is compared with `unlimitedModels` client-side.

### Composer primary action (**Send** **/** **Stop**)

- Control **`#btn-send`** (**`.composer-icon`**) sits **directly right** of the context ring (**`.composer-context-tip-host`**).
- **Circular button** (**not** pill or squircle): fixed equal **width** and **height**, **`border-radius: 50%`**, **`box-sizing: border-box`**. Intended diameter **42px** in production CSS (**may track token scale**, but stays a **circle**).
- **Glyphs** live in **`composer-send-glyph`**. **Play** state uses **`~22px`** **▶**; **stop** uses **`.composer-stop-square`** (**14×14px** filled block, centered in the circle). Ring + stop stay **right-aligned** in **`composer-bar-actions`** (same row as mode tabs). Keep contrast high (**`composer-send-play`** vs **`composer-send-stop`**).
- Idle **disabled** when the message field is empty **and no sendable files are attached** (a disabled attachment under a non-multimodal model does not unlock Send); streaming shows **stop** affordance (see **`docs/ui.md`**, section **Composer primary action**).

### Prompt improvement control

The prompt improvement control (**`data-testid="composer-enhance-btn"`**) is a compact **24×24px** icon button in the composer workspace-context row. It shares that row with the environment, folder, branch, and worktree controls, but is aligned to the **right edge** of the composer card; at **≤520px** it is pinned to the row's **top-right corner**, so wrapped chips remain clear below it. It never appears in the textarea or lower action bar. Its native tooltip and accessible name are both **`Improve prompt`**. It is disabled for a blank draft and while improving or generating; an active request spins the icon. Clicking it calls **`POST /coddy/enhance-prompt`**, replaces the draft only after a successful response, and leaves the original intact on failure. **Ctrl+Z** / **⌘Z** restores the immediately preceding draft after a successful improvement.

Composer mode selector

- **`GET /v1/models`** merges Coddy profiles and YAML backends in one list. Split by **`owned_by`**: **`coddy`** means session profiles **`agent`**, **`plan`**, and **`ask`** only. Any other **`owned_by`** marks a configured **`models[].model`** row (YAML backend).
- **`Mode`** lists only **`agent`**, **`plan`**, and **`ask`**. Default is **`agent`**. **`plan`** uses the orange outline treatment (**`mode-plan`**); **`ask`** uses the green one (**`mode-ask`**: **`rgba(34, 197, 94, 0.65)`** border with a matching **3px** halo). The pill label is localized (**`composer.modeAgent`** / **`composer.modePlan`** / **`composer.modeAsk`**).
- Selected mode is sent as top-level **`model`** in **`POST /v1/responses`**.

Composer YAML **`models[].model`** selector

- **`Model`** sits immediately next to **`Mode`** in **`Composer.tsx`**. It lists only YAML backend rows (**`owned_by`** is not **`coddy`**). Opens **down** on the empty start screen (**`isEmpty`**) and **up** when docked over an active chat (same **`opens-down`** / **`opens-up`** convention as **`Mode`**).
- Default for **new chat** follows cookie **`coddy_llm_model`** when valid, else **`default_agent_model`** from **`GET /v1/models`**, else the first YAML row (**`Path=/`**, long **`Max-Age`** on the cookie).
- Opening an existing session restores **Model** from **`GET /coddy/sessions/{id}/messages`** field **`model`** (per-session override on disk), not from the cookie. Changing **Model** updates the cookie (default for the next **New chat**) and **`PATCH`** **`selectedModelId`** on the active session.
- For ReAct (**`agent`** / **`plan`** / **`ask`**), the UI sends **`metadata.model`** with the selected YAML **`id`**; the context-meter **`max_context_tokens`** for the ring follows that YAML row.
- **Long model lists** — backend ids are **`vendor/model`**. When more than one vendor is present the menu groups rows under an uppercase **vendor header** (**`mode-menu-group-label`**) and rows show only the model name (the full id stays in the row **`title`**). On desktop the list is capped to roughly **5 rows** and scrolls (**`mode-menu-scroll`**, **`max-height: min(175px, 50vh)`**). When there are **more than 5** backends a **filter input** (**`mode-menu-filter`**, auto-focused) appears pinned above the scroll; it matches the query against the vendor, the model name, or the full id (case-insensitive). **Enter** selects the first match, **Escape** closes the menu, and an empty result shows a “No models match …” notice. The desktop menu width is constrained (**`mode-menu--llm`**). Helper logic lives in **`chat/llmModelMenu.ts`**.
- **Stays in step with the configuration** — the list behind this menu is read once, at boot, so a model added while the page is open would otherwise be invisible until a reload. **`GET /coddy/events`** carries **`event: config_reloaded`** after every swap of the live configuration (a settings save here or elsewhere, the agent's **`config_commit`** / **`config_rollback`**, a skill install); **`App.tsx`** bumps **`configEpoch`** on it and re-reads **`GET /v1/models`** together with the slash-command list. The menu changes under the operator without any chrome of its own: no toast, no badge, no reopen. The re-read never moves the current selection of an open session - only a chat with no session yet re-picks its default from the new list.
- **Mobile sheet** — on narrow/mobile shells (**`isMobileShell`**, the **`max-width: 1199px`** shell-stack breakpoint) the **`Mode`** / **`Model`** / **`Reasoning`** menus render as a **full-width bottom sheet** (**`mode-menu--sheet`**), the same family as the slash / **`@`** picker sheet, over a dimmed scrim (**`mode-menu-backdrop--scrim`**) instead of the cramped anchored dropdown. The sheet drops the desktop width cap (full width up to **560px**, centered) and gives the scroll a taller **`46vh`** cap. Desktop keeps the anchored **`mode-menu--portal`** dropdown.
- **`Reasoning`** sits immediately next to **`Model`**, shown **only** when the active model row exposes a non-empty **`reasoning_levels`** in **`GET /v1/models`** (reasoning models). Same dropdown styling and **`opens-down`** / **`opens-up`** convention as **`Mode`** / **`Model`**; the button label is the current level capitalized (e.g. **`High`**), default label **`Reasoning`**.
- Default for **new chat** follows cookie **`coddy_llm_reasoning`** when valid for the model, else the model's **`reasoning_default`**, else **`medium`** (or the first offered level). Opening a session restores it from **`GET /coddy/sessions/{id}/messages`** field **`selectedReasoning`**. Switching **Model** clamps the level to one the new model offers. Changing it updates the cookie and **`PATCH`** **`selectedReasoning`**; ReAct turns also send **`metadata.reasoning`**.

Composer does not show tools toggles in this milestone.

### Slash commands picker (skills)

When the caret sits on the current composer line on a **`/`** that is **line-start or preceded by whitespace**, with optional `[a-zA-Z0-9_-]*` typed after it, and outside Markdown fences or blockquotes, the UI loads **`GET /coddy/slash-commands`** with a **100ms** debounce, required **`page=1`** and **`page_size=30`**, and optional **`prefix`** from typed characters after **`/`** (works mid-line, for example `say /foo`). Menu open/close rules match **`slashMenuDraftAtCaret`** in **`external/ui/src/ui/skills/draftSlash.ts`**.

- **Automation** uses **`data-testid="slash-command-menu"`**, per-row **`data-testid={`slash-command-row-${name}`}`**, and **`data-testid="slash-command-more"`** for paging.
- **Row layout**: each row is a single block button (**`.slash-row-btn`**, not a 2-column grid). The **`/name`** (**`.slash-row-name`**, bold) and the **description** (**`.slash-row-desc`**, muted) flow **inline inside one **`.slash-row-line`****: the description begins **right after the name**, wraps onto the next line, and the whole line is **clamped to 2 lines** (**`-webkit-line-clamp: 2`**) so overflow ends in an **ellipsis** — no separate right-hand description column.
- **Desktop** (**`slash-menu--floating`**) attaches above the textarea inside **`composer-card`**. **`Mobile`** (**narrow width**, match roughly **`max-width: 720px`**) renders a dimming backdrop (**`slash-sheet-backdrop`**) plus a bottom sheet (**`slash-menu--sheet`**).
- Choosing a row replaces the typed **`/`…** segment with **`/<name> `** (plain **`#composer`** value and wire text to **`POST /v1/responses`**). The UI **never** stores **`[/<name>](coddy-skill:<name>)`** in the composer draft.
- While the draft is non-empty, **`Composer`** draws a **mirror layer** (see **Caret sync** below) and highlights slash tokens parsed by **`segmentComposerSlashSpans`** (**`external/ui/src/ui/skills/segmentComposerSlashSpans.ts`**) with **`span.composer-skill-chip-inline`** (**`data-testid="composer-skill-chip"`**).
- **`user_message`** bubbles show persisted text as-is (**`UserMessage`**, **`msg-user-body`**, **`white-space: pre-wrap`**). Skill mirror chips apply only in **`#composer`**, not in the transcript.
- **`Escape`** closes the menu; **`Enter`** confirms the first row when results are loaded and the menu is open (same turn as **`/`** autocomplete).

### Line-range picker (**`@path:N-M`**)

A workspace mention can narrow a file to a **1-based inclusive** line range, **`@Dockerfile:21-31`**. The composer text stays the only input: typing the **`:`** after a file token closes the **`@`** file picker (**`:`** is not a **`MENU_PATH_CHAR`**) and the range panel takes its place, reusing the picker chrome one for one - **`slash-menu-surface`** plus **`slash-menu-scroll`**, the **`slash-menu--portal`** node under **`document.body`** on desktop and the **`slash-menu--sheet`** bottom sheet with **`slash-sheet-backdrop`** on stacked shells. Draft logic lives in **`external/ui/src/ui/skills/draftAtRange.ts`**; functional checklist in **`docs/ui.md`** (**Line ranges**).

- **Automation**: root **`data-testid="at-range-picker"`** with **`role="group"`** (the file and slash pickers keep **`listbox`**), rows **`data-testid={`at-range-line-${n}`}`** with **`data-line`**, list **`data-testid="at-range-lines"`**, current range **`data-testid="at-range-current"`**.
- **Title row** is the shared **`slash-menu-title`**: the caption, then the path in **`.at-range-title-path`** (600 weight, no uppercase transform), then the typed range as an accent pill **`.at-range-title-range`** (**`color: var(--accent)`** on a 16% accent tint, tabular numerals). A hint line (**`.at-range-hint`**, **`slash-muted`**) explains the entry: type the range or, on desktop, click and drag the rows; the mobile copy only asks to type it.
- **Rows** (**`.at-range-lines`** > **`.at-range-line`**) are one logical line each in the monospace stack (**`--mono`**, 12px, line-height 1.5): a right-aligned gutter **`.at-range-line-no`** (min 3ch, 50% opacity, tabular, not selectable) and the code in **`.at-range-line-code`** with **`white-space: pre`**. Long lines never wrap; **`.at-range-lines`** scrolls horizontally inside the panel so the page keeps **`scrollWidth === clientWidth`**. Selected rows use **`.at-range-line--sel`** (accent at 18% over **`--coddy-blend-base`**); a start typed without an end highlights that one line.
- **Desktop** rows are **`button`**s (pointer cursor, 6% text-tint hover): **`mousedown`** anchors the range and calls **`preventDefault`** so focus stays in **`#composer`**, dragging over rows extends it, and every step rewrites the **`:N-M`** suffix through **`replaceAtRangeSuffix`**. On **`isMobileShell`** the rows are plain **`div`**s with no hover or pointer affordance - there is no mouse to drag with, so the range is typed.
- The panel counts as **open** only once the file loaded (**`GET /coddy/workspace/file`**, one fetch per settled path), so a colon in prose never flashes an empty panel; a path that does not resolve leaves it closed. **`Escape`** dismisses it for that mention, **`Enter`** is left alone and still sends. The **`@`** file menu gains one footer line **`.at-range-menu-hint`** (top border at 12% text tint) telling the user that **`:`** after a file picks lines.
- Truncation: the route caps the preview (default 2000 lines); a truncated file adds a **`slash-muted`** line naming how many of the total lines are shown.

#### Composer mirror and caret sync (contract)

The textarea uses **transparent** glyphs when the draft is non-empty; the user-visible line is the **mirror** (`.composer-mirror-inner`) that must be **pixel-aligned** with **`#composer`** for the same string. The **caret** position is computed **only** by the textarea engine on the raw characters, so any styling in the mirror that **changes horizontal advance** of the same code points **breaks** perceived caret placement.

Rules for **`.composer-skill-chip-inline`** (composer only):

- **MUST** use the **same effective font metrics** as **`#composer`**: **`font-family`**, **`font-size`**, **`line-height`**, **`font-weight`**, **`letter-spacing`**, **`font-style`** inherited or explicitly matched (current gate: **400** weight on both mirror and textarea in **`styles.css`**).
- **MUST NOT** add **horizontal** **`padding`**, **`margin`**, or a **`border`** that participates in the inline box width. Use **`box-shadow: 0 0 0 1px …`** for a ring and **`padding: 0`**, **`margin: 0`** so chip width tracks the underlying `/name` glyphs.
- **MUST** keep **`scrollbar-gutter: stable`** on **`#composer`** and mirror **`padding-right`** adjusted for scrollbar width (**`ResizeObserver`** in **`Composer.tsx`**) so wrapped lines do not drift.

Transcript chips (**.md .coddy-skill-chip**) are **not** bound by this contract; they may use monospace, heavier weight, and pill padding because they are not paired with a transparent textarea.

Full browser checks against a running **`coddy serve`** instance (including a **mobile viewport**) use **Playwright MCP** in Cursor, Codex or any other code agent you use. This repository does not ship **`@playwright/test`** as an npm dependency.

**Frosted glass (Playwright MCP smoke)** - after **`npm run dev`** under **`external/ui/`** (or **`coddy serve`** with **`make build TAGS="http ui"`**), use **`browser_tabs` / `browser_navigate`** to the SPA, then **`browser_evaluate`** **`getComputedStyle(...).backdropFilter`** and **`.backgroundColor`** on:

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
| UC8 | Live **`coddy serve`** after **`make build TAGS="http ui"`**, **`#composer`** with **`/coddy_slash_demo`** | **`textarea.value`** plain; **`fontFamily`** chip **===** **`#composer`**; EOL **`selectionStart === value.length`** | **Playwright MCP** · **`browser_navigate`**, **`browser_fill_form`**, **`browser_evaluate`** |

### Markdown

**Assistant** messages may contain Markdown. **User** bubbles do not (plain **`pre-wrap`** text).

- **`coddy-skill:`** chips appear only in the **composer mirror** while editing, not in persisted user bubbles.
- Render fenced code blocks with syntax highlighting.
- Each code block has a copy button in the top right corner that copies only the block contents.

### Memory tree (deferred explorer)

A file-tree over combined **global** (**`memory.dir`** / `$CODDY_HOME/memory`) and **workspace** (`<cwd>/memory`) remains out of scope for this milestone.

### Memory copilot transcript

When **`memory.enable`** is true, each user turn can show a **`memory`** grey foldout styled like **thinking** (`thinking-row` / `thinking-details` / `thinking-body`), placed **after** that user bubble and **before** the main assistant stream for the same turn. Expanded content has **Recalled** and **Memorized** subheads, optional streamed reasoning and answer text, duration in the summary row, and **`data-testid="memory-copilot-row"`** for automation.

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

### Swarm screen (`ui/swarm/SwarmView.tsx`)

Shown at **`#/swarm`** in any environment that answers **`GET /swarm/info`**, and as the **home
screen** when the environment is a relay itself. It is the only screen a relay has, and the map
is the screen: there is no list of nodes under it, because everything the list did the map does.

- **Relay as home.** A relay serves no **`/coddy/*`** at all: it holds no sessions, no workspace and
  no model. **`App.tsx`** tracks this as **`atSwarmRoot`** (the swarm probe answered and the
  environment is not a node reached *through* a relay). While it is true the composer and
  **`ChatScreen`** are not rendered, and the rail hides **History** and **Scheduler** - a drawer of
  sessions that cannot exist is furniture for a room nobody can enter.
- **The map is the way in.** Clicking a node in the graph connects to it: the app repoints at that
  node through the relay (**`connectSwarmNode`**) and every ordinary screen - chat, History,
  Scheduler - then works against it. The attached relay and a node with no route are not
  clickable. Enter and Space do what a click does, and an enterable node takes a visible focus
  ring.
- **Where we are, and how we got there.** **`returnToSwarm`** carries the node last entered back to
  the relay environment as **`swarmFrom`**, so the map can mark it: that node is drawn as *you are
  here* and every edge on its route from the attached relay is drawn as the live path, with
  everything off the route receding. Hovering or focusing another node previews its route the
  same way, weaker.
- **What the swarm is doing.** **`GET /swarm/sessions`** carries **`turnActive`** and
  **`permissionPending`** per session; **`nodeActivity`** (**`swarm/routes.ts`**, pure and tested)
  folds them per node path. Under each node's meta line: nothing when it holds no sessions,
  a session count when it is idle, a running count when a turn is in flight, and *needs an answer*
  when anything there waits on a permission prompt - that state wins, because it is the one that
  needs a human. A running node pulses slowly, a waiting node pulses sharply in a different
  rhythm, and the hops on the route to a running node carry a travelling dash. Every one of those
  is driven by that live data and by nothing else, and
  **`@media (prefers-reduced-motion: reduce)`** removes all of it, leaving the states carried by
  the copy and a static ring.
- **Header.** Title (relay name) and a subtitle counting relays, agents and offline nodes, then
  **`.swarm-header-actions`** holding the **`headerSlot`** - **`App.tsx`** passes
  **`<EnvironmentChip/>`** there at the relay root, because the composer that normally carries it
  is not on screen.
- **The click lands on the question.** Spotting on the map that a box is asking is half the job:
  clicking a node whose sessions include one waiting on a permission prompt opens *that* session,
  then one with a turn in flight, freshest first; only a node with neither opens its own home
  (**`sessionToOpen`** in **`swarm/routes.ts`**, pure and tested). The graph box also scrolls
  itself to the current node, so a narrow shell opens on the branch you are on rather than on an
  empty gutter.
- **Search, not filter.** The box goes to the relay, which fans out, so a query reaches machines
  this browser cannot dial. With a query, matching sessions appear as rows under the map, each
  naming its node and route, and a row opens that session on that node. With no query there are
  no rows at all.
- **Distinct empty and error states.** **`/swarm/info`** is public, so a credentialed relay answers
  the probe and refuses everything else; the view then shows **`.swarm-error`** asking for a token
  rather than reporting an empty swarm. Nodes that did not answer are listed in
  **`.swarm-warnings`** above the map rather than silently dropped.
- **The topology graph** is a hand-rolled SVG (**`TopologyGraph.tsx`**, layout in
  **`swarm/layout.ts`**). A relay is a card carrying an accent-filled tile with the router mark; an
  agent is a circle with its name on a chip below. Hops are orthogonal elbows that leave the
  bottom, turn on a rail shared by the children of one parent, and arrive at the top with an
  arrowhead; a same-tier link between peers is a bow that leaves and arrives at the sides, with
  its name at the apex; a link that skips a row goes round the outside in a lane clear of every
  card. Depth is stated in the left gutter - a dashed upright with a tick, a hop caption and a
  node count per tier. Every state is said twice, never in colour alone: the route in use is solid
  with a filled head, a way round a ring is dotted with an open chevron, a link into an offline
  node is coarsely dashed and dimmed, and a node that dials out carries a badge as well as a
  dotted wire. Because **`role="img"`** collapses the subtree, the SVG is described by a visually
  hidden paragraph naming each tier, its nodes, where the app is and what is running; marker ids
  are **`useId()`**-scoped so two graphs on one page cannot collide. The SVG keeps its intrinsic
  size and scrolls inside **`.swarm-graph-scroll`** rather than scaling its labels below
  legibility on a phone, and the legend below it wraps instead of setting a minimum width.

The relay serves this SPA from its own address when built with **`-tags "swarm ui"`**
(**`external/swarm/spa_ui.go`**), so a relay is something you open in a browser rather than a
service you reach through some other node's UI.

## States

- Idle composer: bordered textarea.
- Streaming assistant: progressively grows final assistant bubble; token HUD updates concurrently.
- Error streaming: surfaced inside assistant transcript with HTTP status text fallback.
- **Working, with no assistant text yet**: the three typing dots carry a **live status line** to their right (**`TypingDotsMessage.tsx`**, classes **`typing-dots-status*`**). It names the current step — verb plus its target, for example **`Reading external/ui/src/ui/App.tsx`** or **`Running npm test`** — followed by an elapsed counter (**`· 12s`**). Derivation is pure and lives in **`chat/liveStatus.ts`**: it reads the transcript the SPA already holds, scanning back to the last **`user_message`** so a stale row from a finished turn cannot drive the label. Priority is unresolved **permission** prompt → unresolved **question** prompt → running **tool_call** → in-progress **thinking** (**`Thinking…`**) → **memory_copilot** → waiting on the model. The two prompt states show **no counter**: nothing is running, so a climbing number would be a lie (same reasoning as the frozen **`ToolCallMessage`** timer). A plain wait escalates its phrase with time: **`Waiting for the model`** → **`The model is taking longer than usual`** (15s, **`typing-dots-status--slow`**) → **`Still no response from the server`** (60s). Layout: only the **target** ellipsizes when space runs out — the verb and the counter are **`flex: none`**, so the row degrades rather than wrapping or clipping the verb. The status node **must** stay after the three **`.typing-dots-dot`** spans, whose **`:nth-child(2)/(3)`** rules carry the bounce stagger. The interactive console (**`external/cli`**) shows the same phrases next to its spinner, from the Go twin of the table in **`external/cli/status.go`**.

## Non-goals for this milestone

Server-side SSR routes per session, BFF auth, CDN-hosted Swagger, and editing **`agentMemory`** via REST remain out-of-scope (`session.json` slot remains agent-managed).

## Dev workflow

To iterate on UI without rebuilding the Go binary:

Backend:

```bash
make build TAGS=http
./build/coddy serve --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

Frontend:

```bash
npm --prefix external/ui install
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```
