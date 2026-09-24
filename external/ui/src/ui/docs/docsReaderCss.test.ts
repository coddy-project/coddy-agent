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

// The picture is dragged and pinched on the stage itself, so the browser must
// not pan or zoom it first. The rule is on the stage, which lives only while
// the viewer is open, so the page behind it keeps its own gestures.
test("the lightbox stage takes the touch gestures itself", () => {
  const stage = rule(".docs-lightbox-stage");
  expect(stage).toMatch(/touch-action:\s*none/);
  expect(stage).toMatch(/user-select:\s*none/);
});

// A zoomed picture is dragged with the pointer: the hand, open until it holds.
test("a zoomed picture in the lightbox offers the grab cursor", () => {
  expect(rule(".docs-lightbox-stage img.is-zoomed")).toMatch(/cursor:\s*grab;/);
  expect(rule(".docs-lightbox-stage.is-panning")).toMatch(/cursor:\s*grabbing/);
  expect(rule(".docs-lightbox-stage.is-panning img.is-zoomed")).toMatch(/cursor:\s*grabbing/);
});

// The rules one width query sets for a selector: the body of the first
// `@media (<query>)` block that holds `selector {`.
function mediaRule(query: string, selector: string): string {
  const head = `@media (${query}) {`;
  let from = 0;
  for (;;) {
    const at = css.indexOf(head, from);
    expect(at, `${head} holding ${selector}`).toBeGreaterThan(-1);
    let depth = 0;
    let end = at + head.length - 1;
    for (; end < css.length; end++) {
      if (css[end] === "{") depth++;
      else if (css[end] === "}" && --depth === 0) break;
    }
    const block = css.slice(at + head.length, end);
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const m = new RegExp(`(?:^|\\n)\\s*${escaped}\\s*\\{([^}]+)\\}`).exec(block);
    if (m) return m[1]!;
    from = end;
  }
}

// On the stacked shell the header folds into two rows. The search in the
// second one is still the text's own control: as wide as the text column,
// never stretched past it over the empty space beside the page.
test("on the stacked shell the search stays as wide as the text column", () => {
  const article = px(rule(".docs-article"), "max-width");
  expect(px(rule(".docs-header-search"), "max-width")).toBe(article);
  expect(mediaRule("max-width: 1199px", ".docs-header")).toMatch(/"search search"/);
  const stacked = (() => {
    try {
      return mediaRule("max-width: 1199px", ".docs-header-search");
    } catch {
      return "";
    }
  })();
  expect(stacked).not.toMatch(/max-width:\s*none/);
});

// The contents button and the page under the search keep the same measure,
// so the three line up on one left edge and one right edge.
test("the stacked page is one column as wide as the text", () => {
  const article = px(rule(".docs-article"), "max-width");
  expect(mediaRule("max-width: 1199px", ".docs-layout")).toMatch(
    new RegExp(`grid-template-columns:\\s*minmax\\(0,\\s*${article}px\\)`),
  );
});

// The pages of a group sit to the right of the group's title, so the title
// reads as the heading of the list under it rather than as one more row.
test("the pages of the contents are indented under their group title", () => {
  const title = /margin:\s*0 0 6px (\d+)px/.exec(
    rule(".docs-toc-group-title,\n.docs-outline-title"),
  );
  expect(title).not.toBeNull();
  const list = rule(".docs-toc-group ul");
  const indent = px(list, "padding-left");
  const pagePad = /padding:\s*\d+px (\d+)px/.exec(rule(".docs-toc-page"));
  expect(pagePad).not.toBeNull();
  // Where the page's text starts, against where the title's text starts.
  expect(indent + Number(pagePad![1])).toBeGreaterThanOrEqual(Number(title![1]) + 10);
});
