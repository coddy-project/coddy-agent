import { expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

// On the stacked shell the title stands 16px under the top bar at rest: the bar's
// 4px of slack and the stack gap. The first message keeps that same room under the
// title, or the title reads as attached to the transcript and apart from the menu.
test("the room under the chat title matches the room above it on the stacked shell", () => {
  // From the stacked block on: the partial carries a desktop rule for the same
  // head above it, which pads it over the chat header.
  const css = stylesPartial("shell/stacked")
    .replace(/\s+/g, " ")
    .split("@media (max-width: 1199px) {")[1];
  const head =
    /\.chat-scroll-sticky-head \{([^}]*)\}/.exec(css ?? "")?.[1] ?? "";
  expect(head).toContain(
    "padding-bottom: calc(var(--coddy-mobile-chat-stack-gap) + 4px);",
  );
});
