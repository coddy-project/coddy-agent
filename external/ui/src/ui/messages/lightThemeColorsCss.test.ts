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

// Regression: the dark-theme syntax colors (bright green/blue) had no light
// override, so code blocks read as glaring on the pale code surface.
test("light theme overrides bright syntax green and blue", () => {
  const css = cssText();
  expect(css).toMatch(
    /\[data-theme="light"\]\s*\{[^}]*--syntax-string:\s*#116931/s,
  );
  expect(css).toMatch(
    /\[data-theme="light"\]\s*\{[^}]*--syntax-number:\s*#0d5cad/s,
  );
});

// Regression: .msg-system-time kept the dark-theme pink rgba(255,168,168,...)
// on the light canvas, rendering as a salmon timestamp.
test("light theme overrides the system-message timestamp color", () => {
  const css = cssText();
  expect(css).toMatch(
    /\[data-theme="light"\]\s*\.msg-system-time\s*\{[^}]*color:/s,
  );
});
