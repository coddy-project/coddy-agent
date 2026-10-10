import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The active count of History and Scheduler sits over the item's icon in
 * every shape of the rail: icon-only, labelled (wide) and the rows of the
 * phone's More menu. It is placed from the icon (.rail-nav-icon), never from
 * the row: placed from the row, the wide rail's count ran to the far end of
 * the label and the More menu's stood after the text.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function ruleBody(selector: string): string {
  const idx = css.indexOf(`\n${selector} {`);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return css.slice(open + 1, css.indexOf("}", open)).replace(/\s+/g, " ");
}

test("the icon is what the count is placed from", () => {
  const icon = ruleBody(".rail-nav-icon");
  expect(icon).toContain("position: relative");
  // The wide rail's grid places the wrapper, centred in the icon track.
  expect(icon).toContain("justify-self: center");
});

test("the count hangs off the icon's top right corner", () => {
  const count = ruleBody(".rail-active-count");
  expect(count).toContain("position: absolute");
  expect(count).toMatch(/top: -\d+px/);
  expect(count).toMatch(/right: -\d+px/);
});

test("no rule puts a count after a label", () => {
  expect(css).not.toContain(".rail-more-active-count");
  expect(css).not.toContain(".rail-active-count--inline");
  expect(css).not.toMatch(/\.rail-active-count[^{]*\{[^}]*margin-left: auto/);
});
