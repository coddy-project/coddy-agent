import React from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AssistantMessage } from "./AssistantMessage";

afterEach(() => cleanup());

test("assistant hides footer while streaming", () => {
  render(<AssistantMessage content="Hi" streaming />);
  expect(screen.queryByTestId("assistant-message-copy")).toBeNull();
});

test("assistant shows copy after stream and copies raw markdown", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(globalThis.navigator, "clipboard", {
    value: { writeText },
    configurable: true,
    writable: true,
  });
  render(
    <AssistantMessage
      content="# Title"
      streaming={false}
      createdAtUtc="2026-01-01T00:00:00.000Z"
    />,
  );
  const copyBtn = screen.getByTestId("assistant-message-copy");
  expect(copyBtn).toHaveAttribute("title", "Copy message");
  copyBtn.click();
  expect(writeText).toHaveBeenCalledWith("# Title");
});
