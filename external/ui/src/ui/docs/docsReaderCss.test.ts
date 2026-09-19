import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, selector).not.toBeNull();
  return m![1]!;
}

function px(block: string, prop: string): number {
  const m = new RegExp(`(?:^|[;\\s])${prop}:\\s*([\\d.]+)px`).exec(block);
  return m ? Number(m[1]) : NaN;
}

// Buttons side by side in the header are one height: the ask pill next to
// the close control it shares the row with.
test("the ask pill is as tall as the close control beside it", () => {
  expect(px(rule(".docs-ask"), "height")).toBe(px(rule(".sessions-close"), "height"));
});

// On a wide window the sheet is as wide as its columns, so the outline sits
// right after the text instead of drifting to the far edge.
test("the reader sheet is no wider than its three columns", () => {
  const dock = rule(".docs-dock-cluster");
  const layout = rule(".docs-layout");
  const view = rule(".docs-view");
  const sheet = Number(/width:\s*min\((\d+)px/.exec(dock)?.[1]);
  const cols = /grid-template-columns:\s*minmax\(\d+px,\s*(\d+)px\)\s*minmax\(0,\s*1fr\)\s*minmax\(\d+px,\s*(\d+)px\)/.exec(layout);
  const article = px(rule(".docs-article"), "max-width");
  const gap = px(layout, "gap");
  const inline = px(view, "--docs-inline");
  expect(cols).not.toBeNull();
  expect(sheet).toBe(Number(cols![1]) + article + Number(cols![2]) + 2 * gap + 2 * inline);
});

// The lightbox stage is a flex row: an image that may shrink is pulled back
// to the stage's width, and a zoom past it would never scroll.
test("a zoomed image in the lightbox keeps its width", () => {
  expect(rule(".docs-lightbox-stage img")).toMatch(/(?:^|[;\s])flex:\s*none/);
  expect(rule(".docs-lightbox-stage img.is-zoomed")).toMatch(/max-width:\s*none/);
});

// The zoom level reads "Fit" / "По окну": on a phone it must stay one line in
// the 30px frame the close control sets.
test("the lightbox zoom level stays on one line", () => {
  expect(rule(".docs-lightbox-level")).toMatch(/white-space:\s*nowrap/);
});

// The page scrolls under the header, not with it: the scrollbar starts below
// the search box instead of running up beside it.
test("the reader scrolls its body, not the sheet the header sits in", () => {
  expect(rule(".docs-dock-cluster")).toMatch(/overflow:\s*hidden/);
  expect(rule(".docs-header")).not.toMatch(/position:\s*sticky/);
  const body = rule(".docs-body");
  expect(body).toMatch(/overflow:\s*auto/);
  expect(body).toMatch(/min-height:\s*0/);
});
