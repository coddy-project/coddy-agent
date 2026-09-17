/**
 * Contract: the tags under a History row start where the title's text starts.
 *
 * The state marks (activity, permission, question, archive, unread) lead the
 * title on its line. The tags used to start at the row's edge plus a 2px nudge,
 * under the marks rather than under the words they label, so a row with a mark
 * read as two misaligned columns. The marks now take a column of their own and
 * the title and the tags share the second one: without a mark that column is
 * empty and both start at the row's edge.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return (
    new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? ""
  );
}

test("the row link lays the marks and the text out as two columns", () => {
  const link = rule(".session-row-link");
  expect(link).toMatch(/display:\s*grid/);
  expect(link).toMatch(/grid-template-columns:\s*auto minmax\(0,\s*1fr\)/);
});

test("the marks hold the first column and keep their gap to the title themselves", () => {
  const marks = rule(".session-row-marks");
  expect(marks).toMatch(/grid-column:\s*1/);
  expect(marks).toMatch(/margin-right:\s*6px/);
});

test("the title line and the tags share the second column, with no nudge of their own", () => {
  expect(rule(".session-row-leading")).toMatch(/grid-column:\s*2/);
  const tags = rule(".session-row-tags");
  expect(tags).toMatch(/grid-column:\s*2/);
  expect(tags).not.toMatch(/padding-left/);
});
