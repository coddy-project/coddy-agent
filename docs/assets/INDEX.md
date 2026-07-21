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

## Full HD tour (README, July 2026)

Captured at **1920×1080** via Playwright MCP against the embedded SPA (`make build TAGS="http ui scheduler memory"` + `coddy http` on a disposable `--home`). Mobile shots at **390×844**. Theme is the default **Dark** (cookie `coddy_ui_theme=dark`; switch via Settings → Appearance). Provider `api_key` is left empty in the demo config and supplied via `RPA_API_KEY` so no secret is rendered. Re-captured **2026-07-21** (0.9.40 + context-compaction branch): the composer now shows the environment chip (**Local**) plus workspace / branch / worktree chips; the settings sidebar includes **Context compaction**; demo scheduler jobs use `cwd: /workspace/demo-api` so no host paths leak into shots.

- `screenshot-fullhd-start.png` - new chat / hero start screen (README, above fold)
- `screenshot-fullhd-chat.png` - active session transcript with agent tool/thinking/memory rows (`#/s/...`)
- `screenshot-fullhd-history.png` - History drawer on a session (`#/s/...?history=1`)
- `screenshot-fullhd-scheduler.png` - scheduler list plus job editor (`#/scheduler/jobs/nightly-docs`)
- `screenshot-fullhd-scheduler-job.png` - job settings editor dialog only (element shot of `scheduler-editor-panel`)
- `screenshot-fullhd-settings.png` - settings drawer, tabbed nav, LLM providers tab (`#/settings/providers`)
- `screenshot-fullhd-settings-skills.png` - settings Skills tab (dirs + installed skills)
- `screenshot-fullhd-settings-appearance.png` - settings Appearance tab (7 theme swatches)
- `screenshot-mobile-start.png`, `screenshot-mobile-chat.png` - 390×844 top-bar shell

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
