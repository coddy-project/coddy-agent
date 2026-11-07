import { afterEach, expect, test } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";

afterEach(cleanup);

const summary: TranscriptItem = {
  id: "generation_2026-10-30T10:00:00.123456789Z",
  type: "system_notice",
  level: "notice",
  message: "Generation complete",
  generation: { outputTokens: 100 },
};
const wake: TranscriptItem = { id: "wake", type: "background_wake", tasks: [] };
const tool: TranscriptItem = {
  id: "tool",
  type: "tool_call",
  toolCallId: "tc",
  title: "read",
  status: "completed",
};
const todo = [{ content: "Check", status: "in_progress" }];

test("an invisible wake cannot leave two adjacent status lines above the checklist", () => {
  const { rerender } = render(
    <MessageList items={[summary, wake]} generating todoPlan={todo} />,
  );
  expect(screen.queryByTestId("generation-summary")).toBeNull();
  expect(screen.getByTestId("typing-dots").nextElementSibling).toBe(
    screen.getByTestId("todo-checklist"),
  );
  rerender(<MessageList items={[summary, wake]} todoPlan={todo} />);
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
  expect(screen.queryByTestId("live-line-slot")).toBeNull();
  rerender(<MessageList items={[summary, wake]} runningTasks={1} />);
  expect(screen.getByTestId("generation-summary")).toHaveTextContent(
    "1 running task",
  );
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("a completed frame of the current turn stays hidden even if tool rows follow", () => {
  render(
    <MessageList
      items={[summary, tool]}
      generating
      turnProgress={{
        startedAtMs: 1,
        serverStartedAtMs: Date.parse("2026-10-30T10:00:00.123Z"),
        outputTokens: 100,
        estimated: false,
      }}
    />,
  );
  expect(screen.queryByTestId("generation-summary")).toBeNull();
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("a previous summary remains when visible work separates it from a new turn", () => {
  render(
    <MessageList
      items={[summary, wake, tool]}
      generating
      turnProgress={{
        startedAtMs: 1,
        serverStartedAtMs: Date.parse("2026-10-30T10:01:00Z"),
        outputTokens: 0,
        estimated: false,
      }}
    />,
  );
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("a previous summary stays after a visible message even without server turn identity", () => {
  render(
    <MessageList
      items={[
        summary,
        wake,
        { id: "answer", type: "assistant_message", content: "Continuing" },
      ]}
      generating
    />,
  );
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
  expect(screen.getByText("Continuing")).toBeInTheDocument();
});
