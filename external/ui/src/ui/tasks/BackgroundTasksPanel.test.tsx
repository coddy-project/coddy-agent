import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { BackgroundTasksPanel } from "./BackgroundTasksPanel";
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

function done(id: string, over: Partial<BackgroundTask> = {}): BackgroundTask {
  return task({
    id,
    running: false,
    status: "succeeded",
    exit_code: 0,
    finished_at: new Date(START_MS + 30_000).toISOString(),
    elapsed_seconds: 30,
    ...over,
  });
}

type Props = React.ComponentProps<typeof BackgroundTasksPanel>;

function renderPanel(over: Partial<Props> = {}) {
  const props: Props = {
    open: true,
    selectedTaskId: null,
    tasks: [task()],
    selectedOutput: "",
    listError: null,
    loading: false,
    nowMs: START_MS + 30_000,
    onClose: () => {},
    onOpenTask: () => {},
    onBackToList: () => {},
    onStopTask: () => {},
    onClearFinished: () => {},
    ...over,
  };
  return render(<BackgroundTasksPanel {...props} />);
}

test("a closed panel renders nothing", () => {
  renderPanel({ open: false });
  expect(screen.queryByTestId("bgtasks-panel")).toBeNull();
});

test("running tasks get a card, finished ones stay behind a counter", () => {
  renderPanel({ tasks: [task(), done("bg_2"), done("bg_3")] });

  expect(screen.getByTestId("bgtask-section-running")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-card-bg_1")).toBeInTheDocument();

  // History is counted, not listed: that is what keeps the panel cheap when a
  // session has hundreds of finished tasks.
  expect(screen.getByTestId("bgtask-finished-toggle")).toHaveTextContent(
    "Finished 2",
  );
  expect(screen.queryByTestId("bgtask-finished-list")).toBeNull();
});

test("expanding the counter reveals the history, newest first", () => {
  renderPanel({
    tasks: [
      done("bg_old", { started_at: new Date(START_MS - 60_000).toISOString() }),
      done("bg_new", { started_at: new Date(START_MS).toISOString() }),
    ],
  });

  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  const rows = screen.getAllByTestId(/^bgtask-finished-bg_/);
  expect(rows.map((r) => r.getAttribute("data-testid"))).toEqual([
    "bgtask-finished-bg_new",
    "bgtask-finished-bg_old",
  ]);
});

test("only a running task offers Stop", () => {
  const onStopTask = vi.fn();
  renderPanel({ tasks: [task(), done("bg_2")], onStopTask });

  fireEvent.click(screen.getByTestId("bgtask-stop-bg_1"));
  expect(onStopTask).toHaveBeenCalledWith("bg_1");

  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  expect(screen.queryByTestId("bgtask-stop-bg_2")).toBeNull();
});

test("Clear is offered only when there is history to clear", () => {
  const onClearFinished = vi.fn();
  const { rerender } = renderPanel({ tasks: [task()] });
  expect(screen.queryByTestId("bgtask-clear-finished")).toBeNull();

  rerender(
    <BackgroundTasksPanel
      open
      selectedTaskId={null}
      tasks={[task(), done("bg_2")]}
      selectedOutput=""
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
      onClearFinished={onClearFinished}
    />,
  );
  fireEvent.click(screen.getByTestId("bgtask-clear-finished"));
  expect(onClearFinished).toHaveBeenCalled();
});

test("the progress bar appears only when the model gave an estimate", () => {
  const { rerender } = renderPanel({ tasks: [task()] });
  expect(screen.queryByRole("progressbar")).toBeNull();

  rerender(
    <BackgroundTasksPanel
      open
      selectedTaskId={null}
      tasks={[task({ expected_seconds: 120 })]}
      selectedOutput=""
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
      onClearFinished={() => {}}
    />,
  );
  expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
});

test("selecting a task shows its command and captured output", () => {
  renderPanel({ selectedTaskId: "bg_1", selectedOutput: "compiling package…" });

  expect(screen.getByTestId("bgtask-detail")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "compiling package…",
  );
  expect(screen.getByText("make build TAGS=http")).toBeInTheDocument();
});

test("a task with no output yet says so", () => {
  renderPanel({ selectedTaskId: "bg_1", selectedOutput: "   " });
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "(no output yet)",
  );
});

test("empty and error states replace the sections", () => {
  const { rerender } = renderPanel({ tasks: [] });
  expect(screen.getByTestId("bgtasks-list-empty")).toBeInTheDocument();

  rerender(
    <BackgroundTasksPanel
      open
      selectedTaskId={null}
      tasks={[]}
      selectedOutput=""
      listError="HTTP 500"
      loading={false}
      nowMs={START_MS}
      onClose={() => {}}
      onOpenTask={() => {}}
      onBackToList={() => {}}
      onStopTask={() => {}}
      onClearFinished={() => {}}
    />,
  );
  expect(screen.getByTestId("bgtasks-list-error")).toHaveTextContent("HTTP 500");
  expect(screen.queryByTestId("bgtasks-list-empty")).toBeNull();
});
