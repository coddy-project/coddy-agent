import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.scss",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

function block(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, `missing rule ${selector}`).not.toBeNull();
  return m![1] ?? "";
}

test("follow-up prompts balance the footer room with shared spacing variables", () => {
  const css = cssText();
  const stack = block(css, ".msg-user-stack");
  expect(stack).toMatch(/--user-foot-height:\s*28px\s*;/);
  expect(stack).toMatch(/--user-foot-gap:\s*6px\s*;/);
  expect(stack).not.toMatch(/padding-top\s*:/);
  const followup = block(css, ".msg-user-stack--followup").replace(/\s+/g, " ");
  expect(followup).toContain(
    "padding-top: calc(var(--user-foot-height) + var(--user-foot-gap));",
  );
  const foot = block(css, ".msg-user-foot");
  expect(foot).toMatch(/min-height:\s*var\(--user-foot-height\)\s*;/);
  expect(foot).toMatch(/margin-top:\s*var\(--user-foot-gap\)\s*;/);
});

// Regression: the copy control and the timestamp under a user bubble hugged
// the bubble's right edge, while the same row under an assistant or a system
// message is inset by the message card's horizontal padding. The user row is
// inset by that amount on the right, so the three rows keep one margin.
test("user message action row is inset by the message card's horizontal padding", () => {
  const css = cssText();
  const card = block(css, ".msg");
  const cardPadding = /padding:\s*(\d+)px\s+(\d+)px\s*;/.exec(card);
  expect(
    cardPadding,
    ".msg keeps a vertical/horizontal padding pair",
  ).not.toBeNull();
  const horizontal = cardPadding![2];
  const foot = block(css, ".msg-user-foot");
  expect(foot).toMatch(new RegExp(`padding-right:\\s*${horizontal}px\\s*;`));
  expect(foot).toMatch(/box-sizing:\s*border-box\s*;/);
});
