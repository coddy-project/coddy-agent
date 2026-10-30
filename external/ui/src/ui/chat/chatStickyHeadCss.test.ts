import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

function cssText(): string {
  return stylesSource();
}

// The session title remains pinned at the top of the scroll viewport.
test("pinned chat title head stays sticky", () => {
  const css = cssText();
  const block = /\.chat-scroll-sticky-head\s*\{[^}]+\}/m.exec(css);
  expect(block).not.toBeNull();
  // Sticky head keeps position: sticky (pinned).
  expect(block![0]).toMatch(/position:\s*sticky/);
});
