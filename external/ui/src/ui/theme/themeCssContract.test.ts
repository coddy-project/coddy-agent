import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

function cssText(): string {
  return stylesSource();
}

// The dark theme's accent written as a number painted the frames, rings and glows
// of every other theme purple: a focused field, the active item of the rail, Save.
test("no stylesheet but the dark palette names the dark theme's accent", () => {
  const stylesDir = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../styles",
  );
  const named: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (entry.name.endsWith(".scss") && entry.name !== "_dark.scss") {
        const text = readFileSync(path, "utf8");
        if (/rgba?\(\s*147,\s*51,\s*234|#9333ea/i.test(text))
          named.push(entry.name);
      }
    }
  };
  walk(stylesDir);
  expect(named).toEqual([]);
});

// A light accent (Nord, Rose Pine) cannot carry white text: what stands on a strong
// accent ground takes the colour each theme names for it.
test("text on a strong accent ground takes the theme's colour for it", () => {
  const css = cssText();
  for (const selector of [
    ".settings-btn-primary.is-dirty {",
    ".workspace-modal-btn--primary {",
    ".question-tool-offer-row--taken .question-prompt-bubble {",
  ]) {
    const at = css.indexOf(selector);
    expect(at, selector).toBeGreaterThanOrEqual(0);
    expect(css.slice(at, css.indexOf("}", at)), selector).toContain(
      "color: var(--coddy-on-accent-fg)",
    );
  }
});

test("canvas background uses theme variables", () => {
  const css = cssText();
  expect(css).toMatch(/--coddy-canvas-gradient-bottom:/);
  expect(css).toMatch(
    /background-color:\s*var\(--coddy-canvas-gradient-bottom\)/,
  );
});

test("desktop canvas follows the dynamic viewport in Firefox", () => {
  const css = cssText();
  expect(css).toMatch(/html,\s*body,\s*#root\s*\{[^}]*height:\s*100%/s);
  expect(css).toMatch(/\.shell\s*\{[^}]*height:\s*100dvh/s);
  expect(css).toMatch(/\.rail-column\s*\{[^}]*height:\s*100dvh/s);
});

test("light composer vignette blends into the canvas instead of darkening it", () => {
  const css = cssText();
  const rule = css.match(
    /\[data-theme="light"\]\s+\.chat-bottom:has\(\.composer-wrap-docked\)::before\s*\{[^}]*\}/s,
  );
  expect(rule?.[0]).toMatch(/var\(--coddy-canvas-gradient-bottom\)/);
  expect(rule?.[0]).not.toMatch(/rgba\(11,\s*11,\s*12/);
});

test("index.html bootstraps theme before paint", () => {
  const html = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../index.html"),
    "utf8",
  );
  expect(html).toContain("coddy_ui_theme");
  expect(html).toContain("dataset.theme");
});

test("styles.scss defines variable blocks for all 7 themes", () => {
  const css = cssText();
  const themeSelectors = [
    '[data-theme="dark"]',
    '[data-theme="light"]',
    '[data-theme="midnight"]',
    '[data-theme="solarized-dark"]',
    '[data-theme="monokai"]',
    '[data-theme="nord"]',
    '[data-theme="rose-pine"]',
  ];
  for (const sel of themeSelectors) {
    expect(css).toContain(sel);
  }
});

test("each theme block defines --accent", () => {
  const css = cssText();
  const themes = [
    "dark",
    "light",
    "midnight",
    "solarized-dark",
    "monokai",
    "nord",
    "rose-pine",
  ];
  for (const t of themes) {
    const block = new RegExp(
      `\\[data-theme="${t}"\\][^{]*\\{[^}]*--accent:[^}]*\\}`,
      "s",
    );
    expect(css, `${t} should have --accent`).toMatch(block);
  }
});

test("language select fills narrow settings without overflow", () => {
  const rule = cssText().match(
    /\.appearance-language-select\s*\{[^}]*\}/s,
  )?.[0];
  expect(rule).toMatch(/width:\s*100%/);
  expect(rule).toMatch(/min-width:\s*0/);
  expect(rule).toMatch(/min-height:\s*40px/);
});
