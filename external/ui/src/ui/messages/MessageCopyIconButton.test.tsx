import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { pageWithoutClipboardApi, selectedOnce } from "../clipboardTestKit";
import { MessageCopyIconButton } from "./MessageCopyIconButton";

afterEach(() => cleanup());

// The copy control of a message (a user's message, a system row) on a page
// without navigator.clipboard (issue #566).
test("message copy works without a clipboard API, refused or not", async () => {
  const page = pageWithoutClipboardApi(false);
  render(
    <MessageCopyIconButton
      textToCopy="# Title"
      tooltip="Copy message"
      ariaLabel="Copy message"
      dataTestId="message-copy"
    />,
  );
  await act(async () => {
    screen.getByTestId("message-copy").click();
  });
  expect(page.copies).toEqual(selectedOnce("# Title"));
  expect(document.querySelector("textarea")).toBeNull();
});
