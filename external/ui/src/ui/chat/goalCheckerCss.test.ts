import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The goal menu's numbers - continuations, checks, time worked, tokens, the
 * checker and its reasoning level - are cells of one grid: three to a row on
 * a desktop and a tablet, two on a phone, every cell one column wide, so the
 * rows come out full and every label stands on the grid's column lines
 * (DESIGN.md, Session goal in the composer).
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

test("three numbers to a row above the phone tier", () => {
  const rule = css.match(/(?:^|\n)\.goal-numbers\s*\{([^}]*)\}/);
  expect(squash(rule?.[1] ?? "")).toContain(
    "grid-template-columns: repeat(3, minmax(0, 1fr))",
  );
});

test("two numbers to a row on a phone", () => {
  const phone = [
    ...css.matchAll(/@media \(max-width: 599px\) \{([\s\S]*?)\n\}/g),
  ].map((m) => squash(m[1] ?? ""));
  expect(
    phone.some((block) =>
      /\.goal-numbers \{ grid-template-columns: repeat\(2, minmax\(0, 1fr\)\); \}/.test(
        block,
      ),
    ),
  ).toBe(true);
});

test("no cell spans columns, and a long model id wraps in its own", () => {
  expect(css).not.toMatch(/\.goal-numbers-checker\b/);
  expect(css).not.toMatch(/\.goal-checker-numbers\b/);
  const dd = css.match(/\.goal-numbers dd\s*\{([^}]*)\}/);
  expect(squash(dd?.[1] ?? "")).toContain("overflow-wrap: anywhere");
});

test("the goal's action icons are rounded squares a finger can hit", () => {
  const rule = css.match(/\.goal-icon-btn\s*\{([^}]*)\}/);
  const body = squash(rule?.[1] ?? "");
  expect(body).toContain("width: 36px");
  expect(body).toContain("height: 36px");
  expect(body).toContain("border-radius: 10px");
});
