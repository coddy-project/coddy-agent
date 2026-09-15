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

// The button hangs off the top edge of the docked block, so it rides with the
// composer at every breakpoint instead of being placed against the viewport.
test("the scroll-to-bottom button sits above the composer, against its right edge", () => {
  const body = ruleBody(".chat-scroll-bottom {");
  expect(body).toMatch(/position:\s*absolute/);
  expect(body).toMatch(/bottom:\s*100%/);
  expect(body).toMatch(/right:\s*0/);
});

test("the button is a circle on the shared glass tokens, so every theme carries it", () => {
  const body = ruleBody(".chat-scroll-bottom {");
  expect(body).toMatch(/border-radius:\s*50%/);
  expect(body).toContain("var(--coddy-glass-panel-bg)");
  expect(body).toContain("var(--coddy-glass-panel-border)");
});

// A 34px circle wants a shadow of its own size, not the diffuse cast the full
// panel token (0 14px 44px) draws under a composer-sized card.
test("the circle lifts off the transcript on a small shadow, tuned per theme", () => {
  const body = ruleBody(".chat-scroll-bottom {");
  expect(body).toMatch(/box-shadow:\s*[^;]*0 3px 10px/);
  expect(body).not.toContain("var(--coddy-glass-panel-shadow)");
  expect(ruleBody('[data-theme="light"] .chat-scroll-bottom {')).toMatch(
    /box-shadow:/,
  );
});

// The node stays mounted for the whole chat: an unmounted button cannot
// animate its way out, and the reader should see it leave as well as arrive.
test("both states are a transition on one mounted node, not a mount and unmount", () => {
  const hidden = ruleBody(".chat-scroll-bottom {");
  expect(hidden).toMatch(/opacity:\s*0/);
  expect(hidden).toMatch(/pointer-events:\s*none/);
  expect(hidden).toMatch(/transition:/);

  const shown = ruleBody(".chat-scroll-bottom.is-visible {");
  expect(shown).toMatch(/opacity:\s*1/);
  expect(shown).toMatch(/pointer-events:\s*auto/);
  expect(shown).toMatch(/transition:/);
});

test("both transitions are dropped for readers who asked for less motion", () => {
  const block =
    /@media \(prefers-reduced-motion: reduce\)\s*\{[\s\S]*?\.chat-scroll-bottom,\s*\.chat-scroll-bottom\.is-visible\s*\{[^}]*\}/m.exec(
      css,
    );
  expect(
    block,
    "no reduced-motion rule for .chat-scroll-bottom",
  ).not.toBeNull();
  expect(block![0]).toMatch(/transition:\s*none/);
});
