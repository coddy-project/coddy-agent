/**
 * Contract: play/stop button icons must remain correctly sized and free of
 * manual transform offsets that caused misalignment.
 *
 * Key invariants:
 * - Play icon is an SVG, never a bare Unicode glyph (▶), whose rendering
 *   is font-dependent and impossible to reliably center.
 * - Composer (42 px button) play SVG: 17×17 px  — ~40 % fill, matching the
 *   visual proportion that the scheduler play icon holds in its 34 px button.
 * - Scheduler (34 px button) play SVG: 14×14 px.
 * - Stop square stays at 14×14 px CSS block in both contexts.
 * - No hand-rolled `transform: translate(…)` hacks on the play glyph wrapper
 *   — those were the root cause of the "crooked icon" regression.
 * - No `transform: translateY(…)` on the scheduler stop glyph wrapper —
 *   that was shifting the stop square ~1 px above center.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));

function composerSrc(): string {
  return readFileSync(join(dir, "Composer.tsx"), "utf8");
}
function schedulerSrc(): string {
  return readFileSync(
    join(dir, "../scheduler/SchedulerJobsDrawer.tsx"),
    "utf8",
  );
}
function cssText(): string {
  return readFileSync(join(dir, "../../styles.css"), "utf8");
}

// ── JSX: icon glyph type ───────────────────────────────────────────────────

test("composer play button uses SVG, not bare Unicode ▶", () => {
  expect(composerSrc()).not.toContain("▶");
});

test("scheduler play button uses SVG, not bare Unicode ▶", () => {
  expect(schedulerSrc()).not.toContain("▶");
});

// ── JSX: icon dimensions ──────────────────────────────────────────────────

test("composer play SVG is 17×17 px (proportional to 42 px button)", () => {
  const src = composerSrc();
  // Match the SVG element that carries the play triangle path
  expect(src).toMatch(/width="17"[^>]*height="17"|height="17"[^>]*width="17"/);
});

test("scheduler play SVG is 14×14 px (proportional to 34 px button)", () => {
  const src = schedulerSrc();
  expect(src).toMatch(/width="14"[^>]*height="14"|height="14"[^>]*width="14"/);
});

// ── CSS: no manual offset hacks on glyph wrappers ─────────────────────────

test("play glyph wrapper has no transform offset in CSS", () => {
  // Any transform on .composer-run-icon--play .composer-send-glyph would
  // re-introduce the hand-tuned centering hack that broke across fonts.
  expect(cssText()).not.toMatch(
    /composer-run-icon--play[^}]*\.composer-send-glyph[^}]*transform\s*:/s,
  );
});

test("scheduler stop glyph wrapper has no translateY offset in CSS", () => {
  // translateY(-0.08em) was shifting the stop square ~1 px above center.
  expect(cssText()).not.toMatch(
    /scheduler-job-run-icon[^}]*composer-run-icon--stop[^}]*\.composer-send-glyph[^}]*transform\s*:\s*translateY/s,
  );
});

// ── CSS: stop square size ─────────────────────────────────────────────────

test("stop square is 14×14 px", () => {
  const css = cssText();
  const block = css.match(/\.composer-stop-square\s*\{([^}]+)\}/s);
  expect(block).not.toBeNull();
  expect(block![1]).toMatch(/width:\s*14px/);
  expect(block![1]).toMatch(/height:\s*14px/);
});
