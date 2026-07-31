import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { BackgroundTasksChip } from "./BackgroundTasksChip";
import type { BackgroundTask } from "./types";

afterEach(() => cleanup());

function task(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "s1",
    kind: "command",
    label: "make build",
    status: "running",
    started_at: "2026-07-29T12:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
}

const done = (id: string) =>
  task({ id, running: false, status: "succeeded", exit_code: 0 });

test("a chat that never ran anything shows no opener", () => {
  render(<BackgroundTasksChip tasks={[]} onOpen={() => {}} />);
  expect(screen.queryByTestId("bgtask-chip")).toBeNull();
});

test("running work is counted and singular reads correctly", () => {
  const { rerender } = render(
    <BackgroundTasksChip tasks={[task()]} onOpen={() => {}} />,
  );
  expect(screen.getByTestId("bgtask-chip")).toHaveTextContent("1 running task");

  rerender(
    <BackgroundTasksChip
      tasks={[task(), task({ id: "bg_2" })]}
      onOpen={() => {}}
    />,
  );
  expect(screen.getByTestId("bgtask-chip")).toHaveTextContent("2 running tasks");
});

test("with nothing running the opener still reaches the history", () => {
  render(
    <BackgroundTasksChip tasks={[done("bg_1"), done("bg_2")]} onOpen={() => {}} />,
  );
  const chip = screen.getByTestId("bgtask-chip");
  expect(chip).toHaveTextContent("2 background tasks");
  expect(chip.className).not.toContain("is-running");
});

test("a live chat marks the chip so it reads as active", () => {
  render(
    <BackgroundTasksChip tasks={[task(), done("bg_2")]} onOpen={() => {}} />,
  );
  const chip = screen.getByTestId("bgtask-chip");
  expect(chip).toHaveTextContent("1 running task");
  expect(chip.className).toContain("is-running");
});

test("clicking opens the panel", () => {
  const onOpen = vi.fn();
  render(<BackgroundTasksChip tasks={[task()]} onOpen={onOpen} />);
  fireEvent.click(screen.getByTestId("bgtask-chip"));
  expect(onOpen).toHaveBeenCalledTimes(1);
});
