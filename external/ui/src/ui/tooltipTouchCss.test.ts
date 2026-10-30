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
