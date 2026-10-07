/**
 * Contract: the tags under a History row start where the title's text starts.
 *
 * The state marks (activity, permission, question, archive, unread) lead the
 * title on its line. The tags used to start at the row's edge plus a 2px nudge,
 * under the marks rather than under the words they label, so a row with a mark
 * read as two misaligned columns. The marks now take a fixed column of
 * their own, so an 8px dot and a 16px permission/question marker leave title
 * and tags at the same edge with deliberate breathing room.
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
  expect(link).toMatch(/grid-template-columns:\s*24px minmax\(0,\s*1fr\)/);
});

test("the marks start at the fixed column edge and center smaller dots", () => {
  const marks = rule(".session-row-marks");
  expect(marks).toMatch(/grid-column:\s*1/);
  expect(marks).toMatch(/width:\s*16px/);
  expect(marks).toMatch(/justify-content:\s*center/);
  expect(marks).toMatch(/margin-left:\s*0px/);
  expect(marks).not.toMatch(/margin-right/);
});

test("the title line and the tags share the second column, with no nudge of their own", () => {
  expect(rule(".session-row-leading")).toMatch(/grid-column:\s*2/);
  const tags = rule(".session-row-tags");
  expect(tags).toMatch(/grid-column:\s*2/);
  expect(tags).not.toMatch(/padding-left/);
});
