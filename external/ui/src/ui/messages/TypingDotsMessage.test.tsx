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

test("MessageList keeps typing dots while the assistant message streams", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "a1", type: "assistant_message", content: "Hi th", streaming: true },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
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

test("a step counts from its own stamp when no start time is supplied", () => {
  vi.useFakeTimers();
  render(<TypingDotsMessage statusKind="tool" statusKey="status.run" />);
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

// The turn's own line: total time first, then what the model wrote, then what runs in
// the background, then the step (docs/plans/turn-progress.md).

test("before the first token the line is the turn clock and the waiting phrase", () => {
  vi.useFakeTimers();
  render(
    <TypingDotsMessage
      statusKind="waiting"
      startedAtMs={Date.now() - 57_000}
      turnStartedAtMs={Date.now() - 57_000}
    />,
  );
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe("57s");
  expect(
    screen.getByText("The model is taking longer than usual"),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("typing-dots-turn-tokens")).toBeNull();
  // The model's own phases carry no second clock next to the turn's.
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
  act(() => {
    vi.advanceTimersByTime(3000);
  });
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe(
    "1m 00s",
  );
});

test("the turn clock leads the line, ahead of the phrase", () => {
  render(
    <TypingDotsMessage
      statusKind="thinking"
      statusKey="status.thinking"
      turnStartedAtMs={Date.now() - 45_000}
      turnTokens={433}
    />,
  );
  const status = screen.getByTestId("typing-dots-status");
  expect(status.textContent).toMatch(/^45s.*433 tokens.*Thinking/);
});

test("generated tokens appear once there are any, shortened past a thousand", () => {
  const { rerender } = render(
    <TypingDotsMessage
      statusKind="writing"
      statusKey="status.writing"
      turnStartedAtMs={Date.now() - 5_000}
      turnTokens={0}
    />,
  );
  expect(screen.queryByTestId("typing-dots-turn-tokens")).toBeNull();
  rerender(
    <TypingDotsMessage
      statusKind="writing"
      statusKey="status.writing"
      turnStartedAtMs={Date.now() - 5_000}
      turnTokens={1}
    />,
  );
  expect(screen.getByTestId("typing-dots-turn-tokens").textContent).toBe(
    "1 token",
  );
  rerender(
    <TypingDotsMessage
      statusKind="writing"
      statusKey="status.writing"
      turnStartedAtMs={Date.now() - 5_000}
      turnTokens={13_540}
    />,
  );
  expect(screen.getByTestId("typing-dots-turn-tokens").textContent).toBe(
    "13.5k tokens",
  );
});

test("a tool step keeps its own clock after the phrase, next to the turn's", () => {
  vi.useFakeTimers();
  render(
    <TypingDotsMessage
      statusKind="tool"
      statusKey="status.run"
      statusTarget="make test"
      startedAtMs={Date.now() - 45_000}
      turnStartedAtMs={Date.now() - 125_000}
      turnTokens={1200}
    />,
  );
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe(
    "2m 05s",
  );
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("45s");
  act(() => {
    vi.advanceTimersByTime(1000);
  });
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe(
    "2m 06s",
  );
  expect(screen.getByTestId("typing-dots-elapsed").textContent).toBe("46s");
});

test("an operator gate keeps the turn clock and still has no step clock", () => {
  render(
    <TypingDotsMessage
      statusKind="permission"
      statusKey="status.awaitingPermission"
      turnStartedAtMs={Date.now() - 30_000}
    />,
  );
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe("30s");
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
});

test("running background tasks are named on the line and open the Tasks panel", () => {
  const onOpenTasks = vi.fn();
  render(
    <TypingDotsMessage
      statusKind="thinking"
      statusKey="status.thinking"
      turnStartedAtMs={Date.now() - 908_000}
      turnTokens={13_500}
      runningTasks={1}
      onOpenTasks={onOpenTasks}
    />,
  );
  const tasks = screen.getByTestId("typing-dots-turn-tasks");
  expect(tasks.tagName).toBe("BUTTON");
  expect(tasks.textContent).toBe("1 running task");
  expect(screen.getByTestId("typing-dots-status").textContent).toMatch(
    /^15m 08s.*13\.5k tokens.*1 running task.*Thinking/,
  );
  tasks.click();
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("no running tasks, no tasks segment", () => {
  render(
    <TypingDotsMessage
      statusKind="thinking"
      statusKey="status.thinking"
      turnStartedAtMs={Date.now() - 1_000}
      runningTasks={0}
      onOpenTasks={() => {}}
    />,
  );
  expect(screen.queryByTestId("typing-dots-turn-tasks")).toBeNull();
});

test("without a known turn start the line counts from when it appeared", () => {
  vi.useFakeTimers();
  render(<TypingDotsMessage statusKind="waiting" />);
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe("0s");
  act(() => {
    vi.advanceTimersByTime(2000);
  });
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe("2s");
});

test("the separator after the tasks segment is outside the button, so hover does not underline it", () => {
  render(
    <TypingDotsMessage
      statusKind="thinking"
      statusKey="status.thinking"
      turnStartedAtMs={Date.now() - 1_000}
      runningTasks={2}
      onOpenTasks={() => {}}
    />,
  );
  const button = screen.getByTestId("typing-dots-turn-tasks");
  // The middle dot is drawn by ::after of .typing-dots-turn-item.
  expect(button.className).not.toContain("typing-dots-turn-item");
  expect(button.parentElement?.className).toContain("typing-dots-turn-item");
});

// The turn has ended and the tasks it started have not: the same dots hold the tail of
// the transcript, carrying the count and nothing else.

test("after the turn the line is the dots and the count, with no turn numbers", () => {
  const onOpenTasks = vi.fn();
  render(
    <TypingDotsMessage
      tasksOnly={true}
      runningTasks={2}
      onOpenTasks={onOpenTasks}
    />,
  );
  expect(document.querySelectorAll(".typing-dots-dot").length).toBe(3);
  const status = screen.getByTestId("typing-dots-status");
  expect(status.className).toContain("typing-dots-status--tasks-only");
  expect(status.textContent).toBe("2 running tasks");
  expect(screen.queryByTestId("typing-dots-turn-elapsed")).toBeNull();
  expect(screen.queryByTestId("typing-dots-turn-tokens")).toBeNull();
  expect(screen.queryByTestId("typing-dots-elapsed")).toBeNull();
  expect(document.querySelector(".typing-dots-status-text")).toBeNull();
  screen.getByTestId("typing-dots-turn-tasks").click();
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("the count of a tasks-only line stays after the three dots as well", () => {
  render(<TypingDotsMessage tasksOnly={true} runningTasks={1} />);
  const row = document.querySelector(".typing-dots");
  expect(row).not.toBeNull();
  expect(row!.children.length).toBe(4);
  for (let i = 0; i < 3; i++) {
    expect(row!.children[i]!.className).toContain("typing-dots-dot");
  }
  expect(row!.children[3]!.className).toContain("typing-dots-status");
});

test("a tasks-only line with nothing running does not stand at all", () => {
  render(<TypingDotsMessage tasksOnly={true} runningTasks={0} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("MessageList keeps the dots after the turn while background tasks run", () => {
  const onOpenTasks = vi.fn();
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    { id: "a1", type: "assistant_message", content: "Started it." },
  ];
  render(
    <MessageList
      items={items}
      generating={false}
      runningTasks={2}
      onOpenTasks={onOpenTasks}
    />,
  );
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots-turn-tasks").textContent).toBe(
    "2 running tasks",
  );
  expect(screen.queryByTestId("typing-dots-turn-elapsed")).toBeNull();
  expect(screen.queryByTestId("typing-dots-status-text")).toBeNull();
});

test("a running turn carries the count once: the second line is not added", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
  ];
  render(<MessageList items={items} generating={true} runningTasks={2} />);
  expect(screen.queryAllByTestId("typing-dots").length).toBe(1);
  expect(screen.queryAllByTestId("typing-dots-turn-tasks").length).toBe(1);
});
