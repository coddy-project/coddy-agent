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

/** A subagent run: no command, the label the pool writes, the child session. */
function agentTask(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_7",
    session_id: "s1",
    kind: "agent",
    label: "agent explore: survey the repo",
    agent: { name: "explore", session_id: "sess_0a1b2c" },
    status: "running",
    started_at: new Date(START_MS).toISOString(),
    timeout_seconds: 1800,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
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
    onOpenSession: () => {},
    ...over,
  };
  return render(<BackgroundTasksPanel {...props} />);
}

test("a closed panel renders nothing", () => {
  renderPanel({ open: false });
  expect(screen.queryByTestId("bgtasks-panel")).toBeNull();
});

// One card for every task: a shell command, a subagent, the memory run, running or
// finished. A click expands it in place - there is no second pane.

test("running tasks stand above the counter, finished ones behind it, all as the same card", () => {
  renderPanel({ tasks: [task(), done("bg_2"), done("bg_3")] });

  expect(screen.getByTestId("bgtask-card-bg_1")).toBeInTheDocument();
  // Anything above the finished counter is running, so the live cards carry no
  // heading of their own.
  expect(screen.queryByTestId("bgtask-section-running")).toBeNull();

  // History is counted, not listed: that is what keeps the panel cheap when a
  // session has hundreds of finished tasks.
  expect(screen.getByTestId("bgtask-finished-toggle")).toHaveTextContent(
    "Finished 2",
  );
  expect(screen.queryByTestId("bgtask-finished-list")).toBeNull();

  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  const live = screen.getByTestId("bgtask-card-bg_1");
  const past = screen.getByTestId("bgtask-card-bg_2");
  expect(past.className).toContain("bgtask-card");
  // Same parts in the same order: the opener with dot, tag and title, then the meta line.
  const shape = (card: HTMLElement) =>
    [...card.querySelectorAll("[data-part]")].map((el) =>
      el.getAttribute("data-part"),
    );
  expect(shape(past)).toEqual(shape(live));
  expect(shape(live)).toEqual(["dot", "tag", "title", "meta"]);
});

test("expanding the counter reveals the history, newest first", () => {
  renderPanel({
    tasks: [
      done("bg_old", { started_at: new Date(START_MS - 60_000).toISOString() }),
      done("bg_new", { started_at: new Date(START_MS).toISOString() }),
    ],
  });

  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  const rows = screen.getAllByTestId(/^bgtask-card-bg_/);
  expect(rows.map((r) => r.getAttribute("data-testid"))).toEqual([
    "bgtask-card-bg_new",
    "bgtask-card-bg_old",
  ]);
});

test("a finished card says how the task ended and when", () => {
  renderPanel({
    tasks: [done("bg_2"), done("bg_3", { status: "failed", exit_code: 2 })],
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  expect(screen.getByTestId("bgtask-meta-bg_2")).toHaveTextContent(
    /^Succeeded · 30s · \d{2}:\d{2}/,
  );
  expect(screen.getByTestId("bgtask-meta-bg_3")).toHaveTextContent(
    /^Failed · 30s/,
  );
});

test("only a running task offers Stop, and Stop is not part of the card's own control", () => {
  const onStopTask = vi.fn();
  const onOpenTask = vi.fn();
  renderPanel({ tasks: [task(), done("bg_2")], onStopTask, onOpenTask });

  const stop = screen.getByTestId("bgtask-stop-bg_1");
  expect(screen.getByTestId("bgtask-open-bg_1").contains(stop)).toBe(false);
  fireEvent.click(stop);
  expect(onStopTask).toHaveBeenCalledWith("bg_1");
  expect(onOpenTask).not.toHaveBeenCalled();

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
      onOpenSession={() => {}}
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
      onOpenSession={() => {}}
    />,
  );
  expect(screen.getByRole("progressbar")).toHaveAttribute(
    "aria-valuenow",
    "25",
  );
});

test("a card names what runs in a tag on the left and the work in its title", () => {
  renderPanel({
    tasks: [
      task(),
      agentTask(),
      agentTask({
        id: "bg_9",
        label: "memory: what did we decide",
        agent: { name: "memory", session_id: "sess_mem", system: true },
      }),
      agentTask({
        id: "bg_10",
        label: "agent general",
        agent: { name: "general", session_id: "sess_general" },
      }),
    ],
  });

  // A shell command: the tag says shell, the title is the command.
  expect(screen.getByTestId("bgtask-tag-bg_1")).toHaveTextContent("shell");
  expect(screen.getByTestId("bgtask-title-bg_1")).toHaveTextContent(
    "make build",
  );
  // A subagent: its name is the tag, and the title does not repeat it.
  expect(screen.getByTestId("bgtask-tag-bg_7")).toHaveTextContent("explore");
  expect(screen.getByTestId("bgtask-title-bg_7").textContent).toBe(
    "survey the repo",
  );
  // The memory run of a turn.
  expect(screen.getByTestId("bgtask-tag-bg_9")).toHaveTextContent("memory");
  expect(screen.getByTestId("bgtask-title-bg_9").textContent).toBe(
    "what did we decide",
  );
  // A run with no description still has a title.
  expect(screen.getByTestId("bgtask-title-bg_10").textContent).toBe(
    "Subagent run",
  );

  // The tag stands before the title.
  const opener = screen.getByTestId("bgtask-open-bg_7");
  const parts = [...opener.querySelectorAll("[data-part]")].map((el) =>
    el.getAttribute("data-part"),
  );
  expect(parts.indexOf("tag")).toBeLessThan(parts.indexOf("title"));
});

test("the card is one control: a click expands it in place, another folds it", () => {
  const onOpenTask = vi.fn();
  const onBackToList = vi.fn();
  const { rerender } = renderPanel({ onOpenTask, onBackToList });

  const opener = screen.getByTestId("bgtask-open-bg_1");
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByTestId("bgtask-body-bg_1")).toBeNull();
  fireEvent.click(opener);
  expect(onOpenTask).toHaveBeenCalledWith("bg_1");

  rerender(
    <BackgroundTasksPanel
      open
      selectedTaskId="bg_1"
      tasks={[task()]}
      selectedOutput="compiling package…"
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      onClose={() => {}}
      onOpenTask={onOpenTask}
      onBackToList={onBackToList}
      onStopTask={() => {}}
      onClearFinished={() => {}}
      onOpenSession={() => {}}
    />,
  );
  expect(
    screen.getByTestId("bgtask-open-bg_1").getAttribute("aria-expanded"),
  ).toBe("true");
  expect(screen.getByTestId("bgtask-body-bg_1")).toBeInTheDocument();
  // The list stays: there is no second pane and nothing to go back from.
  expect(screen.queryByTestId("bgtask-detail")).toBeNull();
  expect(screen.queryByTestId("bgtask-back")).toBeNull();
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  expect(onBackToList).toHaveBeenCalledTimes(1);
});

test("an expanded command card shows the command with a copy control, the output and how it ended", () => {
  renderPanel({
    selectedTaskId: "bg_2",
    tasks: [task(), done("bg_2", { elapsed_seconds: 95 })],
    selectedOutput: "ok  pkg/a 0.4s\nok  pkg/b 1.2s",
  });

  // A finished task that is open brings its section with it.
  expect(screen.getByTestId("bgtask-finished-list")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-command-bg_2")).toHaveTextContent(
    "make build TAGS=http",
  );
  expect(screen.getByTestId("bgtask-copy-command-bg_2")).toBeEnabled();
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "ok pkg/b 1.2s",
  );
  const foot = screen.getByTestId("bgtask-foot-bg_2");
  expect(foot).toHaveTextContent("Exit code 0");
  expect(foot).toHaveTextContent("1m35s");
  // Only one card is open at a time.
  expect(screen.queryByTestId("bgtask-body-bg_1")).toBeNull();
});

test("a failed run reads its error and its exit code in the card", () => {
  renderPanel({
    selectedTaskId: "bg_1",
    tasks: [
      task({
        running: false,
        status: "failed",
        exit_code: 2,
        elapsed_seconds: 90,
        expected_seconds: 45,
        finished_at: new Date(START_MS + 90_000).toISOString(),
        error: "make: *** [site-docs-check] Error 2",
      }),
    ],
  });

  expect(screen.getByTestId("bgtask-meta-bg_1")).toHaveTextContent(/^Failed/);
  expect(
    screen.getByText("make: *** [site-docs-check] Error 2"),
  ).toBeInTheDocument();
  const foot = screen.getByTestId("bgtask-foot-bg_1");
  expect(foot).toHaveTextContent("Exit code 2");
  expect(foot).toHaveTextContent("1m30s");
});

test("a running card has no ending to report yet", () => {
  renderPanel({ selectedTaskId: "bg_1", selectedOutput: "compiling…" });
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent("compiling…");
  expect(screen.queryByTestId("bgtask-foot-bg_1")).toBeNull();
});

test("a task with no output yet says so", () => {
  renderPanel({ selectedTaskId: "bg_1", selectedOutput: "   " });
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "(no output yet)",
  );
});

test("marks truncated output in the card", () => {
  renderPanel({
    selectedTaskId: "bg_1",
    tasks: [task({ output_truncated: true })],
  });

  expect(screen.getByText("truncated")).toBeInTheDocument();
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
      onOpenSession={() => {}}
    />,
  );
  expect(screen.getByTestId("bgtasks-list-error")).toHaveTextContent(
    "HTTP 500",
  );
  expect(screen.queryByTestId("bgtasks-list-empty")).toBeNull();
});

test("an expanded subagent card opens the child transcript and shows the run's log, not a command", () => {
  const onOpenSession = vi.fn();
  renderPanel({
    selectedTaskId: "bg_7",
    tasks: [
      agentTask({
        running: false,
        status: "succeeded",
        exit_code: 0,
        elapsed_seconds: 200,
        finished_at: new Date(START_MS + 200_000).toISOString(),
      }),
    ],
    selectedOutput: "→ read\n=== subagent report ===\nstatus: succeeded",
    onOpenSession,
  });

  // No shell stands behind an agent run: no command block, no exit code.
  expect(screen.queryByTestId("bgtask-command-bg_7")).toBeNull();
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "=== subagent report ===",
  );
  const foot = screen.getByTestId("bgtask-foot-bg_7");
  expect(foot).not.toHaveTextContent("Exit code");
  expect(foot).toHaveTextContent("3m20s");

  const transcript = screen.getByTestId("bgtask-open-transcript");
  expect(transcript).toHaveTextContent("Show transcript");
  fireEvent.click(transcript);
  expect(onOpenSession).toHaveBeenCalledWith("sess_0a1b2c");
});

test("Show transcript stays disabled until the child session is known", () => {
  const onOpenSession = vi.fn();
  renderPanel({
    selectedTaskId: "bg_7",
    tasks: [agentTask({ agent: { name: "explore" } })],
    onOpenSession,
  });

  const button = screen.getByTestId("bgtask-open-transcript");
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(onOpenSession).not.toHaveBeenCalled();
});
