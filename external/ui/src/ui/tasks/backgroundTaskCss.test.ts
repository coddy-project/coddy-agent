import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

test("the opener chip is styled from theme tokens and marks a live session", () => {
  const chip = ruleBody(".bgtask-chip {");
  expect(chip).toContain("var(--text)");
  const live = ruleBody(".bgtask-chip.is-running {");
  expect(live).toContain("var(--accent)");
});

test("task colors are derived from theme tokens, not hardcoded greys", () => {
  for (const selector of [
    ".bgtask-card-label {",
    ".bgtask-card-meta {",
    ".bgtask-finished-label {",
    ".bgtask-finished-meta {",
  ]) {
    expect(ruleBody(selector)).toContain("var(--text)");
  }
});

test("the panel is docked in the session rather than floating over the shell", () => {
  // It belongs to the chat that started the tasks, so it must not reuse the
  // History/Scheduler drawer machinery that overlays the whole shell.
  const panel = ruleBody(".bgtasks-panel {");
  expect(panel).toContain("right:");
  expect(css).not.toContain("bgtask-dock-drawer");
  expect(css).not.toContain("bgtask-dock-cluster");
});

test("the chat column yields the width the panel occupies", () => {
  // Otherwise the composer and transcript sit underneath the panel.
  expect(css).toContain(".shell-main.shell-tasks-open");
});

test("the running dot animation is disabled for reduced motion", () => {
  // styles.css has more than one reduced-motion block, so find the one that
  // actually covers the pulsing dot.
  const blocks = css.split("@media (prefers-reduced-motion: reduce)").slice(1);
  const covering = blocks.find((b) => b.slice(0, 400).includes(".bgtask-dot--running"));
  expect(covering, "no reduced-motion block covers .bgtask-dot--running").toBeDefined();
  expect(covering?.slice(0, 400)).toContain("animation: none");
});

test("phone layout gives the panel the screen and taller touch targets", () => {
  const idx = css.indexOf("@media (max-width: 1199px)");
  expect(idx).toBeGreaterThan(-1);
  expect(css.slice(idx)).toContain(".bgtasks-panel");
  expect(ruleBody(".bgtask-back {")).toContain("min-height: 36px");
});
