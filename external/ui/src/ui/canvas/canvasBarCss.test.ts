import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The canvas's fields take the focus as the app's other fields do: their own
 * border in the accent, never the browser's blue ring around them.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.scss"), "utf8");

function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.scss`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return squash(css.slice(open + 1, css.indexOf("}", open)));
}

test("a canvas field draws its own focus, not the browser's ring", () => {
  const body = ruleBody(".canvas-bar-field:focus");
  expect(body).toContain("outline: none");
  expect(body).toMatch(/border-color: color-mix\(in srgb, var\(--accent\)/);
  expect(css).toContain(".canvas-popover-note:focus");
});

// The board draws nothing again on a pan or a zoom: one world layer takes the
// camera's transform, and what keeps its size on screen scales back by the
// variable the camera writes (cameraStore.ts).
test("the board moves as one world layer and the title bars scale back", () => {
  expect(ruleBody(".canvas-world {")).toContain("transform-origin: 0 0");
  expect(ruleBody(".canvas-board.is-moving .canvas-world")).toContain(
    "will-change: transform",
  );
  const bar = ruleBody(".canvas-frame-bar {");
  expect(bar).toContain("scale(var(--canvas-inv, 1))");
  expect(bar).toContain("var(--canvas-scale, 1)");
  expect(ruleBody(".canvas-frame-page {")).not.toContain("transform");
  expect(ruleBody(".canvas-node-body {")).not.toContain("transform");
});

// A blur behind a control over the board is drawn again on every frame of a
// pan or a zoom: the controls over it are opaque instead.
test("nothing over the board blurs what moves under it", () => {
  for (const sel of [".zoom-controls {", ".canvas-mode {"]) {
    expect(ruleBody(sel)).not.toContain("backdrop-filter");
  }
  // Nor do the window and the chat it covers while a board shows.
  expect(ruleBody(".files-dock-cluster:has(.canvas-pane)")).toContain(
    "backdrop-filter: none",
  );
  const covered = ruleBody(":root:has(.canvas-pane)");
  expect(covered).toContain("backdrop-filter: none");
  expect(ruleBody(".canvas-group-label {")).toContain(
    "scale(var(--canvas-inv, 1))",
  );
  expect(ruleBody(".canvas-edge-label {")).toContain(
    "scale(var(--canvas-inv, 1))",
  );
});

// A frame's size menu opens over the pages of the board, not under the next
// one: the bar stands above the frames, the one with its menu open above all.
test("a frame's size menu is never under a page", () => {
  expect(css).toMatch(/\.canvas-frame-bar \{\s*z-index: 2;/);
  expect(css).toMatch(/\.canvas-frame-bar\.is-menu-open \{\s*z-index: 3;/);
});

// The zoom controls are the shared ones, at the board's top right, as on the
// swarm map and in the picture viewer.
test("the canvas has no zoom controls of its own", () => {
  expect(css).not.toMatch(/\.canvas-zoom\b/);
  expect(css).not.toMatch(/\.canvas-fit\b/);
  expect(ruleBody(".zoom-controls {")).toContain("right: 10px");
});

test("the bar has no Stop of its own beside Send", () => {
  expect(css).not.toMatch(/\.canvas-bar-stop\b/);
});
