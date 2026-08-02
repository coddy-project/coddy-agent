import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(dirname(fileURLToPath(import.meta.url)), "../../styles.css");

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

test("canvas background uses theme variables", () => {
  const css = cssText();
  expect(css).toMatch(/--coddy-canvas-gradient-bottom:/);
  expect(css).toMatch(/background-color:\s*var\(--coddy-canvas-gradient-bottom\)/);
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

test("styles.css defines variable blocks for all 7 themes", () => {
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
  const themes = ["dark", "light", "midnight", "solarized-dark", "monokai", "nord", "rose-pine"];
  for (const t of themes) {
    const block = new RegExp(`\\[data-theme="${t}"\\][^{]*\\{[^}]*--accent:[^}]*\\}`, "s");
    expect(css, `${t} should have --accent`).toMatch(block);
  }
});
