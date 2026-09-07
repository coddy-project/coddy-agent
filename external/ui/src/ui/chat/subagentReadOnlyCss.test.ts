import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

test("the read-only notice reuses the composer's glass card tokens", () => {
  const body = ruleBody(".subagent-readonly-notice {");
  expect(body).toContain("var(--coddy-glass-panel-radius)");
  expect(body).toContain("var(--coddy-glass-panel-border)");
  expect(body).toContain("var(--coddy-glass-panel-bg)");
  expect(body).toContain("var(--text)");
});

test("the parent link is accent-tinted from theme tokens", () => {
  expect(ruleBody(".subagent-readonly-link {")).toContain("var(--accent)");
});
