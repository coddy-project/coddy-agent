/**
 * Contract: History/sessions drawer must be a pure overlay — it must never push
 * the chat canvas aside. Scheduler and Settings are also fixed overlays and the
 * three drawers must be mutually exclusive (no side-by-side dual-drawer layout).
 *
 * Also encodes two CSS structural invariants discovered during bug-fixing:
 *
 * 1. Nav rail z-index must exceed the backdrop scrim so the rail stays
 *    unscrimmed and clickable while any drawer is open (no need to close
 *    the current drawer before switching to another).
 *
 * 2. The fade-in animation must live on .messages-inner, NOT on .chat-stack.
 *    An animation on .chat-stack promotes it to a GPU compositing layer;
 *    backdrop-filter on descendants (.chat-header, .composer-card) then only
 *    blurs what is behind the compositing group (the dark background) instead
 *    of what is visually behind the element — breaking the frosted-glass effect
 *    permanently (browsers keep the layer even after opacity reaches 1).
 */
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

// ── Drawer overlay layout ─────────────────────────────────────────────────

test("shell-history-open does not push chat canvas via padding-left", () => {
  const css = cssText();
  // The selector that previously added padding-left to shift the chat must not exist.
  expect(css).not.toMatch(
    /shell-history-open\s*(?:>|[^\{]*)\s*\.main\s*\{[^}]*padding-left/s,
  );
});

test("no dual-drawer beside-scheduler CSS positioning for History", () => {
  const css = cssText();
  expect(css).not.toContain("shell-history-beside-scheduler");
  expect(css).not.toContain("sessions-drawer-beside-scheduler");
});

// ── Nav rail z-index: stays above backdrop so drawers are switchable ──────

test("desktop .rail-column z-index exceeds .backdrop z-index", () => {
  const css = cssText();
  // match() returns the FIRST occurrence, which is the base (desktop) rule;
  // @media overrides come later in the file and are ignored here.
  const railMatch = css.match(/\.rail-column\s*\{[^}]*z-index:\s*(\d+)/);
  const backdropMatch = css.match(/\.backdrop\s*\{[^}]*z-index:\s*(\d+)/);
  expect(railMatch, ".rail-column must set z-index").not.toBeNull();
  expect(backdropMatch, ".backdrop must set z-index").not.toBeNull();
  expect(parseInt(railMatch![1], 10)).toBeGreaterThan(
    parseInt(backdropMatch![1], 10),
  );
});

// ── Frosted glass: animation must not sit on .chat-stack ─────────────────

test(".chat-stack has no animation property (would break backdrop-filter on descendants)", () => {
  const css = cssText();
  // Any animation on .chat-stack creates a GPU compositing layer whose
  // backdrop-filter descendants can no longer blur sibling content.
  expect(css).not.toMatch(/\.chat-stack\s*\{[^}]*\banimation\s*:/s);
});

test(".messages-inner carries the chat-fade-in animation instead of .chat-stack", () => {
  const css = cssText();
  expect(css).toMatch(/\.messages-inner\s*\{[^}]*\banimation\s*:/s);
});
