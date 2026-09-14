import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BackgroundTasksPanel } from "./BackgroundTasksPanel";
import { stripSubagentTitlePrefix } from "./SubagentPermissionCard";
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
    onOpenSession: () => {},
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

test("marks truncated output in the task detail", () => {
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

test("agent tasks carry an agent badge in both sections", () => {
  renderPanel({
    tasks: [
      agentTask(),
      task(),
      done("bg_2"),
      agentTask({
        id: "bg_8",
        running: false,
        status: "succeeded",
        finished_at: new Date(START_MS + 30_000).toISOString(),
        elapsed_seconds: 30,
      }),
    ],
  });

  expect(screen.getByTestId("bgtask-agent-badge-bg_7")).toHaveTextContent(
    "agent",
  );
  expect(screen.queryByTestId("bgtask-agent-badge-bg_1")).toBeNull();

  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  expect(screen.getByTestId("bgtask-agent-badge-bg_8")).toBeInTheDocument();
  expect(screen.queryByTestId("bgtask-agent-badge-bg_2")).toBeNull();
});

test("an agent task's detail names the subagent and opens its transcript", () => {
  const onOpenSession = vi.fn();
  const { container } = renderPanel({
    selectedTaskId: "bg_7",
    tasks: [agentTask()],
    selectedOutput: "→ read\n=== subagent report ===\nstatus: succeeded",
    onOpenSession,
  });

  expect(screen.getByTestId("bgtask-detail-agent-name")).toHaveTextContent(
    "explore",
  );
  // The role name stands where a shell command would.
  expect(container.querySelector(".bgtask-detail-command")).toBeNull();
  // The output pane is the child's live log, report block included.
  expect(screen.getByTestId("bgtask-output")).toHaveTextContent(
    "=== subagent report ===",
  );

  fireEvent.click(screen.getByTestId("bgtask-open-transcript"));
  expect(onOpenSession).toHaveBeenCalledWith("sess_0a1b2c");
});

test("Open transcript stays disabled until the child session is known", () => {
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

function awaitingTask(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return agentTask({
    pending_permission: {
      sessionId: "sess_0a1b2c",
      agent_name: "explore",
      asked_at: new Date(START_MS + 10_000).toISOString(),
      toolCall: {
        toolCallId: "call_9",
        title: "[subagent explore] Run: run_command",
        kind: "execute",
        content: [
          { type: "content", content: { type: "text", text: "npm test" } },
        ],
      },
      options: [
        { optionId: "allow", name: "Allow once", kind: "allow_once" },
        { optionId: "reject", name: "Reject", kind: "reject_once" },
      ],
    },
    ...over,
  });
}

// The parent turn that spawned a detached run has ended, so its prompt has no
// chat stream to appear in: the task that is blocked has to ask.
test("a detached subagent's prompt is answered on its task card", async () => {
  const calls: Array<{ url: string; body?: string; headers?: HeadersInit }> =
    [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      calls.push({
        url: String(url),
        ...(typeof init?.body === "string" ? { body: init.body } : {}),
        ...(init?.headers ? { headers: init.headers } : {}),
      });
      return Promise.resolve({ ok: true, json: async () => ({}) });
    }),
  );
  const onRefresh = vi.fn();
  renderPanel({ tasks: [awaitingTask()], onRefresh });

  expect(screen.getByTestId("bgtask-card-bg_7").className).toContain(
    "is-awaiting",
  );
  const card = screen.getByTestId("bgtask-permission-bg_7");
  expect(card).toHaveTextContent(
    'The subagent "explore" is waiting for your approval:',
  );
  expect(card).toHaveTextContent("npm test");
  // The standing grants never reach a detached prompt, and the card offers
  // exactly what the row carries.
  expect(screen.getByTestId("bgtask-permission-allow-bg_7")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-permission-reject-bg_7")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("bgtask-permission-allow-bg_7"));
  await waitFor(() => expect(onRefresh).toHaveBeenCalled());
  // Answered against the child session - the one actually waiting - not the
  // parent chat the task belongs to.
  expect(calls).toHaveLength(1);
  expect(calls[0]?.url).toBe("/coddy/sessions/sess_0a1b2c/permission");
  expect(calls[0]?.body).toBe(
    JSON.stringify({ toolCallId: "call_9", optionId: "allow" }),
  );
  vi.unstubAllGlobals();
});

test("the detail pane of a waiting task carries the same card", () => {
  renderPanel({ tasks: [awaitingTask()], selectedTaskId: "bg_7" });
  expect(screen.getByTestId("bgtask-detail")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-permission-bg_7")).toBeInTheDocument();
});

test("a running task with no prompt carries no permission card", () => {
  renderPanel({ tasks: [agentTask()] });
  expect(screen.queryByTestId("bgtask-permission-bg_7")).toBeNull();
  expect(screen.getByTestId("bgtask-card-bg_7").className).not.toContain(
    "is-awaiting",
  );
});

// The relay prefixes a forwarded title so the parent chat can tell whose
// prompt it is; on a task row the card already says that, and the prefix would
// leave the tool preview's header blank.
test("the relay's subagent prefix is dropped from the previewed title", () => {
  expect(stripSubagentTitlePrefix("[subagent explore] Run: run_command")).toBe(
    "Run: run_command",
  );
  expect(stripSubagentTitlePrefix("Run: run_command")).toBe("Run: run_command");
  expect(stripSubagentTitlePrefix(undefined)).toBe("");
});
