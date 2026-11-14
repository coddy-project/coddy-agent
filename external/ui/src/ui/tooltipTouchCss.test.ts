import { expect, test } from "vitest";
import { stylesSource } from "../stylesSource";

const css = stylesSource().replace(/\s+/g, " ");

// A touch leaves :hover on whatever it landed on, so a tooltip opened by :hover
// stays up after a tap, and comes back when a panel over the control goes away.
// iOS also holds the click of a tap that reveals something by :hover, which is
// how a first tap on a control did nothing.
test("a tooltip opens on hover only where there is a real pointer", () => {
  for (const reveal of [
    ".chat-view-tip-host:hover .chat-view-tip",
    ".rail-tip-host:hover .rail-tip",
    ".composer-context-tip-host:hover .rail-tip",
    ".composer-goal-tip-host:hover .rail-tip",
  ]) {
    expect(css, reveal).toContain(
      `@media (hover: hover) { ${reveal} { opacity: 1; }`,
    );
  }
});

test("a tooltip opens by focus only for a keyboard", () => {
  expect(css).toContain(
    ".chat-view-tip-host:has(.chat-view-btn:focus-visible) .chat-view-tip { opacity: 1; }",
  );
  expect(css).toContain(
    ".rail-tip-host:has(:focus-visible) .rail-tip { opacity: 1; }",
  );
  expect(css).toContain(
    ".composer-context-tip-host:focus-visible .rail-tip { opacity: 1; }",
  );
  // A tap focuses its button, and :focus-within would show the tip for it.
  expect(css).not.toMatch(/:focus-within \.rail-tip \{ opacity: 1;/);
});

// The blur makes the composer card a stacking context, and the plate joined to its
// top stands over it (z-index: 2), so the z-index of a tooltip inside the card did
// nothing there: the context ring's tip opened upward and went under the plate.
test("a tooltip of the composer card stands over the plate joined to it", () => {
  expect(css).toContain(
    "@media (hover: hover) { .composer-card:has(.rail-tip-host:hover, .composer-context-tip-host:hover) { z-index: 3; }",
  );
  expect(css).toMatch(
    /\.composer-card:has\( ?\.rail-tip-host :focus-visible, \.composer-context-tip-host:focus-visible ?\) \{ z-index: 3; \}/,
  );
});

// Regression: the ring's tip stands 12px above the ring, and it carried the
// rail's shadow - 10px down over 28px of blur - which washed grey over the
// ring and the bar under it. Cast upward it still spilled under its own
// bottom edge, so the tip carries no shadow at all: an opaque plate with a
// border is what tells it from the transcript.
test("the tip above the context ring draws no shadow", () => {
  expect(css).toMatch(
    /\.composer-context-tip-host \.composer-context-tip\.rail-tip \{[^}]*box-shadow: none;/,
  );
  // And no palette keeps a token nothing draws with.
  expect(css).not.toContain("--coddy-tip-shadow-above");
});

// The panel of the plate and the card draws the shadow while a tip is up -
// not the wrap, which also holds the queue and the banners above the plate.
// On a touch device a tap leaves :hover behind, so the hover rules run only
// where a real pointer hovers; :focus-visible stays for the keyboard.
test("the composer shadow stays under the plate while a tip is up", () => {
  expect(css).toContain(
    "@media (hover: hover) { .composer-panel:has(.rail-tip-host:hover, .composer-context-tip-host:hover) { border-radius: 12px; box-shadow: var(--coddy-glass-panel-shadow); }",
  );
  expect(css).toContain(
    "@media (hover: hover) { .composer-card:has(.rail-tip-host:hover, .composer-context-tip-host:hover) { box-shadow: none; }",
  );
  expect(css).toMatch(
    /\.composer-panel:has\( ?\.rail-tip-host :focus-visible, \.composer-context-tip-host:focus-visible ?\) \{ border-radius: 12px; box-shadow: var\(--coddy-glass-panel-shadow\); \}/,
  );
  expect(css).toMatch(
    /\.composer-card:has\( ?\.rail-tip-host :focus-visible, \.composer-context-tip-host:focus-visible ?\) \{ box-shadow: none; \}/,
  );
  // The wrap holds the queue and the banners too: a shadow on it would
  // cover them, so it is not the element that draws it.
  expect(css).not.toMatch(
    /\.composer-wrap:has\([^)]*rail-tip[^)]*\)[^{]*\{[^}]*box-shadow/,
  );
});

test("the composer shadow has the same owner before and during a tooltip", () => {
  expect(css).toContain(
    ".composer-panel { border-radius: 12px; box-shadow: var(--coddy-glass-panel-shadow); }",
  );
  expect(css).toContain(".composer-panel .composer-card { box-shadow: none; }");
});

// A finger is wider than a cursor and the control beside the ring stops the
// turn, so the two stand further apart wherever the pointer is coarse.
test("the ring keeps its distance from the stop control on a touch screen", () => {
  expect(css).toMatch(
    /@media \(max-width: 599px\), \(any-hover: none\) and \(any-pointer: coarse\) \{ \.composer-bar-actions \{ gap: 20px; \}/,
  );
  expect(css).toMatch(/\.composer-bar-actions \{[^}]*gap: 10px;/);
});

test("the plus of the rail changes its frame and its ground under the pointer", () => {
  expect(css).toMatch(
    /@media \(hover: hover\) \{ \.rail-pill \.rail-hit\.rail-new-chat:hover \{ border-color: var\(--accent\); background: color-mix\(in srgb, var\(--accent\) 28%/,
  );
});

// A row of the wide rail takes the accent for its label under the pointer, which
// on the plus's accent ground could not be read.
test("the label of the new chat row stays readable on its accent ground", () => {
  expect(css).toMatch(
    /\.rail-pill \.rail-hit\.rail-new-chat:hover \.rail-nav-label \{ color: var\(--coddy-tip-fg\);/,
  );
});
