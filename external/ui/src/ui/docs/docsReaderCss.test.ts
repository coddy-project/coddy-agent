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

// On a tablet the reader has the sheet to itself: the search, the Contents
// button and the page stretch across it, so nothing leaves an empty strip
// beside the text or the search.
test("on the stacked shell the search and the page stretch across the sheet", () => {
  expect(px(rule(".docs-header-search"), "max-width")).toBe(px(rule(".docs-article"), "max-width"));
  expect(mediaRule("max-width: 1199px", ".docs-header")).toMatch(/"search search"/);
  expect(mediaRule("max-width: 1199px", ".docs-header-search")).toMatch(/max-width:\s*none/);
  expect(mediaRule("max-width: 1199px", ".docs-article")).toMatch(/max-width:\s*none/);
  const layout = mediaRule("max-width: 1199px", ".docs-layout");
  expect(layout).toMatch(/grid-template-columns:\s*minmax\(0,\s*1fr\)\s*;/);
});

// Where the outline has no room beside the page it folds into a button above
// it, under the Contents button, the way the contents do.
test("a narrow stacked shell folds On this page into a button above the page", () => {
  const layout = mediaRule("max-width: 1199px", ".docs-layout");
  expect(layout).toMatch(/"toc"\s*"outline"\s*"page"/);
  expect(rule(".docs-outline-toggle")).toMatch(/display:\s*none/);
  expect(mediaRule("max-width: 1199px", ".docs-outline-toggle")).toMatch(/display:\s*flex/);
  expect(mediaRule("max-width: 1199px", ".docs-outline")).toMatch(/position:\s*static/);
  expect(mediaRule("max-width: 1199px", ".docs-outline ul")).toMatch(/display:\s*none/);
  expect(mediaRule("max-width: 1199px", ".docs-outline.is-open ul")).toMatch(/display:\s*flex/);
});

// The pages of a group sit to the right of the group's title, so the title
// reads as the heading of the list under it rather than as one more row.
test("the pages of the contents are indented under their group title", () => {
  const title = /margin:\s*0 0 6px (\d+)px/.exec(
    rule(".docs-toc-group-title,\n.docs-outline-title"),
  );
  expect(title).not.toBeNull();
  const list = rule(".docs-toc-group > ul");
  const indent = px(list, "padding-left");
  const pagePad = /padding:\s*\d+px (\d+)px/.exec(rule(".docs-toc-page"));
  expect(pagePad).not.toBeNull();
  // Where the page's text starts, against where the title's text starts.
  expect(indent + Number(pagePad![1])).toBeGreaterThanOrEqual(Number(title![1]) + 10);
});

// The header is laid on the columns of the page. With columns of its own (the
// actions as wide as their content), the search ran past the text into the
// On this page column beside it.
test("on the desktop the header uses the page's own columns", () => {
  const columns = (block: string) =>
    /grid-template-columns:\s*([^;]+);/.exec(block)?.[1]?.replace(/\s+/g, " ").trim();
  expect(columns(rule(".docs-header"))).toBe(columns(rule(".docs-layout")));
});

// The body scrolls and the header does not: a classic scrollbar takes its
// width from the body's columns alone. The header makes room for the same
// width, measured by DocsView, so the two sets of columns stay one.
test("the header leaves room for the body's scrollbar", () => {
  expect(rule(".docs-header")).toMatch(
    /padding:[^;]*calc\(var\(--docs-inline\) \+ var\(--docs-scrollbar, 0px\)\)/,
  );
  expect(mediaRule("max-width: 1199px", ".docs-header")).toMatch(
    /padding:[^;]*calc\(var\(--docs-inline\) \+ var\(--docs-scrollbar, 0px\)\)/,
  );
});

// A tablet wide enough for both puts On this page beside the page, where the
// desktop has it: the page takes the rest of the width, the outline keeps its
// column, and the contents stay folded above the page.
test("a wide tablet shows On this page beside the page", () => {
  const q = "min-width: 900px) and (max-width: 1199px";
  const layout = mediaRule(q, ".docs-layout");
  expect(layout).toMatch(/grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(190px,\s*210px\)/);
  expect(layout).toMatch(/"toc outline"\s*"page outline"/);
  expect(mediaRule(q, ".docs-outline")).toMatch(/position:\s*sticky/);
  expect(mediaRule(q, ".docs-outline-toggle")).toMatch(/display:\s*none/);
  expect(mediaRule(q, ".docs-outline ul")).toMatch(/display:\s*flex/);
});
