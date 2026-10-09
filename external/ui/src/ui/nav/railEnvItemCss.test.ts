import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The environment item of the wide rail stands on the same column as every
 * other item: an item's icon is centred in the 44px track of the wide grid
 * (.rail-nav-hit-svg), and the environment's icon is wrapped with its status
 * dot (.rail-env-icon), so the wrapper is what the grid places and what has to
 * be centred. Left to the grid's justify-items: start, it sat at the left edge
 * of the track, off the column of Docs and Settings above it.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function ruleBody(selector: string): string {
  const idx = css.indexOf(`${selector} {`);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return css.slice(open + 1, css.indexOf("}", open)).replace(/\s+/g, " ");
}

test("the wide rail centres an item's icon in its track", () => {
  expect(ruleBody(".rail-nav-hit-svg")).toContain("justify-self: center");
});

test("the environment item centres its icon with its status dot", () => {
  expect(ruleBody(".rail-env-icon")).toContain("justify-self: center");
});

test("a long environment name is cut, not widening the rail", () => {
  const body = ruleBody(".rail-env-label");
  expect(body).toContain("overflow: hidden");
  expect(body).toContain("text-overflow: ellipsis");
});
