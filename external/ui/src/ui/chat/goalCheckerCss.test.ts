import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The checker of a goal is the widest value of the goal menu: in one number's
 * column a model id broke into several lines. It closes the numbers' grid on
 * a row of its own that starts at the first column and spans two, so its
 * reasoning level falls into the third column, under the time worked, and
 * every label of the menu stands on the same column lines (DESIGN.md, Session
 * goal in the composer).
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

test("the checker starts a row of the numbers' grid and spans two columns", () => {
  const rule = css.match(/\.goal-numbers-checker\s*\{([^}]*)\}/);
  expect(rule, ".goal-numbers-checker is missing").not.toBeNull();
  expect((rule?.[1] ?? "").replace(/\s+/g, " ")).toContain(
    "grid-column: 1 / span 2",
  );
});

test("the numbers keep three columns at every width", () => {
  // auto-fit dropped to two columns on a phone, and the level fell under the
  // model instead of beside it.
  const rule = css.match(/\.goal-numbers\s*\{([^}]*)\}/);
  expect((rule?.[1] ?? "").replace(/\s+/g, " ")).toContain(
    "grid-template-columns: repeat(3, minmax(0, 1fr))",
  );
});

test("no grid of its own pushes the level to the edge", () => {
  expect(css).not.toMatch(/\.goal-checker-numbers\b/);
});
