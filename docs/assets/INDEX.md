# UI reference images

This folder contains reference screenshots used to align the embedded UI with the target design.

## Navbar (RPA-style references, May 2026)

Implementation note: **Coddy does not render a circle or logo glyph** before the **Coddy agent** brand in the embedded SPA. SVG logos under **`coddy-logo-*.svg`** are for README, **`logo-preview.html`**, and favicon (**`coddy-favicon.svg`** aliases **`coddy-logo-mark-flat.svg`**, same asset as [coddy.dev](https://coddy.dev/) **`assets/coddy-favicon.svg`**). Raster favicons **`favicon-32.png`**, **`favicon.ico`**, **`apple-touch-icon.png`** ship with the embedded SPA at the site root. **`coddy-logo-mark-icon.svg`** is square full-bleed plate fill with no rim stroke or corner radius; **`coddy-logo-mark-icon-2048.png`** is a 2048×2048 raster export; **`coddy-logo-social.svg`** (1280×640) is the GitHub repository social preview with wordmark and tagline, with **`coddy-logo-social-1280x640.png`** and **`coddy-logo-social-640x320.png`** raster exports; **`coddy-logo-mark.svg`** adds halo filters. Some references still show a circle, treat it as layout inspiration only.

- `ref-navbar-narrow-tooltips-accent.png` - narrow vertical rail, tooltips right, purple hover on icon
- `ref-navbar-narrow-icons-only.png` - narrow rail, icons only (Coddy uses History + GitHub + API, not News or Projects)
- `ref-navbar-wide-with-labels.png` - wide rail with text labels next to items

## Playwright MCP (verification, May 2026)

Captured from local `vite` + `coddy http` with `CODDY_UI_BACKEND`.

- `pw-navbar-1440-narrow.png` - desktop under 1920px width, narrow rail (no widen toggle), no burger
- `pw-navbar-1440-history-hover.png` - History hover / pressed accent and tooltip styling
- `pw-navbar-1920-wide-labels.png` - min-width 1920px, wide rail (**rectangular panel**, rounded on the right only), header with **collapse** (stacked lines) plus **Coddy agent** text-only brand, full-width rows icon plus label
- `pw-navbar-1920-github-hover.png` - wide rail, hover on **GitHub** row (label plus icon pick up accent)
- `pw-navbar-390-mobile-topbar.png` - max-width 1199px shell, rail as top bar row
- `pw-navbar-390-sessions-drawer.png` - History opens chats drawer overlay

## Full HD tour (README, re-captured August 2026)

Captured at **1920×1080** through Playwright against the embedded SPA (`make build TAGS="http ui scheduler memory cli"` + `coddy http` on a disposable `CODDY_HOME`), mobile at **390×844**, default **Dark** theme, browser locale **en-US** so no shot lands in another language. Re-captured **2026-08-17** on **0.9.71**: the composer carries the attach button and the improve-prompt wand, chips wrap individually on narrow viewports, Appearance holds the language picker, and the scheduler job editor uses the shared markdown line editor.

The disposable home lives at a presentable path (`/home/pasha/demo/coddy-home` at capture time) because the Skills tab prints the resolved `skills.dirs`. **Never capture the LLM provider detail pane**: it renders `api_key` values in full. The provider master list (names only) is safe, which is why the `providers` tab is not part of this set.

- `screenshot-fullhd-start.png` - new chat / hero start screen (README, above fold)
- `screenshot-fullhd-chat.png` - session transcript with an expanded `edit` tool call showing a real diff
- `screenshot-fullhd-history.png` - History drawer over the start screen
- `screenshot-fullhd-scheduler.png` - scheduler drawer, three jobs, one paused
- `screenshot-fullhd-scheduler-job.png` - drawer plus the job editor (cron hint, mode/model, markdown body)
- `screenshot-fullhd-settings.png` - settings sheet, tabbed nav, **ReAct agent** tab
- `screenshot-fullhd-settings-skills.png` - settings Skills tab (dirs, remote sources, installed skills)
- `screenshot-fullhd-settings-appearance.png` - settings Appearance tab (7 theme swatches plus language picker)
- `screenshot-fullhd-settings-mcp.png` - settings MCP tab: a connected global server and a project-local one awaiting workspace approval
- `screenshot-fullhd-tasks.png` - background tasks panel docked in a session, one task running
- `screenshot-fullhd-branches.png` - `‹ 2/2 ›` branch navigator under an edited user message
- `screenshot-mobile-start.png`, `screenshot-mobile-chat.png` - 390×844 top-bar shell

## Console TUI (README and coddy.dev, August 2026)

Captured **2026-08-17** from a real **Konsole** window on an isolated Xvfb `:99` (staged by `demo-videos/rig/stage_konsole.sh`, driven with XTEST), 1920 px wide and cropped to the used rows because the TUI renders inline from the top. Deterministic pyte-rendered counterparts live in `cli-tui/`; pi originals in `pi-tui-reference/`. See `docs/cli.md` (**Captures**).

- `screenshot-console-start.png` - the `coddy` launch line plus header, `[Context]`, `[Skills]`, editor, footer
- `screenshot-console-models.png` - `ctrl+l` model selector
- `screenshot-console-chat.png` - finished turn: `read` tool box, thinking block, markdown answer, footer counters

## Tool approval previews (July 2026)

Captured from the real permission-prompt and expanded transcript-card components
with representative Coddy filesystem, search, shell, and patch tool payloads.

- `screenshot-tool-previews-light.png` - approval prompts and expanded tool cards in the Light theme
- `screenshot-tool-previews-dark.png` - approval prompts and expanded tool cards in the Dark theme
- `screenshot-tool-previews-overflow-light.png` - collapsed `More…` and expanded `Less` states in the Light theme
- `screenshot-tool-previews-overflow-dark.png` - collapsed `More…` and expanded `Less` states in the Dark theme

## Persisted image attachments (August 2026)

Captured from the embedded SPA against a disposable NeuralDeep-backed session.

- `pr-98-disabled-attachment-1280-dark.png` - wide Dark composer after switching an attached image to a non-multimodal model
- `pr-98-disabled-attachment-390-dark.png` - narrow Dark variant
- `pr-98-disabled-attachment-390-light.png` - narrow Light variant
- `pr-98-persisted-thumbnail-1280-dark.png` - sent image thumbnail restored from the session backend after a full reload

## Primary

- `ref-home-1.png` - landing page with collapsed left rail and centered composer
- `ref-home-composer.png` - expanded left menu and composer action area
- `ref-chat.png` - in chat view with floating composer and left rail
- `ref-wide-1.png` - wide desktop layout with expanded left nav and sessions list
- `ref-wide-2.png` - wide desktop layout variant
- `ref-wide-3.png` - wide desktop layout with session context menu

## Mobile

- `ref-image-098475fd-f1e8-4722-9975-67890f85a2c8.png` - mobile rail states and expanded menu

## Batch uploads

Files named `ref-image-*.png` are direct uploads from chat. They are kept as source of truth.
