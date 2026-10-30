import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";

afterEach(cleanup);

const answer: TranscriptItem = {
  id: "a1",
  type: "assistant_message",
  content: "Done.",
};
const finished: TranscriptItem = {
  id: "generation_1",
  type: "system_notice",
  level: "notice",
  message: "Generation complete",
  generation: { outputTokens: 289, tokensPerSecond: 111, elapsedMs: 4000 },
  createdAtUtc: "2026-10-30T10:05:00Z",
};

test("a finished turn with tasks still running says both on one line", () => {
  const onOpenTasks = vi.fn();
  render(
    <MessageList
      items={[answer, finished]}
      runningTasks={1}
      onOpenTasks={onOpenTasks}
    />,
  );
  const row = screen.getByTestId("generation-summary");
  // No second line at the tail, and the mark turns while the work goes on.
  expect(screen.queryByTestId("typing-dots")).toBeNull();
  expect(row.querySelector(".typing-dots--finished")).toBeNull();
  expect(
    [...row.querySelectorAll(".typing-dots-turn-item")].map(
      (el) => el.textContent,
    ),
  ).toEqual(["4s", "289 tokens · 111 tok/s", "1 running task"]);
  fireEvent.click(screen.getByTestId("typing-dots-turn-tasks"));
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("once the tasks end the line settles on the finished turn", () => {
  render(<MessageList items={[answer, finished]} runningTasks={0} />);
  const row = screen.getByTestId("generation-summary");
  expect(row.querySelector(".typing-dots--finished")).not.toBeNull();
  expect(screen.queryByTestId("typing-dots-turn-tasks")).toBeNull();
});

test("tasks after anything but a finished turn keep the tail's own line", () => {
  render(<MessageList items={[answer]} runningTasks={2} />);
  expect(screen.getByTestId("typing-dots")).toHaveTextContent(
    "2 running tasks",
  );
});

test("the finished line takes the live line's place, with no empty room under it", () => {
  render(<MessageList items={[answer, finished]} />);
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
  expect(screen.queryByTestId("live-line-slot")).toBeNull();
});

test("the live line stands until the turn is released, then simply stops", () => {
  const { rerender } = render(
    <MessageList items={[answer, finished]} generating={true} />,
  );
  // The completed frame comes a moment before the turn lets go: never both lines.
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
  expect(screen.queryByTestId("generation-summary")).toBeNull();

  rerender(<MessageList items={[answer, finished]} generating={false} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
});

test("a chat ending in anything else keeps the live line's empty room", () => {
  render(<MessageList items={[answer]} />);
  expect(screen.getByTestId("live-line-slot")).toBeInTheDocument();
});

test("a finished line in the middle of a chat stays while a later turn runs", () => {
  const next: TranscriptItem = {
    id: "u2",
    type: "user_message",
    content: "More",
  };
  render(<MessageList items={[answer, finished, next]} generating={true} />);
  expect(screen.getByTestId("generation-summary")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});
