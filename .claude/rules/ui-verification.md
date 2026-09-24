---
description: UI layout checks before claiming work is done
paths:
  - "external/ui/**/*"
---

# UI verification (Playwright MCP)

Before you tell the user that a UI change is complete or merge-ready:

1. Start **`npx vite`** in **`external/ui`** on a free port (for example **`127.0.0.1:5201`**).
2. Use **Playwright MCP** (or the repo browser tools) to open **`/layout-scroll-check.html`** and **`/`** with at least two viewports: **narrow** (for example **390px**) and **wide** (for example **1280px**).
3. Run **`browser_evaluate`** to compare **`getBoundingClientRect()`** edges for **`.chat-header`**, **`.messages-inner`** first child, and **`.composer-card`** (left and right should match within about **1px** after shared column padding).
4. If anything is off, fix CSS and re-check before reporting done.

This is required for changes to **`external/ui/src/styles.css`**, **`ChatScreen`**, **`Composer`**, or nav layout.

## Safari and other WebKit reports

A Safari-only report is reproducible without a Mac: Playwright's WebKit is the engine Safari is cut
from and its version tracks Safari's. When the change touches a capped, scrollable or **`position:
fixed`** surface, run **`external/ui/scripts/webkit-scroll-check.mjs`** against a live
**`coddy serve`** (setup and env vars in **`docs/surfaces/web-ui.md`**, *Reproducing a Safari report without a
Mac*), and run it again with **`CODDY_ENGINE=chromium`** to tell a WebKit-only regression from a
layout bug every engine shares.

## The transcript at every width of the grid

Nothing in the transcript may be wider than the transcript: on the stacked shell its column is the
page, and one row that cannot wrap makes a phone scroll sideways (**`DESIGN.md`**, *Layout grid*,
*Tool card UI*, *Markdown*). When the change touches the transcript's rows, the Markdown styles or a
width query, run **`external/ui/scripts/phone-overflow-check.mjs`** against a **`vite`** dev server
(setup in **`docs/surfaces/web-ui.md`**, *Checking the transcript at every width of the grid*): it
mounts the rows that used to overflow and fails when the page scrolls sideways or anything sticks out
of **`.messages-inner`** at 360 to 1280px. A new width query goes into the grid first; the vitest
**`layoutGridCss.test.ts`** fails on one the grid does not name.

## The fold chevron

The chevron is an SVG whose ink is centred in its viewBox, never a text glyph: a glyph's ink moves
with the platform's font, which is why the reports of a chevron riding above its label kept coming
back (**`DESIGN.md`**, *Chevron*). jsdom has no layout, so vitest cannot see where it lands. When the
change touches the chevron, the rows it sits on or the type around them, run
**`external/ui/scripts/chevron-align-check.mjs`** against a **`vite`** dev server (setup in
**`docs/surfaces/web-ui.md`**, *Checking the fold chevron against its label*): it measures the
chevron's ink centre against the label's on a transcript row and on the Tasks drawer toggle, and
fails past **1px**.
