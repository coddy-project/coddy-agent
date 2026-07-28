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

// Regression: `.skills-list-item` is a flex row and the leading skill icon
// (a bare 16x16 <svg>) had no `flex-shrink: 0`, so a long name/description
// squeezed the icon horizontally, rendering it slanted/crooked. Pin it square.
test("skills list leading icon does not shrink in the flex row", () => {
  const css = cssText();
  expect(css).toMatch(
    /\.skills-list-item\s*>\s*svg\s*\{[^}]*flex-shrink:\s*0/s,
  );
});

// Contract: the install-results menu floats (absolute) over the installed list
// so it never reflows the rows beneath it, and its anchor is positioned.
test("install results dropdown floats over the list, anchored to the control", () => {
  const css = cssText();
  expect(css).toMatch(/\.skills-install\s*\{[^}]*position:\s*relative/s);
  expect(css).toMatch(/\.skills-install-results\s*\{[^}]*position:\s*absolute/s);
  // Sits above the following static list rows.
  expect(css).toMatch(/\.skills-install-results\s*\{[^}]*z-index:\s*\d+/s);
});
