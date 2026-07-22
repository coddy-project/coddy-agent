import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SystemNoticeMessage } from "./SystemNoticeMessage";

afterEach(cleanup);

test("renders a retry button next to copy that calls onRetry", () => {
  const onRetry = vi.fn();
  render(
    <SystemNoticeMessage
      level="error"
      message="model did not respond (no output within 1m30s)"
      onRetry={onRetry}
    />,
  );
  // Copy stays available.
  expect(screen.getByTestId("system-message-copy")).toBeTruthy();
  const retry = screen.getByTestId("system-message-retry");
  expect(retry.getAttribute("title")).toBe("Refresh");
  fireEvent.click(retry);
  expect(onRetry).toHaveBeenCalledTimes(1);
});

test("hides the retry button when onRetry is not provided", () => {
  render(<SystemNoticeMessage level="error" message="oops" />);
  expect(screen.queryByTestId("system-message-retry")).toBeNull();
  expect(screen.getByTestId("system-message-copy")).toBeTruthy();
});
