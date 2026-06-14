import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(dirname(fileURLToPath(import.meta.url)), "../../styles.css");

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// The session title is pinned (position: sticky) at the top of the scroll
// viewport. Its region must be opaque so transcript text does not bleed
// through it while scrolling (see DESIGN.md, Chat canvas / pinned title).
test("pinned chat title head stays opaque so transcript does not bleed through", () => {
  const css = cssText();
  const block = /\.chat-scroll-sticky-head\s*\{[^}]+\}/m.exec(css);
  expect(block).not.toBeNull();
  // Sticky head keeps position: sticky (pinned) ...
  expect(block![0]).toMatch(/position:\s*sticky/);
  // ... and paints an opaque canvas-colored backing (no see-through).
  expect(block![0]).toMatch(
    /background[^;]*var\(--coddy-canvas-gradient-top\)/,
  );
});
