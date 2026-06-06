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
