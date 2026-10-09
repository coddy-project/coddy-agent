import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The filters of the session management table sit on a row of their own under
 * the search, half of it each. Beside the field and the two actions they left a
 * phone's field a sliver and pushed the actions past the edge of the drawer
 * (DESIGN.md, Sessions tab, Filters row).
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");
const tsx = readFileSync(join(__dirname, "SessionsManager.tsx"), "utf8");

function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

/** The body of the last rule whose selector list ends with `selector {`. */
function ruleBody(selector: string): string {
  const idx = css.lastIndexOf(`${selector} {`);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return squash(css.slice(open + 1, css.indexOf("}", open)));
}

test("the filters row lays the two selects side by side, half each", () => {
  expect(ruleBody(".sessions-manager-filters")).toContain("display: flex");
  expect(ruleBody(".sessions-manager-origin")).toContain("flex: 1 1 0");
  expect(ruleBody(".sessions-manager-origin")).toContain("min-width: 0");
  expect(ruleBody(".sessions-manager-origin-select")).toContain("width: 100%");
});

test("neither select is inside the toolbar of the search and the actions", () => {
  const toolbar = tsx.indexOf('className="sessions-manager-toolbar"');
  const filters = tsx.indexOf('className="sessions-manager-filters"');
  const archive = tsx.indexOf('data-testid="sessions-manager-archive-filter"');
  const origin = tsx.indexOf('data-testid="sessions-manager-origin-filter"');
  const actions = tsx.indexOf('className="sessions-manager-actions"');
  expect(toolbar).toBeGreaterThan(-1);
  // The toolbar holds the field and then the actions; the filters row starts
  // after it and holds both selects.
  expect(actions).toBeGreaterThan(toolbar);
  expect(filters).toBeGreaterThan(actions);
  expect(archive).toBeGreaterThan(filters);
  expect(origin).toBeGreaterThan(filters);
});
