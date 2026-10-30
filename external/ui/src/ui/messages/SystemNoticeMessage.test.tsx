import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { formatUtcToLocalFullDetail } from "./formatMessageTime";
import { SystemNoticeMessage } from "./SystemNoticeMessage";

afterEach(cleanup);

test("a finished turn keeps the live line without its phrase, the time on hover", () => {
  const createdAtUtc = "2026-10-30T10:05:00Z";
  render(
    <SystemNoticeMessage
      level="notice"
      message="Generation complete"
      createdAtUtc={createdAtUtc}
      generation={{
        outputTokens: 1200,
        tokensPerSecond: 9.8,
        elapsedMs: 81_000,
      }}
    />,
  );
  const row = screen.getByTestId("generation-summary");
  // The row is built of the live line's own parts, so it shares its look: the
  // mark stands where it stood, and the phrase of the step is gone with the step.
  const line = row.querySelector(".typing-dots");
  expect(line?.querySelector(".coddy-work-indicator")).not.toBeNull();
  expect(row.querySelector(".typing-dots-status-text")).toBeNull();
  const parts = [...row.querySelectorAll(".typing-dots-turn-item")].map(
    (el) => el.textContent,
  );
  expect(parts).toEqual(["1m 21s", "1.2k tokens · 9.8 tok/s"]);
  // When it finished is the line's tooltip.
  expect(line).toHaveAttribute(
    "title",
    formatUtcToLocalFullDetail(createdAtUtc),
  );
  expect(screen.queryByTestId("system-message-copy")).toBeNull();
});

test("a turn saved before its duration was kept shows the rest of the row", () => {
  render(
    <SystemNoticeMessage
      level="notice"
      message="Generation complete"
      generation={{ outputTokens: 1200, tokensPerSecond: 9.8 }}
    />,
  );
  expect(screen.getByTestId("generation-summary")).toHaveTextContent(
    "1.2k tokens · 9.8 tok/s",
  );
});

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

test("renders a notice row as a status without the retry control", () => {
  const { container } = render(
    <SystemNoticeMessage
      level="notice"
      message="Hooks file .coddy/hooks.json is not approved for this workspace"
      onRetry={vi.fn()}
    />,
  );
  const row = container.querySelector(".msg-system");
  expect(row?.getAttribute("role")).toBe("status");
  expect(row?.classList.contains("msg-system-notice")).toBe(true);
  expect(screen.queryByTestId("system-message-retry")).toBeNull();
  expect(screen.getByTestId("system-message-copy")).toBeTruthy();
});
