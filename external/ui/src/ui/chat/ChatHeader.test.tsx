import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ChatHeader } from "./ChatHeader";
import type { BackgroundTask } from "../tasks/types";

afterEach(() => cleanup());

test("edit mode shows full-width title input class", () => {
  render(<ChatHeader title="Hello" editable onTitleSave={() => {}} />);

  fireEvent.click(screen.getByRole("button", { name: /chat title/i }));

  const input = screen.getByRole("textbox");
  expect(input).toHaveClass("chat-title-input");
});

// The opener of the Tasks panel lives in the sticky header, so it does not scroll
// away with the transcript (docs/plans/turn-progress.md).

function task(over: Partial<BackgroundTask>): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "sess",
    kind: "command",
    label: "make test",
    command: "make test",
    status: "running",
    started_at: "2026-09-18T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 5,
    overdue: false,
    running: true,
    ...over,
  };
}

test("the tasks control is in the header of a chat that never ran a task, without counts", () => {
  const onOpenTasks = vi.fn();
  render(<ChatHeader title="Hello" tasks={[]} onOpenTasks={onOpenTasks} />);
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).not.toHaveClass("is-running");
  expect(control.querySelector(".chat-header-tasks-label")?.textContent).toBe(
    "Tasks",
  );
  expect(screen.queryByTestId("chat-header-tasks-counts")).toBeNull();
  expect(control.getAttribute("aria-label")).toBe("Background tasks: none yet");
  fireEvent.click(control);
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("with tasks the control says how many are running out of how many there are", () => {
  render(
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1" }),
        task({ id: "bg_2", status: "succeeded", running: false }),
        task({ id: "bg_3", status: "failed", running: false }),
        // The memory run of a turn is a system task: neither running nor total.
        task({
          id: "bg_4",
          kind: "agent",
          agent: { name: "memory", system: true },
        }),
      ]}
      onOpenTasks={() => {}}
    />,
  );
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).toHaveClass("is-running");
  expect(screen.getByTestId("chat-header-tasks-counts").textContent).toBe(
    "1 / 3",
  );
  expect(control.getAttribute("aria-label")).toBe(
    "Background tasks: 1 running, 3 in total",
  );
});

test("once everything has finished the control keeps the total and drops the live mark", () => {
  render(
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1", status: "succeeded", running: false }),
        task({ id: "bg_2", status: "failed", running: false }),
      ]}
      onOpenTasks={() => {}}
    />,
  );
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).not.toHaveClass("is-running");
  expect(screen.getByTestId("chat-header-tasks-counts").textContent).toBe(
    "0 / 2",
  );
});

test("the control says whether the panel it opens is showing", () => {
  const { rerender } = render(
    <ChatHeader title="Hello" tasks={[task({})]} onOpenTasks={() => {}} />,
  );
  expect(
    screen.getByTestId("chat-header-tasks").getAttribute("aria-expanded"),
  ).toBe("false");
  rerender(
    <ChatHeader
      title="Hello"
      tasks={[task({})]}
      onOpenTasks={() => {}}
      tasksOpen={true}
    />,
  );
  expect(
    screen.getByTestId("chat-header-tasks").getAttribute("aria-expanded"),
  ).toBe("true");
});

test("without a way to open the panel the header has no tasks control", () => {
  render(<ChatHeader title="Hello" tasks={[task({})]} />);
  expect(screen.queryByTestId("chat-header-tasks")).toBeNull();
});
