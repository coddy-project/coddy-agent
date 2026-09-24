/**
 * Contract: nothing in the transcript is wider than the transcript.
 *
 * A row that cannot wrap widens its column, and on the stacked shell the column
 * is the page: one tool row named after an MCP tool made a 360px phone scroll
 * sideways by 400px, and so did a long link or identifier in an answer. jsdom
 * does no layout, so the rules are pinned here; the live check at every width of
 * the grid is external/ui/scripts/phone-overflow-check.mjs.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** Declarations of every top-level rule whose selector list is exactly `selector`. */
function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return [...css.matchAll(new RegExp(`(?:^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "g"))]
    .map((m) => m[1])
    .join(";");
}

function decl(body: string, prop: string): string | undefined {
  const values = [...body.matchAll(new RegExp(`(?:^|[;{\\s])${prop}\\s*:\\s*([^;]+)`, "g"))].map(
    (m) => (m[1] ?? "").trim(),
  );
  return values[values.length - 1];
}

function px(value: string | undefined): number {
  return Number(/^(\d+(?:\.\d+)?)px$/.exec(value ?? "")?.[1] ?? NaN);
}

describe("a tool row never widens the transcript", () => {
  test("the label keeps its line while it fits, and wraps inside the row when it does not", () => {
    const label = rule(".thinking-head .thinking-label");
    // It never gives way to a sibling: a short label next to a long target
    // stays whole and the target is what ends in an ellipsis.
    expect(decl(label, "flex")).toBe("0 0 auto");
    expect(decl(label, "max-width")).toBe("100%");
    // An MCP tool name is one long identifier: it has to break inside itself.
    expect(decl(label, "overflow-wrap")).toBe("anywhere");
  });

  test("the head wraps, so what trails a full-width label moves under it", () => {
    expect(decl(rule(".thinking-head"), "flex-wrap")).toBe("wrap");
  });

  test("what trails the label moves as one group, and only when the label's line lacks the room it needs", () => {
    const trail = rule(".thinking-trail");
    expect(decl(trail, "display")).toBe("inline-flex");
    expect(decl(trail, "align-items")).toBe("baseline");
    expect(decl(trail, "gap")).toBe("0 6px");
    // The basis is the room the group needs on the label's line: the duration
    // and a readable start of the target. Not the target's whole text, which
    // would push the group under every label with a long command.
    const [grow, shrink, basis] = (decl(trail, "flex") ?? "").split(/\s+/);
    expect(grow).toBe("1");
    expect(shrink).toBe("1");
    expect(basis).toMatch(/^\d+(\.\d+)?em$/);
    // The automatic minimum would be that whole text again.
    expect(decl(trail, "min-width")).toBe("0");
    // Capped at its own width: the head is as wide as its content, so a basis
    // wider than a short group would wrap it under a label it fits beside.
    expect(decl(trail, "max-width")).toBe("max-content");
    // A failed call also carries the failure marker, so it needs more.
    const failed = decl(rule(".thinking-trail--failed"), "flex-basis") ?? "";
    expect(parseFloat(failed)).toBeGreaterThan(parseFloat(basis ?? ""));
  });

  test("the target inside the group keeps its own width and ends in an ellipsis", () => {
    const target = rule(".tool-summary-target");
    expect(decl(target, "flex")).toBe("0 1 auto");
    expect(decl(target, "min-width")).toBe("0");
    expect(decl(target, "overflow")).toBe("hidden");
    expect(decl(target, "text-overflow")).toBe("ellipsis");
    expect(decl(target, "white-space")).toBe("nowrap");
  });

  test("a tool's raw output wraps inside its card, a long unbroken line included", () => {
    const pre = rule(".tool-result-pre");
    expect(decl(pre, "white-space")).toBe("pre-wrap");
    expect(decl(pre, "word-break")).toBe("break-word");
  });

  test("the duration never shrinks", () => {
    expect(decl(rule(".thinking-head .thinking-dur"), "flex")).toBe("0 0 auto");
  });
});

describe("an answer never widens the transcript", () => {
  test("a long link or word in the prose breaks where it has to", () => {
    expect(decl(rule(".md"), "overflow-wrap")).toBe("anywhere");
  });

  test("inline code is at most a line wide and wraps inside its chip", () => {
    const chip = rule(".md-inline-code");
    expect(decl(chip, "max-width")).toBe("100%");
    expect(decl(chip, "overflow-wrap")).toBe("anywhere");
  });

  test("a chip that wraps keeps its lines apart and a one-line chip keeps its height", () => {
    // The chip was 18px: a 10px line box and 5px + 3px of padding. A 10px line
    // box under 12px type overlaps the next line as soon as the text wraps, so
    // the line box grew to 14px and the padding gave the 4px back.
    const chip = rule(".md-inline-code");
    const line = px(decl(chip, "line-height"));
    const [top, , bottom] = (decl(chip, "padding") ?? "").split(/\s+/).map((v) => px(v));
    expect(px(decl(chip, "font-size"))).toBe(12);
    expect(line).toBeGreaterThanOrEqual(14);
    expect(line + (top ?? NaN) + (bottom ?? NaN)).toBe(18);
    // The text keeps its place in the chip: the middle of the line box sits
    // 10px below the chip's top edge, as it did with the 10px line box.
    expect((top ?? NaN) + line / 2).toBe(10);
  });
});
