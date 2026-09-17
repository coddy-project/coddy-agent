/**
 * Contract: the app has one chevron - the one the transcript rows fold with. The
 * History group headings drew a small solid triangle of their own, the Tasks panel a
 * border triangle, a settings field a third glyph, and each read as a different kind
 * of control. Every disclosure, submenu and dropdown chevron is <Chevron />.
 */
import React from "react";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Chevron } from "./Chevron";

afterEach(() => cleanup());

const dir = dirname(fileURLToPath(import.meta.url));
const uiRoot = join(dir, "..");
const css = readFileSync(join(uiRoot, "../styles.css"), "utf8");

function sources(root: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (/\.tsx$/.test(name) && !/\.test\.tsx$/.test(name)) out.push(path);
  }
  return out;
}

test("no component draws a triangle glyph of its own", () => {
  const offenders = sources(uiRoot).filter((path) =>
    // The play glyph of a Run control is not a chevron and stays.
    /[▾▸▴◂▼]/.test(readFileSync(path, "utf8")),
  );
  expect(offenders).toEqual([]);
});

test("the shared chevron is the transcript row's chevron", () => {
  // One rule draws both, so they cannot drift apart.
  expect(css).toMatch(
    /\.thinking-chevron:after,\s*\.coddy-chevron:after\s*\{[^}]*content:\s*"›"/,
  );
  expect(css).not.toMatch(/\.bgtask-section-chevron\s*\{/);
});

test("it points right until it is open, then down", () => {
  const { container, rerender } = render(<Chevron />);
  const el = container.querySelector(".coddy-chevron")!;
  expect(el).toHaveAttribute("aria-hidden", "true");
  expect(el).not.toHaveClass("is-open");
  rerender(<Chevron open />);
  expect(container.querySelector(".coddy-chevron")).toHaveClass("is-open");
  expect(css).toMatch(/\.coddy-chevron\.is-open\s*\{[^}]*rotate\(90deg\)/);
});
