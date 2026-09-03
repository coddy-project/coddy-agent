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

/** Every rule block whose declarations enable content-visibility on transcript rows. */
function containmentBlocks(
  css: string,
): Array<{ selector: string; body: string }> {
  const out: Array<{ selector: string; body: string }> = [];
  const noComments = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const re = /([^{}]+)\{([^{}]*content-visibility:\s*auto[^{}]*)\}/g;
  for (const m of noComments.matchAll(re)) {
    out.push({ selector: m[1]!.trim(), body: m[2]! });
  }
  return out;
}

test("off-screen row containment opts in per row type", () => {
  const blocks = containmentBlocks(cssText()).filter((b) =>
    b.selector.includes(".messages-inner"),
  );
  expect(blocks.length).toBe(1);
  const selectors = blocks[0]!.selector.split(",").map((s) => s.trim());
  expect(selectors).toEqual(
    expect.arrayContaining([
      ".messages-inner > .msg-assistant-stack",
      ".messages-inner > .thinking-row",
      ".messages-inner > .msg-system-stack",
    ]),
  );
  // `contain-intrinsic-size: auto` keeps the last rendered height, so the
  // scroll position does not jump when rows re-enter the viewport.
  expect(blocks[0]!.body).toMatch(/contain-intrinsic-size:\s*auto\s+\d+px/);
});

test("rows that paint outside their own box are never contained", () => {
  // Paint containment clips the user-row hover edit button (left: -32px) and
  // the permission / question / plan card shadows, so the rule must not match
  // every child of the transcript or any of those wrappers.
  const selectorText = containmentBlocks(cssText())
    .map((b) => b.selector)
    .join(",");
  expect(selectorText).not.toMatch(/\.messages-inner\s*>\s*\*/);
  for (const forbidden of [
    ".msg-user-stack",
    ".message-row-permission",
    ".message-row-question",
    ".message-row-plan",
    ".branch-nav",
  ]) {
    expect(selectorText).not.toContain(forbidden);
  }
});

test("the user-row edit button still hangs outside the bubble", () => {
  // Documents the constraint the containment rule has to respect.
  const rule = cssText().match(/\.msg-user-edit\s*\{[^}]*\}/s);
  expect(rule?.[0]).toMatch(/position:\s*absolute/);
  expect(rule?.[0]).toMatch(/left:\s*-\d+px/);
});
