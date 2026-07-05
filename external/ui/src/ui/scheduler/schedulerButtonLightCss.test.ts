import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// Regression: `.scheduler-btn` hardcoded a dark translucent surface
// (`rgba(20, 20, 22, 0.45)`) with no light override, so the footer Pause/Trash
// buttons rendered as muddy grey boxes on the Light theme.
test("light theme overrides the scheduler button surface", () => {
  const css = cssText();
  expect(css).toMatch(
    /\[data-theme="light"\]\s*\.scheduler-btn\s*\{[^}]*background:/s,
  );
});

// Regression: `.scheduler-btn-danger` used the dark pale-pink text
// (`rgba(254, 202, 202, …)`), which washed out to near-invisible on the Light
// theme. The Trash button needs a readable red instead.
test("light theme gives the danger (Trash) button a readable colour", () => {
  const css = cssText();
  const rule = css.match(
    /\[data-theme="light"\]\s*\.scheduler-btn-danger\s*\{[^}]*\}/s,
  );
  expect(rule).toBeTruthy();
  expect(rule![0]).toMatch(/color:/);
  expect(rule![0]).not.toMatch(/rgba\(254/);
});
