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

// Regression: blockquote text used a hardcoded near-white color
// rgba(229, 231, 235, 0.9) with no light-theme override, making quotes
// invisible on the light canvas (#f8f8fa). Text color must be theme-aware.
test("markdown blockquote text color is theme-aware (readable on light)", () => {
  const css = cssText();
  // Anchor to the standalone rule (not the `.thinking-body .md ...` variant,
  // which is already theme-aware).
  const block = /^\.md :where\(blockquote\)\s*\{[^}]+\}/m.exec(css);
  expect(block).not.toBeNull();
  // Must not hardcode the near-white color that vanishes on the light canvas.
  expect(block![0]).not.toMatch(/color:\s*rgba\(\s*229,\s*231,\s*235/);
  // Must derive from the theme --text token so it flips with the theme.
  expect(block![0]).toMatch(/color:\s*color-mix\(in srgb,\s*var\(--text\)/);
});
