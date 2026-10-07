import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, selector).not.toBeNull();
  return m![1]!;
}

/** The four sides of a `padding` shorthand, in pixels. */
function padding(block: string): number[] {
  const values = /padding:\s*([^;]+);/
    .exec(block)![1]!
    .trim()
    .split(/\s+/)
    .map((v) => parseFloat(v));
  const [top, right = top, bottom = top, left = right] = values as [
    number,
    number?,
    number?,
    number?,
  ];
  return [top, right, bottom, left];
}

// The tree's rows (and the file on show among them) are as wide as the filter
// over them, in the Files window and the edits window alike.
test("the rows of the tree are as wide as the filter", () => {
  const filter = padding(rule(".files-filter"));
  const tree = padding(rule(".files-tree"));
  expect(tree[1]).toBe(filter[1]);
  expect(tree[3]).toBe(filter[3]);
});

// The tabs stand as far under the window's head as the filter beside them.
test("the tabs start as far under the head as the filter", () => {
  expect(padding(rule(".files-tabs-bar"))[0]).toBe(
    padding(rule(".files-filter"))[0],
  );
});

// The strip scrolls sideways with no scrollbar of its own: the arrows, the
// wheel and a swipe move it.
test("the tab strip scrolls sideways without a scrollbar", () => {
  const strip = rule(".files-tabs");
  expect(strip).toMatch(/overflow-x:\s*auto/);
  expect(strip).toMatch(/scrollbar-width:\s*none/);
  expect(css).toMatch(/\.files-tabs::-webkit-scrollbar\s*\{\s*display:\s*none/);
});

/** A length in pixels of one property of a rule. */
function px(block: string, prop: string): number {
  const m = new RegExp(`(?:^|[;\\s])${prop}:\\s*([\\d.]+)px`).exec(block);
  expect(m, prop).not.toBeNull();
  return parseFloat(m![1]!);
}

// A tab and the line under it are as tall as the filter beside them: the line
// the file starts under is level with the filter's bottom edge.
test("a tab and the line under it are as tall as the filter", () => {
  const input = rule(".files-filter input");
  const tab = rule(".files-tab");
  expect(tab).toMatch(/box-sizing:\s*border-box/);
  const line = /border-bottom:\s*1px/.test(rule(".files-tabs-bar")) ? 1 : 0;
  expect(line).toBe(1);
  expect(px(tab, "height") + line).toBe(px(input, "height"));
});
