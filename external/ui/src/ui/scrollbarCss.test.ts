import { expect, test } from "vitest";
import { stylesSource } from "../stylesSource";

function squash(): string {
  return stylesSource().replace(/\s+/g, " ");
}

/** The root block of styles/_base.scss, told apart by the one declaration only it carries. */
function rootBlock(): string {
  const css = squash();
  const block = /html \{[^}]*-webkit-text-size-adjust[^}]*\}/.exec(css);
  expect(block, "the root html block of styles/_base.scss").not.toBeNull();
  return block![0];
}

// One scrollbar for the whole app, in the theme's own ink rather than the one
// the platform paints: the engine's default bar is wide and light-grey, which
// on a dark canvas reads as a strip of another program.
test("the app's scrollbars carry the theme's ink", () => {
  expect(rootBlock()).toMatch(
    /scrollbar-color: color-mix\(in srgb, var\(--text\) \d+%, transparent\) transparent;/,
  );
});

// The width goes on every element and not on the root: `scrollbar-color`
// inherits and `scrollbar-width` does not, so a root asking alone leaves every
// scroller below it - the transcript, a picker, a code block - with the
// platform's wide bar while its thumb already carries the theme's ink.
test("the width reaches a scroller that is not the root", () => {
  const universal = /(?:^|\}) ?\* \{([^}]*)\}/.exec(squash())?.[1] ?? "";
  expect(universal).toMatch(/scrollbar-width: thin;/);
});

// The transcript's scroller reserves a gutter on both of its edges, and the
// docked composer pads itself by that number to stay on the transcript's centre
// line. A thin bar takes 10px per edge where the wide one took 15, so the token
// follows the bar: left at 14 it pushed the composer off the column by 4px on
// each side.
test("the chat's scrollbar gutter matches the thin bar", () => {
  const stack = /\.chat-stack \{([^}]*)\}/.exec(squash())?.[1] ?? "";
  expect(stack).toMatch(/--coddy-chat-scrollbar-gutter: 10px;/);
});
