import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// Regression: the code-block "Copy" button (.md-copy) hardcoded a dark
// background rgba(20, 20, 22, ...) with no light-theme override, so it
// rendered as a dark blob on the light theme. Background must be theme-aware.
test("code-block copy button background is theme-aware (not a dark blob on light)", () => {
  const css = cssText();
  const base = /^\.md-copy\s*\{[^}]+\}/m.exec(css);
  const hover = /^\.md-copy:hover\s*\{[^}]+\}/m.exec(css);
  expect(base).not.toBeNull();
  expect(hover).not.toBeNull();
  // No hardcoded near-black background on either state.
  expect(base![0]).not.toMatch(/background:\s*rgba\(\s*20,\s*20,\s*22/);
  expect(hover![0]).not.toMatch(/background:\s*rgba\(\s*20,\s*20,\s*22/);
  // Derives from the shared glass-panel token so it flips with the theme.
  expect(base![0]).toMatch(/background:[^;]*var\(--coddy-glass-panel-bg\)/);
});

// The copy button sits on the middle of a code block's first line, so a
// one-line block (a shell command) has it centred. The block fixes its own
// line box: inherited from a reading page (15px at 1.68) it grew the block
// and left the button riding high.
test("code-block copy button is centred on the first line of the block", () => {
  const css = cssText();
  const pre = /^\.md-code pre\s*\{([^}]+)\}/m.exec(css)?.[1] ?? "";
  const copy = /^\.md-copy\s*\{([^}]+)\}/m.exec(css)?.[1] ?? "";
  const glyph = /\.md-copy__glyph\s*\{([^}]*)\}/s.exec(css)?.[1] ?? "";
  const px = (block: string, prop: string) => {
    const m = new RegExp(`(?:^|;|\\s)${prop}:\\s*([\\d.]+)px`).exec(block);
    return m ? Number(m[1]) : NaN;
  };
  const fontSize = px(pre, "font-size");
  const lineHeight = Number(/line-height:\s*([\d.]+)\s*;/.exec(pre)?.[1]);
  const padTop = Number(/padding:\s*(\d+)px/.exec(pre)?.[1]);
  expect(fontSize).toBe(12);
  expect(lineHeight).toBe(1.5);
  const lineCentre = 1 /* border */ + padTop + (fontSize * lineHeight) / 2;
  const buttonHeight =
    2 /* border */ + 2 * px(copy, "padding") + px(glyph, "height");
  const buttonCentre = px(copy, "top") + buttonHeight / 2;
  expect(buttonCentre).toBe(lineCentre);
});

// iOS Safari hit-tests a scrolling block (a pre with overflow: auto) above an
// earlier absolutely positioned sibling that has no z-index: the button is
// painted over the code and every tap lands on the pre, so nothing is copied
// (seen on an iPhone, issue #566). A copy button that is positioned takes a
// z-index, whichever block it sits over: a code block, a figure's source, the
// command of a background task. The ones in a flex or grid row are static and
// overlap nothing.
test("a copy button laid over a scrolling block has a positive z-index", () => {
  const css = cssText();
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({
    selectors: (m[1] ?? "")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .split(",")
      .map((sel) => sel.trim()),
    body: m[2] ?? "",
  }));
  const decl = (body: string, prop: string) =>
    new RegExp(`(?:^|[;\\s])${prop}:\\s*([^;]+)`).exec(body)?.[1]?.trim();
  const isCopy = (sel: string) => /(^|[\s>+~])\.md-copy$/.test(sel);
  const base = rules.find((r) => r.selectors.includes(".md-copy"));
  expect(base).toBeDefined();
  expect(decl(base!.body, "position")).toBe("absolute");
  const baseZ = Number(decl(base!.body, "z-index"));
  expect(baseZ).toBeGreaterThan(0);
  const overlays: string[] = [];
  for (const rule of rules) {
    for (const sel of rule.selectors.filter(isCopy)) {
      const position = decl(rule.body, "position") ?? "absolute";
      if (position === "static") continue;
      overlays.push(sel);
      const z = Number(decl(rule.body, "z-index") ?? baseZ);
      expect(
        z,
        `${sel} sits over a block and has no positive z-index`,
      ).toBeGreaterThan(0);
    }
  }
  // The rules that lay a copy button over a block today.
  expect(overlays).toEqual(
    expect.arrayContaining([
      ".md-copy",
      ".md-figure-source > .md-copy",
      ".bgtask-card-command .md-copy",
    ]),
  );
});
