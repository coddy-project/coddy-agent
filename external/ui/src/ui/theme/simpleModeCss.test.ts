import fs from "node:fs";
import path from "node:path";
import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();
const partial = fs.readFileSync(
  path.join(process.cwd(), "src/styles/_simple.scss"),
  "utf8",
);
const flat = partial.replace(/\s+/g, " ");
/** The partial is last in the cascade, so what stands before this is the rest. */
const FIRST_RULE = 'html[data-simple="on"]';

test("the partial comes last in the cascade and clears shadows, blur, masks and motion", () => {
  const entry = fs.readFileSync(
    path.join(process.cwd(), "src/styles.scss"),
    "utf8",
  );
  const uses = [...entry.matchAll(/^@use "([^"]+)";/gm)].map((m) => m[1]);
  expect(uses[uses.length - 1]).toBe("styles/simple");

  for (const declaration of [
    "text-shadow: none !important",
    "backdrop-filter: none !important",
    "-webkit-backdrop-filter: none !important",
    "mask-image: none !important",
    "-webkit-mask-image: none !important",
    "transition: none !important",
    "animation-iteration-count: 1 !important",
    "scroll-behavior: auto !important",
  ]) {
    expect(flat).toContain(declaration);
  }
  // A fill-mode animation has to reach its end, so the duration goes and not
  // the animation: "animation: none" would leave a faded-in row invisible.
  expect(flat).toContain("animation-duration: 0.01ms !important");
  expect(flat).not.toContain("animation: none");
  expect(flat).toContain("box-shadow: none !important");
});

test("the panels are solid colours of the theme", () => {
  for (const token of [
    "--coddy-glass-panel-bg",
    "--coddy-chat-header-bg",
    "--nav",
    "--sidebar",
    "--coddy-plan-eye-bg",
    "--coddy-tip-bg",
  ]) {
    expect(flat).toContain(`${token}: var(--coddy-simple-surface)`);
  }
  expect(flat).toContain("--coddy-grain: none");
  // Two opaque colours of the theme, so the mix is opaque in every theme.
  expect(flat).toMatch(
    /--coddy-simple-surface: color-mix\( ?in srgb, var\(--text\) 6%, var\(--coddy-simple-canvas\) ?\)/,
  );
  expect(flat).toContain("background: var(--coddy-simple-canvas)");
});

test("every ring the mode keeps exists in the stylesheet", () => {
  const list = partial.match(/\$kept-rings:\s*\(([\s\S]*?)\);/)?.[1] ?? "";
  const rings = [...list.matchAll(/"([^"]+)"/g)].map((m) => m[1]!);
  expect(rings.length).toBeGreaterThan(10);
  const others = css.slice(0, css.indexOf(FIRST_RULE));
  for (const ring of rings) {
    if (ring.startsWith(":")) continue;
    expect(others, ring).toContain(ring);
  }
});

// A new gradient has to say which of the two it is: a decoration, which the mode
// clears, or a mark that is drawn with hard stops and means something.
test("every gradient of the stylesheet is cleared by the mode or draws a mark", () => {
  const others = css
    .slice(0, css.indexOf(FIRST_RULE))
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const selectors = new Set<string>();
  for (const m of others.matchAll(/([^{};]+)\{[^{}]*gradient\(/g)) {
    const selector = m[1]!.trim().replace(/\s+/g, " ");
    if (!selector.startsWith("@")) selectors.add(selector);
  }
  const marks = new Set([
    // The half-filled circle of a task in progress, hard stops.
    ".todo-tool-preview-row--in_progress .todo-tool-preview-mark, .todo-tool-preview-mark--in_progress",
    // The dot in that circle on the checklist, hard stops.
    ".todo-checklist .todo-tool-preview-mark--in_progress",
    // The checkerboard that shows a picture's transparency.
    ".files-image",
  ]);
  // Fades along the edge of a strip of tabs: the mask rule of the blanket.
  const masks = new Set([
    ".composer-tabs",
    ".files-tabs.has-more-left",
    ".files-tabs.has-more-right",
    ".files-tabs.has-more-left.has-more-right",
  ]);
  const cleared = [...selectors].filter(
    (s) => !marks.has(s) && !masks.has(s) && s !== "html",
  );
  expect(cleared.length).toBeGreaterThan(5);
  for (const selector of cleared) {
    for (const one of selector.split(",")) {
      expect(flat, one.trim()).toContain(
        `html[data-simple="on"] ${one.trim().replace(/^\[data-theme="light"\] /, "")}`,
      );
    }
  }
});
