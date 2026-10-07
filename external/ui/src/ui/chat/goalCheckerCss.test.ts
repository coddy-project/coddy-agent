import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The checker of a goal is the widest value of the goal menu: in a number's
 * column a model id broke into several lines. It sits after the numbers in a
 * row of its own, the model taking the room and its reasoning level beside it
 * (DESIGN.md, Session goal in the composer).
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

test("the checker row gives the model the room and the level its own width", () => {
  const rule = css.match(/\.goal-checker-numbers\s*\{([^}]*)\}/);
  expect(rule, ".goal-checker-numbers is missing").not.toBeNull();
  expect((rule?.[1] ?? "").replace(/\s+/g, " ")).toContain(
    "grid-template-columns: minmax(0, 1fr) auto",
  );
});
