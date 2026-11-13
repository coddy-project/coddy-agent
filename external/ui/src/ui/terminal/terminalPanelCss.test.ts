import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

const squash = (s: string) => s.replace(/\s+/g, " ").trim();
const body = (selector: string) => {
  const at = css.indexOf(`\n${selector} {`);
  expect(at, selector).toBeGreaterThanOrEqual(0);
  return squash(css.slice(at, css.indexOf("}", at)));
};

test("the terminal is a window of its own, placed by the component", () => {
  const panel = body(".terminal-panel");
  expect(panel).toContain("position: fixed");
  // The component writes left, top, width and height, so the stylesheet must
  // not pin any of them: a rule here would freeze a window being dragged.
  expect(panel).not.toMatch(/(^| )(left|top|width|height):/);
  // Framed like the other windows of the shell rather than a palette of its own.
  expect(panel).toContain("border-radius: var(--coddy-glass-panel-radius)");
  expect(panel).toContain("background: var(--coddy-glass-panel-bg)");
});

test("the window stands over the chat's layers and under the composer's menus", () => {
  // 80 is the top bar, 85 the expanded Files sheet, 90 the composer's menus.
  const z = /\.terminal-panel\s*\{[^}]*z-index:\s*(\d+)/.exec(css)?.[1];
  expect(Number(z)).toBeGreaterThan(85);
  expect(Number(z)).toBeLessThan(90);
});

test("the stacked shell gets the whole screen, a desktop window only on request", () => {
  const full = body(".terminal-panel--full");
  expect(full).toContain("inset: 0");
  expect(full).toContain("border-radius: 0");
  const maximized = body(".terminal-panel--maximized");
  expect(maximized).toContain("inset: 14px");
});

test("every edge and corner has a grip, and none of them scrolls the page", () => {
  for (const edge of ["n", "s", "e", "w", "ne", "nw", "se", "sw"]) {
    expect(css, edge).toContain(`.terminal-grip--${edge}`);
  }
  expect(body(".terminal-grip")).toContain("touch-action: none");
  // A drag of the head must move the window, not select its title.
  const head = body(".terminal-head");
  expect(head).toContain("touch-action: none");
  expect(head).toContain("user-select: none");
});

test("the head takes the height and inset every window's head takes", () => {
  const head = body(".terminal-head");
  expect(head).toContain("min-height: var(--coddy-panel-head-height)");
  expect(head).toContain("padding: 0 var(--coddy-panel-head-inline)");
});

test("the command line has no Run button and does not zoom an iPhone", () => {
  expect(css).not.toMatch(/\.terminal-input-row button/);
  const at = css.indexOf("@media (max-width: 599px) {\n  .terminal-input-row");
  expect(at).toBeGreaterThanOrEqual(0);
  expect(squash(css.slice(at, css.indexOf("}", at)))).toContain(
    "font-size: 16px",
  );
});
