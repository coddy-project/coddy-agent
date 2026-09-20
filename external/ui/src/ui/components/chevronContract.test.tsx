/**
 * Contract: the app has one chevron - the one the transcript rows fold with. The
 * History group headings drew a small solid triangle of their own, the Tasks panel a
 * border triangle, a settings field a third glyph, and each read as a different kind
 * of control. Every disclosure, submenu and dropdown chevron is <Chevron />.
 *
 * It is an SVG, not a text glyph. A glyph's ink sits wherever the platform's font
 * puts it inside the line box, so the same CSS placed the chevron differently on
 * every machine and the reports of a chevron riding above its label kept coming
 * back. The SVG's ink is centred in its viewBox by construction, which is what the
 * symmetry assertion below holds.
 */
import React from "react";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Chevron } from "./Chevron";

afterEach(() => cleanup());

const dir = dirname(fileURLToPath(import.meta.url));
const uiRoot = join(dir, "..");
const css = readFileSync(join(uiRoot, "../styles.css"), "utf8");
const chevronModule = join("components", "Chevron.tsx");

function sources(root: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (/\.tsx$/.test(name) && !/\.test\.tsx$/.test(name)) out.push(path);
  }
  return out;
}

function numbers(attr: string | null | undefined): number[] {
  return (attr || "")
    .trim()
    .split(/[\s,]+/)
    .filter(Boolean)
    .map(Number);
}

function svgOf(): SVGSVGElement {
  const { container } = render(<Chevron />);
  const svg = container.querySelector("svg");
  if (!svg) throw new Error("the chevron does not render an svg");
  return svg as unknown as SVGSVGElement;
}

test("no component draws a triangle glyph of its own", () => {
  const offenders = sources(uiRoot).filter((path) =>
    // The play glyph of a Run control is not a chevron and stays.
    /[▾▸▴◂▼⌄⌃]/.test(readFileSync(path, "utf8")),
  );
  expect(offenders).toEqual([]);
});

test("nothing draws a disclosure marker as a text glyph", () => {
  // The chevron used to be `content: "›"` on a pseudo-element. Whatever a rule
  // puts in `content`, its ink lands where the font decides, so no stylesheet
  // draws a chevron or a triangle that way again.
  const drawn = css.match(
    /content:\s*(["'])(?:(?!\1).)*[›‹▸▾▴◂▼⌄⌃](?:(?!\1).)*\1/g,
  );
  expect(drawn).toBeNull();

  // The component itself carries no text: an SVG or nothing.
  const { container } = render(<Chevron />);
  expect(container.textContent).toBe("");
  expect(container.querySelectorAll("svg")).toHaveLength(1);
});

test("one source draws every chevron", () => {
  // A class named after a chevron belongs to <Chevron />, and no other module
  // declares one; either would be a second implementation beside this one. A
  // sort direction mark is not a disclosure and is not covered here.
  const misplaced: string[] = [];
  const declared: string[] = [];
  for (const path of sources(uiRoot)) {
    const name = relative(uiRoot, path);
    if (name === chevronModule) continue;
    const text = readFileSync(path, "utf8");
    for (const m of text.matchAll(
      /<([A-Za-z][\w.]*)(?:[^>"']|"[^"]*"|'[^']*')*?className=(?:"([^"]*)"|\{`([^`]*)`\})/g,
    )) {
      const cls = m[2] || m[3] || "";
      if (!/chevron/i.test(cls)) continue;
      if (m[1] !== "Chevron")
        misplaced.push(`${name}: <${m[1]} className="${cls}">`);
    }
    for (const m of text.matchAll(
      /(?:function|const|class)\s+(\w*Chevron\w*)\b/g,
    )) {
      declared.push(`${name}: ${m[1]}`);
    }
  }
  expect(misplaced).toEqual([]);
  expect(declared).toEqual([]);
  expect(css).not.toMatch(/\.bgtask-section-chevron\s*\{/);
});

test("the transcript row's chevron is the shared component", () => {
  // .thinking-chevron used to be a pseudo-element with a glyph of its own, kept
  // in step with the shared one by a single CSS rule. It is the same component
  // now, so the two cannot drift at all.
  for (const name of [
    "ThinkingMessage",
    "ToolCallMessage",
    "CompactionMessage",
  ]) {
    const text = readFileSync(join(uiRoot, "messages", `${name}.tsx`), "utf8");
    expect(text).toMatch(/<Chevron\s+className="thinking-chevron"\s*\/>/);
    expect(text).not.toMatch(/<span className="thinking-chevron"/);
  }
});

test("the chevron's ink is centred in its viewBox", () => {
  // Nothing about the platform can move the ink if the geometry mirrors about the
  // centre of the box it is drawn in: this is the whole fix.
  const svg = svgOf();
  const box = numbers(svg.getAttribute("viewBox"));
  expect(box).toHaveLength(4);
  const [minX = 0, minY = 0, width = 0, height = 0] = box;
  const cx = minX + width / 2;
  const cy = minY + height / 2;

  const shapes = svg.querySelectorAll("polyline, polygon");
  expect(shapes).toHaveLength(1);
  const raw = numbers(shapes[0]?.getAttribute("points"));
  expect(raw.length).toBeGreaterThanOrEqual(6);
  expect(raw.length % 2).toBe(0);
  expect(raw.every((n) => Number.isFinite(n))).toBe(true);

  const xs = raw.filter((_, i) => i % 2 === 0);
  const ys = raw.filter((_, i) => i % 2 === 1);

  // The drawn extent is centred on (cx, cy). A round cap and a round join add the
  // same half-stroke on every side of it, so the ink box is centred too.
  for (const [values, centre, axis] of [
    [xs, cx, "x"],
    [ys, cy, "y"],
  ] as const) {
    const mid = (Math.min(...values) + Math.max(...values)) / 2;
    expect(
      Math.abs(mid - centre),
      `${axis} extent runs ${Math.min(...values)}..${Math.max(...values)}, not centred on ${centre}`,
    ).toBeLessThan(1e-9);
  }

  // And the shape itself mirrors about the horizontal axis, so it cannot lean up
  // or down inside that extent.
  const mirrored = ys.map((v) => 2 * cy - v).sort((a, b) => a - b);
  [...ys]
    .sort((a, b) => a - b)
    .forEach((v, i) => {
      expect(
        Math.abs(v - (mirrored[i] ?? NaN)),
        "the chevron leans",
      ).toBeLessThan(1e-9);
    });
});

test("it keeps the weight of the glyph it replaced", () => {
  const line = svgOf().querySelector("polyline")!;
  expect(line.getAttribute("stroke")).toBe("currentColor");
  expect(line.getAttribute("stroke-linecap")).toBe("round");
  expect(line.getAttribute("stroke-linejoin")).toBe("round");
  expect(Number(line.getAttribute("stroke-width"))).toBeGreaterThan(0);
  expect(svgOf().getAttribute("fill")).toBe("none");
});

test("it points right until it is open, then down", () => {
  const { container, rerender } = render(<Chevron />);
  const el = container.querySelector(".coddy-chevron")!;
  expect(el).toHaveAttribute("aria-hidden", "true");
  expect(el).not.toHaveClass("is-open");
  rerender(<Chevron open />);
  expect(container.querySelector(".coddy-chevron")).toHaveClass("is-open");
  expect(css).toMatch(/\.coddy-chevron\.is-open\s*\{[^}]*rotate\(90deg\)/);
  expect(css).toMatch(/\.coddy-chevron--down\s*\{[^}]*rotate\(90deg\)/);
  expect(css).toMatch(
    /\.coddy-chevron--down\.is-open\s*\{[^}]*rotate\(-90deg\)/,
  );
});

test("the turn stops when the reader asked for less motion", () => {
  const reduced = css.match(
    /@media \(prefers-reduced-motion: reduce\)\s*\{(?:[^{}]|\{[^{}]*\})*\}/g,
  );
  expect(reduced?.some((block) => /\.coddy-chevron[\s,{]/.test(block))).toBe(
    true,
  );
});
