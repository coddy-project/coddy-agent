import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { BackgroundTasksDrawer } from "./BackgroundTasksDrawer";
import type { BackgroundTask } from "./types";

afterEach(() => cleanup());

const START_MS = Date.parse("2026-07-29T12:00:00Z");

function task(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "s1",
    kind: "command",
    label: "make build",
    command: "make build TAGS=http",
    status: "running",
    started_at: new Date(START_MS).toISOString(),
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
}

function renderDrawer(props: Partial<React.ComponentProps<typeof BackgroundTasksDrawer>> = {}) {
  const merged = {
    open: true,
    selectedTaskId: null,
    tasks: [task()],
    selectedOutput: "",
    listError: null,
    loading: false,
    nowMs: START_MS + 30_000,
    hasSession: true,
    onClose: () => {},
    onOpenTask: () => {},
    onBackToList: () => {},
    onStopTask: () => {},
    ...props,
  };
  return render(<BackgroundTasksDrawer {...merged} />);
}

test("closed drawer renders nothing", () => {
  renderDrawer({ open: false });
  expect(screen.queryByTestId("bgtasks-drawer")).toBeNull();
});

test("a running task shows its label, status and timing", () => {
  renderDrawer({ tasks: [task({ expected_seconds: 120 })] });
  const row = screen.getByTestId("bgtask-row-bg_1");
  expect(row).toHaveTextContent("make build");
  expect(row).toHaveTextContent("Running");
  expect(row).toHaveTextContent("30s");
  expect(row).toHaveTextContent("est. 2m");
});

test("the progress bar appears only when the model gave an estimate", () => {
  const { rerender } = renderDrawer({ tasks: [task()] });
  expect(screen.queryByRole("progressbar")).toBeNull();

  rerender(
    <BackgroundTasksDrawer
      open
      selectedTaskId={null}
      tasks={[task({ expected_seconds: 120 })]}
      selectedOutput=""
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      hasSession
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
    />,
  );
  expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
});

test("only a running task offers Stop", () => {
  const onStopTask = vi.fn();
  const { rerender } = renderDrawer({ onStopTask });
  fireEvent.click(screen.getByTestId("bgtask-stop-bg_1"));
  expect(onStopTask).toHaveBeenCalledWith("bg_1");

  rerender(
    <BackgroundTasksDrawer
      open
      selectedTaskId={null}
      tasks={[task({ running: false, status: "succeeded", exit_code: 0 })]}
      selectedOutput=""
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      hasSession
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
    />,
  );
  expect(screen.queryByTestId("bgtask-stop-bg_1")).toBeNull();
});

test("a row links to its own hash so middle-click opens a new tab", () => {
  renderDrawer();
  const link = screen.getByTestId("bgtask-row-bg_1").querySelector("a");
  expect(link).toHaveAttribute("href", "#/tasks/bg_1");
});

test("selecting a task shows its command and captured output", () => {
  renderDrawer({
    selectedTaskId: "bg_1",
    selectedOutput: "compiling package…",
  });
  expect(screen.getByTestId("bgtask-detail")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "compiling package…",
  );
  expect(screen.getByText("make build TAGS=http")).toBeInTheDocument();
});

test("a task with no output yet says so instead of showing an empty box", () => {
  renderDrawer({ selectedTaskId: "bg_1", selectedOutput: "   " });
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "(no output yet)",
  );
});

test("truncated output is flagged in the detail pane", () => {
  renderDrawer({
    selectedTaskId: "bg_1",
    selectedOutput: "tail",
    tasks: [task({ output_truncated: true })],
  });
  expect(screen.getByText("truncated")).toBeInTheDocument();
});

test("a failed task surfaces its error text", () => {
  renderDrawer({
    selectedTaskId: "bg_1",
    tasks: [
      task({
        running: false,
        status: "failed",
        error: "no such shell",
        exit_code: 127,
      }),
    ],
  });
  expect(screen.getByText("no such shell")).toBeInTheDocument();
});

test("empty states distinguish no session from no tasks", () => {
  const { rerender } = renderDrawer({ tasks: [], hasSession: false });
  expect(screen.getByTestId("bgtasks-no-session")).toBeInTheDocument();

  rerender(
    <BackgroundTasksDrawer
      open
      selectedTaskId={null}
      tasks={[]}
      selectedOutput=""
      listError={null}
      loading={false}
      nowMs={START_MS}
      hasSession
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
    />,
  );
  expect(screen.getByTestId("bgtasks-list-empty")).toBeInTheDocument();
});

test("a list error replaces the rows", () => {
  renderDrawer({ tasks: [], listError: "HTTP 500" });
  expect(screen.getByTestId("bgtasks-list-error")).toHaveTextContent("HTTP 500");
});
