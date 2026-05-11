import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(dirname(fileURLToPath(import.meta.url)), "styles.css");

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

test("styles define shared coddy frosted glass tokens", () => {
  const css = cssText();
  expect(css).toMatch(/--coddy-glass-panel-bg:/);
  expect(css).toMatch(/--coddy-glass-panel-backdrop:/);
  expect(css).toMatch(/--coddy-glass-panel-radius:/);
  expect(css).toMatch(/--coddy-overlay-scrim-bg:/);
  expect(css).toMatch(/--coddy-z-slash-command:/);
});

test("composer, history, scheduler, slash frosted surface, mode or model menus share panel rule", () => {
  const css = cssText();
  expect(css).toMatch(
    /\.composer-card,\s*\n\.sessions\.drawer,\s*\n\.scheduler-jobs\.drawer,\s*\n\.scheduler-job-editor-dock,\s*\n\.slash-menu-surface,\s*\n\.mode-menu\s*\{/,
  );
  expect(css).toContain("var(--coddy-glass-panel-bg)");
});

test("history backdrop dims only — no fullscreen blur", () => {
  const css = cssText();
  const backdropBlock = /\.backdrop\s*\{[^}]+\}/m.exec(css);
  expect(backdropBlock).not.toBeNull();
  expect(backdropBlock![0]).not.toMatch(/backdrop-filter/);
});

test("slash mobile sheet scrim dims only — no fullscreen blur", () => {
  const css = cssText();
  const block = /\.slash-sheet-backdrop\s*\{[^}]+\}/m.exec(css);
  expect(block).not.toBeNull();
  expect(block![0]).not.toMatch(/backdrop-filter/);
});
