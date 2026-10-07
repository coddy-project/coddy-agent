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

// The session title remains pinned at the top of the scroll viewport.
test("pinned chat title head stays sticky", () => {
  const css = cssText();
  const block = /\.chat-scroll-sticky-head\s*\{[^}]+\}/m.exec(css);
  expect(block).not.toBeNull();
  // Sticky head keeps position: sticky (pinned).
  expect(block![0]).toMatch(/position:\s*sticky/);
});
