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

// A phone gives the subtitle 40% of the width: the folder's name takes the
// ellipsis and git's count under the title stays whole.
test("on a narrow head the folder gives way and git's count stays whole", () => {
  expect(rule(".edits-window .files-subtitle")).toMatch(/display:\s*flex/);
  const folder = rule(".edits-folder");
  expect(folder).toMatch(/min-width:\s*0/);
  expect(folder).toMatch(/text-overflow:\s*ellipsis/);
  expect(folder).toMatch(/overflow:\s*hidden/);
  expect(rule(".edits-totals")).toMatch(/flex:\s*none/);
});

// Copy and discard stay in sight on every file: nothing hides them until the
// pointer comes, on a desktop or a touch screen.
test("a file's actions are never hidden until hovered", () => {
  expect(css).not.toMatch(/\.dv-file-actions\s*\{[^}]*opacity:\s*0/);
  expect(css).not.toContain(".dv-file-head:hover .dv-file-actions");
});

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

// The diffs keep from the edges of their column what the tree's column keeps
// (the filter's 10px), and the files stand 10px apart.
test("the diffs keep the tree's distance from the edges of their column", () => {
  const filter = padding(rule(".files-filter"));
  const diffs = padding(rule(".dv-scroll"));
  expect(diffs[0]).toBe(filter[0]);
  expect(diffs[1]).toBe(filter[1]);
  expect(diffs[3]).toBe(filter[3]);
  expect(rule(".dv-file")).toMatch(/margin-bottom:\s*10px/);
});

// A file's head, with the card's border above it and its own line under it,
// is as tall as the filter beside it: the card starts where the filter does
// and the line under the head is level with the filter's bottom edge.
test("a file's head is as tall as the filter", () => {
  const input = /height:\s*([\d.]+)px/.exec(rule(".files-filter input"))![1]!;
  const head = rule(".dv-file-head");
  expect(head).toMatch(/box-sizing:\s*border-box/);
  const height = parseFloat(/(?:^|[;\s])height:\s*([\d.]+)px/.exec(head)![1]!);
  expect(rule(".dv-file")).toMatch(/border:\s*1px/);
  // The card's top border, then the head with its 1px line.
  expect(1 + height).toBe(parseFloat(input));
});
