import { expect, test } from "vitest";
import { stylesSource } from "../stylesSource";

const css = stylesSource();

const body = (selector: string) => {
  const at = css.indexOf(`\n${selector} {`);
  expect(at, selector).toBeGreaterThanOrEqual(0);
  return css.slice(at, css.indexOf("}", at));
};

// A window opened over a panel shows both heads side by side, so a head a few
// pixels taller reads as a line out of step: 55px in the Tasks panel, 58.6px in
// the Files window, 57px in the swarm, 64px in the documentation.
test("every head of a window, a panel or a popover takes one height", () => {
  expect(css).toMatch(/--coddy-panel-head-height:\s*58px;/);
  for (const head of [
    ".sessions-head",
    ".files-header",
    ".docs-header",
    ".swarm-header",
  ]) {
    expect(body(head), head).toContain(
      "min-height: var(--coddy-panel-head-height)",
    );
  }
  // The height centres what stands in a head, so none keeps a padding of its
  // own above or below it.
  expect(body(".sessions-head")).toMatch(/padding: 0 /);
  expect(body(".files-header")).toMatch(/padding: 0 /);
  expect(body(".docs-header")).toMatch(/padding: 0 /);
  expect(body(".swarm-header")).toMatch(/padding: 0 /);
  expect(body(".context-breakdown-head")).toContain("padding: 0 12px");
  expect(body(".goal-popover-head")).toContain("padding: 0 12px");
});

// The documentation and the swarm laid their heads on the inset of their pages,
// 28 and 24px, so the title and the close control stood twice as deep as in Files.
test("every head of a window keeps one inset from the sides", () => {
  expect(css).toMatch(/--coddy-panel-head-inline:\s*14px;/);
  const inset = "var(--coddy-panel-head-inline)";
  expect(body(".sessions-head")).toContain(`padding: 0 ${inset}`);
  expect(body(".files-header")).toContain(`padding: 0 ${inset}`);
  expect(body(".docs-header")).toContain(`padding: 0 ${inset}`);
  expect(body(".swarm-header")).toContain(`padding: 0 ${inset}`);
});

// On the stacked shell the head has a second row, the search: the first row is
// still the head every other window has, with the close control where theirs is.
test("the first row of the documentation's two-row head is an ordinary head", () => {
  const at = css.indexOf(
    "@media (max-width: 1199px) {\n  /* Below the top bar",
  );
  expect(at).toBeGreaterThanOrEqual(0);
  const stacked = css.slice(at, css.indexOf(".docs-header-search", at));
  expect(stacked).toContain("--docs-inline: var(--coddy-panel-head-inline)");
  expect(stacked).toMatch(
    /grid-template-rows:\s*calc\(var\(--coddy-panel-head-height\) - 1px\) auto;/,
  );
  expect(stacked).toContain("padding: 0 var(--coddy-panel-head-inline) 10px");
  expect(stacked).toContain("row-gap: 0");
});
