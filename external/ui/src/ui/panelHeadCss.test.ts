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
  expect(body(".sessions-head")).toContain("padding: 0 14px");
  expect(body(".files-header")).toContain("padding: 0 14px 0 12px");
  expect(body(".docs-header")).toContain("padding: 0 var(--docs-inline)");
  expect(body(".swarm-header")).toContain(
    "padding: 0 var(--swarm-inline, 24px)",
  );
  expect(body(".context-breakdown-head")).toContain("padding: 0 12px");
  expect(body(".goal-popover-head")).toContain("padding: 0 12px");
});
