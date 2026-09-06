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

function block(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, `missing rule ${selector}`).not.toBeNull();
  return m![1];
}

// Regression: the action row under a SYSTEM notice sits below the bordered
// card instead of inside it, so its copy control hugged the card's outer edge
// and stood 14px left of the copy control under an assistant row, which
// inherits the card's horizontal padding. The row must be inset by that same
// padding so both controls start at the same x.
test("system notice action row is inset by the message card's horizontal padding", () => {
  const css = cssText();
  const card = block(css, ".msg");
  const cardPadding = /padding:\s*(\d+)px\s+(\d+)px\s*;/.exec(card);
  expect(cardPadding, ".msg keeps a vertical/horizontal padding pair").not.toBeNull();
  const horizontal = cardPadding![2];
  const foot = block(css, ".msg-system-foot");
  expect(foot).toMatch(new RegExp(`padding-left:\\s*${horizontal}px\\s*;`));
});
