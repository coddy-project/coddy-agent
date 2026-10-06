import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));

function cssText(): string {
  return readFileSync(join(dir, "../../styles.css"), "utf8");
}

// The wand sits in the field's top right corner instead of a row of its own,
// so the placeholder starts at the top of the card; the text keeps clear of it,
// and the mirror that draws the text keeps the textarea's padding to the pixel.
test("prompt enhancement control sits in the field's corner and the text keeps clear of it", () => {
  const css = cssText();
  const block = css.match(/\.composer-enhance-btn\s*\{([^}]+)\}/s);
  expect(block).not.toBeNull();
  expect(block![1]).toMatch(/position:\s*absolute/);
  expect(block![1]).toMatch(/top:\s*12px/);
  expect(block![1]).toMatch(/right:\s*12px/);
  expect(block![1]).toMatch(/width:\s*24px/);
  expect(block![1]).toMatch(/height:\s*24px/);
  const ta = css.match(/\ntextarea#composer\s*\{([^}]+)\}/s)![1]!;
  const mirror = css.match(/\n\.composer-mirror-inner\s*\{([^}]+)\}/s)![1]!;
  const pad = (body: string) => /padding:\s*([^;]+);/.exec(body)![1]!.trim();
  expect(pad(ta)).toBe("13px 44px 10px 16px");
  expect(pad(mirror)).toBe(pad(ta));
});

// The first line of the field - the placeholder or the draft - stands level
// with the wand: the middle of the line box is the middle of the button, at the
// 15px desktop type and at the 16px type of a phone or a touch screen alike.
test("the field's first line is level with the wand", () => {
  const css = cssText();
  const wand = css.match(/\.composer-enhance-btn\s*\{([^}]+)\}/s)![1]!;
  const wandMiddle =
    parseFloat(/top:\s*([\d.]+)px/.exec(wand)![1]!) +
    parseFloat(/height:\s*([\d.]+)px/.exec(wand)![1]!) / 2;
  const ta = css.match(/\ntextarea#composer\s*\{([^}]+)\}/s)![1]!;
  const top = parseFloat(/padding:\s*([\d.]+)px/.exec(ta)![1]!);
  // 15px type at a 1.5 line height on a desktop.
  expect(Math.abs(top + (15 * 1.5) / 2 - wandMiddle)).toBeLessThanOrEqual(0.5);
  // 16px type on a phone or a touch screen, with its own top padding.
  const touch = css.slice(
    css.indexOf("@media (max-width: 599px), (any-hover: none) and (any-pointer: coarse) {\n  /* The mirror draws"),
  );
  const block = touch.slice(0, touch.indexOf("}") + 1);
  expect(block).toMatch(/font-size:\s*16px/);
  const touchTop = parseFloat(/padding-top:\s*([\d.]+)px/.exec(block)![1]!);
  expect(Math.abs(touchTop + (16 * 1.5) / 2 - wandMiddle)).toBeLessThanOrEqual(0.5);
});

// Git's count on the plate and the wand under it end on one vertical line.
test("git's count on the plate lines up with the wand under it", () => {
  const css = cssText();
  const bar = css.match(/\n\.workspace-bar\s*\{([^}]+)\}/s)![1]!;
  const padding = /padding:\s*([^;]+);/.exec(bar)![1]!.trim().split(/\s+/);
  const right = parseFloat(padding[1]!);
  const wand = css.match(/\.composer-enhance-btn\s*\{([^}]+)\}/s)![1]!;
  expect(right).toBe(parseFloat(/right:\s*([\d.]+)px/.exec(wand)![1]!));
});

test("the picks of the plate are its own flex items, not a nested box", () => {
  // The folder, branch and worktree picks dissolve into the plate's row
  // (display: contents), so each shrinks with its own ellipsis.
  const css = cssText();
  const block = css.match(/\.workspace-bar-picks\s*\{([^}]+)\}/s);
  expect(block).not.toBeNull();
  expect(block![1]).toMatch(/display:\s*contents/);
});
