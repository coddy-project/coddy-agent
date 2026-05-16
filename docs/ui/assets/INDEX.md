# UI reference images

This folder contains reference screenshots used to align the embedded UI with the target design.

## Navbar (RPA-style references, May 2026)

Implementation note: **Coddy does not render a circle or logo glyph** before the **Coddy agent** brand in the embedded SPA. SVG logos under **`coddy-logo-*.svg`** are for README and **`logo-preview.html`** only. Some references still show a circle, treat it as layout inspiration only.

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
