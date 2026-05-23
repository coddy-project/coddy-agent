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

test("index.html bootstraps theme before paint", () => {
  const html = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../index.html"),
    "utf8",
  );
  expect(html).toContain("coddy_ui_theme");
  expect(html).toContain('dataset.theme = "light"');
});
