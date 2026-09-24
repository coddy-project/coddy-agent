import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

import { PHONE_MAX_WIDTH_PX } from "../shellBreakpoint";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

test("patch previews expose a stable, readable line-number gutter", () => {
  const css = cssText();
  const gutter = css.match(
    /\.permission-preview-diff\s+\.diff-gutter\s*\{[^}]*\}/s,
  );
  const numbers = css.match(
    /\.permission-preview-diff\s+\.diff-no\s*\{[^}]*\}/s,
  );

  expect(gutter?.[0]).toMatch(/background:/);
  expect(numbers?.[0]).toMatch(/min-width:\s*4ch/);
  expect(numbers?.[0]).toMatch(/font-variant-numeric:\s*tabular-nums/);
});

test("code preview titles align with their content", () => {
  const css = cssText();
  const bar = css.match(/\.permission-preview-bar\s*\{[^}]*\}/s);
  const code = css.match(/\.permission-preview-code\s*\{[^}]*\}/s);

  expect(bar?.[0]).toMatch(/padding:\s*6px\s+8px\s+6px\s+12px/);
  expect(code?.[0]).toMatch(/padding:\s*10px\s+12px/);
});

test("expanded previews stay bounded and scroll internally", () => {
  const css = cssText();
  const scrollingViewport = css.match(
    /\.permission-preview-viewport--scroll\s*\{[^}]*\}/s,
  );
  const staticViewport = css.match(
    /\.permission-preview-viewport--static\s*\{[^}]*\}/s,
  );

  expect(scrollingViewport?.[0]).toMatch(/overflow-y:\s*auto/);
  expect(scrollingViewport?.[0]).not.toMatch(/max-height:\s*none/);
  expect(staticViewport?.[0]).toMatch(/max-height:\s*none/);
  expect(staticViewport?.[0]).toMatch(/overflow:\s*visible/);
});

test("overflow toggles are left-aligned and taller on mobile", () => {
  const css = cssText();
  const toggle = css.match(/\.tool-overflow-toggle\s*\{[^}]*\}/s);
  const resultFooter = css.match(
    /\.coddy-tool-call-body\s*>\s*\.tool-call-result-card\s*\+\s*\.tool-result-toggle-row\s*\{[^}]*\}/s,
  );
  const mobile = css.match(
    new RegExp(
      `@media\\s*\\(max-width:\\s*${PHONE_MAX_WIDTH_PX}px\\)\\s*\\{[\\s\\S]*?\\.tool-overflow-toggle\\s*\\{[^}]*\\}`,
    ),
  );

  expect(toggle?.[0]).toMatch(/align-self:\s*flex-start/);
  expect(toggle?.[0]).toMatch(/margin:\s*-1px\s+0\s+0\s+8px/);
  expect(resultFooter?.[0]).toMatch(/margin-top:\s*-8px/);
  expect(mobile?.[0]).toMatch(/min-height:\s*36px/);
});

test("plan exit uses themed progress and semantic completion colors", () => {
  const css = cssText();
  const preview = css.match(/\.plan-exit-preview\s*\{[^}]*\}/s);
  const completed = css.match(
    /\.plan-exit-preview--completed\s+\.plan-exit-preview-icon\s*\{[^}]*\}/s,
  );

  expect(preview?.[0]).toMatch(/var\(--accent\)/);
  expect(completed?.[0]).toMatch(/var\(--coddy-todo-completed\)/);
});

test("the shell block is an inset panel with its own prompt column", () => {
  const css = cssText();
  const block = css.match(/\.permission-preview-shell\s*\{[^}]*\}/s);
  const prompt = css.match(/\.permission-preview-shell-prompt\s*\{[^}]*\}/s);
  const code = css.match(/\.permission-preview-shell-code\s*\{[^}]*\}/s);

  // Nested inside the card, so the command reads as input rather than as body text.
  expect(block?.[0]).toMatch(/border:/);
  expect(block?.[0]).toMatch(/border-radius:/);
  expect(block?.[0]).toMatch(/display:\s*grid/);
  // Prompt, command, copy: the control is the block's own last column.
  expect(block?.[0]).toMatch(
    /grid-template-columns:\s*auto\s+minmax\(0,\s*1fr\)\s+auto/,
  );
  expect(prompt?.[0]).toMatch(/var\(--accent\)/);
  expect(code?.[0]).toMatch(/white-space:\s*pre-wrap/);
  const copy = css.match(/\.permission-preview-shell\s+\.md-copy\s*\{[^}]*\}/s);
  expect(copy?.[0]).toMatch(/position:\s*static/);
});

test("the no-argument action bar reuses the todo status mark", () => {
  const css = cssText();
  const inBar = css.match(
    /\.permission-preview-bar\s*>\s*\.todo-tool-preview-mark\s*\{[^}]*\}/s,
  );
  const completed = css.match(
    /\.todo-tool-preview-row--completed\s+\.todo-tool-preview-mark,\s*\n\.todo-tool-preview-mark--completed\s*\{[^}]*\}/s,
  );

  // The standalone mark shares every state rule with the rows of the plan cards.
  expect(inBar).not.toBeNull();
  expect(completed?.[0]).toMatch(/var\(--coddy-todo-completed\)/);
});

test("the copy control is an icon, sized for its glyph on both surfaces", () => {
  const css = cssText();
  const base = css.match(/^\.md-copy\s*\{[^}]+\}/m);
  const glyph = css.match(/\.md-copy__glyph\s*\{[^}]*\}/s);

  expect(base?.[0]).toMatch(/line-height:\s*0/);
  expect(glyph?.[0]).toMatch(/display:\s*block/);
});
