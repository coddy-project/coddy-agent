import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { TypingDotsMessage } from "./TypingDotsMessage";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

test("renders three dots when generating", () => {
  render(<TypingDotsMessage />);
  const dots = document.querySelectorAll(".typing-dots-dot");
  expect(dots.length).toBe(3);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList shows typing dots when generating and no streaming assistant", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList hides typing dots when not generating", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
  ];
  render(<MessageList items={items} generating={false} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("MessageList hides typing dots when streaming assistant message is present", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "a1", type: "assistant_message", content: "Hi th", streaming: true },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("MessageList shows typing dots when generating with tool call in progress", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Do something" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read_file",
      kind: "read",
      status: "in_progress",
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList shows typing dots when generating between tool calls (no streaming text)", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read_file",
      kind: "read",
      status: "completed",
      resultText: "content",
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("without a status kind it renders bare dots and keeps the aria label", () => {
  render(<TypingDotsMessage />);
  const row = document.querySelector(".typing-dots");
  expect(row?.getAttribute("aria-live")).toBe("polite");
  expect(row?.getAttribute("aria-label")).toBe("Preparing response");
  expect(screen.queryByTestId("typing-dots-status")).toBeNull();
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
});

test("the status node is the fourth child so the dot animation stagger survives", () => {
  render(<TypingDotsMessage statusKind="tool" statusKey="status.read" />);
  const row = document.querySelector(".typing-dots");
  expect(row).not.toBeNull();
  expect(row!.children.length).toBe(4);
  for (let i = 0; i < 3; i++) {
    expect(row!.children[i]!.className).toContain("typing-dots-dot");
  }
  expect(row!.children[3]!.className).toContain("typing-dots-status");
});

test("renders the verb and the target", () => {
  render(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.read"
      statusTarget="…/ui/App.tsx"
      statusTargetFull="external/ui/src/ui/App.tsx"
    />,
  );
  expect(screen.getByText("Reading")).toBeInTheDocument();
  expect(screen.getByText("…/ui/App.tsx")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots-status").getAttribute("title")).toBe(
    "external/ui/src/ui/App.tsx",
  );
});

test("moves the live region off the dots and hides the ticking counter from AT", () => {
  render(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.read"
      startedAtMs={Date.now()}
    />,
  );
  const row = document.querySelector(".typing-dots");
  expect(row?.getAttribute("aria-live")).toBeNull();
  expect(row?.getAttribute("aria-label")).toBeNull();
  const text = document.querySelector(".typing-dots-status-text");
  expect(text?.getAttribute("role")).toBe("status");
  expect(text?.getAttribute("aria-live")).toBe("polite");
  expect(
    screen.getByTestId("typing-dots-elapsed").getAttribute("aria-hidden"),
  ).toBe("true");
});

test("counts elapsed seconds once per second", () => {
  vi.useFakeTimers();
  render(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.run"
      startedAtMs={Date.now() - 12_000}
    />,
  );
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("12s");
  act(() => {
    vi.advanceTimersByTime(1000);
  });
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("13s");
});

test("counts from its own stamp when no start time is supplied", () => {
  vi.useFakeTimers();
  render(<TypingDotsMessage statusKind="waiting" />);
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("0s");
  act(() => {
    vi.advanceTimersByTime(2000);
  });
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("2s");
});

test("escalates the waiting phrase at 15s and 60s", () => {
  vi.useFakeTimers();
  const { rerender } = render(
    <TypingDotsMessage statusKind="waiting" startedAtMs={Date.now() - 14_000} />,
  );
  expect(screen.getByText("Waiting for the model")).toBeInTheDocument();
  expect(document.querySelector(".typing-dots-status--slow")).toBeNull();

  act(() => {
    vi.advanceTimersByTime(2000);
  });
  expect(
    screen.getByText("The model is taking longer than usual"),
  ).toBeInTheDocument();
  expect(document.querySelector(".typing-dots-status--slow")).not.toBeNull();

  rerender(
    <TypingDotsMessage statusKind="waiting" startedAtMs={Date.now() - 61_000} />,
  );
  expect(
    screen.getByText("Still no response from the server"),
  ).toBeInTheDocument();
});

test("shows no counter while blocked on the user", () => {
  render(
    <TypingDotsMessage
      statusKind="permission"
      statusKey="status.awaitingPermission"
    />,
  );
  expect(screen.getByText("Waiting for your approval")).toBeInTheDocument();
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
});

test("restarts the counter when the step changes", () => {
  vi.useFakeTimers();
  const { rerender } = render(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.read"
      statusTarget="a.ts"
    />,
  );
  act(() => {
    vi.advanceTimersByTime(5000);
  });
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("5s");
  rerender(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.read"
      statusTarget="b.ts"
    />,
  );
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("0s");
});

test("clears its interval on unmount", () => {
  vi.useFakeTimers();
  const { unmount } = render(
    <TypingDotsMessage statusKind="tool" statusKey="status.read" />,
  );
  unmount();
  act(() => {
    vi.advanceTimersByTime(5000);
  });
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
});

test("MessageList renders the running tool's verb and path", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read",
      kind: "read",
      status: "in_progress",
      argsText: '{"path":"external/ui/src/ui/App.tsx"}',
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByText("Reading")).toBeInTheDocument();
  expect(
    screen.getByText("external/ui/src/ui/App.tsx", {
      selector: ".typing-dots-status-target",
    }),
  ).toBeInTheDocument();
});

test("MessageList reports an unresolved permission prompt instead of the tool", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "run_command",
      status: "in_progress",
      argsText: '{"command":"rm -rf build"}',
    },
    {
      id: "p1",
      type: "permission_prompt",
      payload: {
        sessionId: "sess_1",
        toolCall: { toolCallId: "call_1", title: "run_command" },
        options: [],
      },
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByText("Waiting for your approval")).toBeInTheDocument();
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
});
