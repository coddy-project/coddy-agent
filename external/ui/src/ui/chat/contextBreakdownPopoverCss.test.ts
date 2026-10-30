import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

test("context breakdown uses the shared drawer head divider and a two-sided usage row", () => {
  expect(css).toMatch(/\.context-breakdown-head\s*\{[^}]*padding:/s);
  expect(css).toMatch(/\.sessions-head\s*\{[^}]*border-bottom:/s);
  expect(css).toMatch(
    /\.context-breakdown-usage-row\s*\{[^}]*display:\s*flex;[^}]*justify-content:\s*space-between;/s,
  );
  expect(css).toMatch(
    /\.context-breakdown-used\s*\{[^}]*font-variant-numeric:/s,
  );
});

// The head and the usage row stand inside the scroller, which already keeps
// 12px from the panel's edges: a padding of their own put the title, the close
// control and the usage 24px in and the token count 26px, against the meter's
// 12px, and stopped the divider short of the edges.
test("the head and the usage keep the meter's edges, and the divider spans the panel", () => {
  const body = (selector: string) => {
    const at = css.indexOf(`\n${selector} {`);
    expect(at, selector).toBeGreaterThanOrEqual(0);
    return css.slice(at, css.indexOf("}", at));
  };
  expect(body(".context-breakdown-menu-scroll")).toContain(
    "padding: 0 12px 12px",
  );
  const head = body(".context-breakdown-head");
  expect(head).toContain("margin: 0 -12px");
  expect(head).toContain("padding: 10px 12px");
  expect(body(".context-breakdown-usage-row")).toContain("padding: 10px 0 0");
  expect(body(".context-breakdown-summary")).toContain("margin: 4px 0 10px");
});
