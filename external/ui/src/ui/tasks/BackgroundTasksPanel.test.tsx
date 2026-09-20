import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
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

/** Outputs by task id, served the way the shell's loader serves them. */
function outputsOf(outputs: Record<string, string>) {
  return vi.fn(async (taskId: string) => outputs[taskId] ?? "");
}

function renderPanel(over: Partial<Props> = {}) {
  const props: Props = {
    open: true,
    tasks: [task()],
    listError: null,
    loading: false,
    nowMs: START_MS + 30_000,
    loadOutput: outputsOf({}),
    onClose: () => {},
    onStopTask: () => {},
    onClearFinished: () => {},
    onOpenSession: () => {},
    ...over,
  };
  return { ...render(<BackgroundTasksPanel {...props} />), props };
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

test("a folded finished card leaves how it ended to its dot, the open card names it at the foot", () => {
  renderPanel({
    tasks: [done("bg_2"), done("bg_3", { status: "failed", exit_code: 2 })],
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  // Folded: how long it ran and when it ended (the clock in the reader's own
  // format, 12:00 or 12:00 PM); the green or red dot says the rest.
  for (const id of ["bg_2", "bg_3"]) {
    const meta = screen.getByTestId(`bgtask-meta-${id}`);
    expect(meta).toHaveTextContent(/^30s · \d{1,2}:\d{2}/);
    expect(meta).not.toHaveTextContent(/Succeeded|Failed/);
  }
  expect(
    screen.getByTestId("bgtask-card-bg_3").querySelector(".bgtask-dot--danger"),
  ).not.toBeNull();

  fireEvent.click(screen.getByTestId("bgtask-open-bg_3"));
  const foot = screen.getByTestId("bgtask-foot-bg_3");
  expect(foot).toHaveTextContent(/^Failed · Exit code 2$/);
  // How long it ran is the folded row's, and it is not said twice: the foot
  // carries only what the folded card leaves to it.
  expect(foot).not.toHaveTextContent("30s");
});

test("only a running task offers Stop, and Stop is not part of the card's own control", () => {
  const onStopTask = vi.fn();
  renderPanel({ tasks: [task(), done("bg_2")], onStopTask });

  const stop = screen.getByTestId("bgtask-stop-bg_1");
  expect(screen.getByTestId("bgtask-open-bg_1").contains(stop)).toBe(false);
  fireEvent.click(stop);
  expect(onStopTask).toHaveBeenCalledWith("bg_1");
  expect(screen.queryByTestId("bgtask-body-bg_1")).toBeNull();

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
      tasks={[task(), done("bg_2")]}
      listError={null}
      loading={false}
      nowMs={START_MS + 30_000}
      loadOutput={outputsOf({})}
      onClose={() => {}}
      onStopTask={() => {}}
      onClearFinished={onClearFinished}
      onOpenSession={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("bgtask-clear-finished"));
  expect(onClearFinished).toHaveBeenCalled();
});

test("the progress bar appears only when the model gave an estimate", () => {
  const { rerender, props } = renderPanel({ tasks: [task()] });
  expect(screen.queryByRole("progressbar")).toBeNull();

  rerender(
    <BackgroundTasksPanel
      {...props}
      tasks={[task({ expected_seconds: 120 })]}
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

test("a folded subagent card names its model and the tokens it spent, a command card neither", () => {
  renderPanel({
    tasks: [
      task(),
      agentTask({
        agent: {
          name: "explore",
          session_id: "sess_0a1b2c",
          model: "neuraldeep/qwen3.8-27b",
          input_tokens: 198_000,
          output_tokens: 14_345,
        },
      }),
      done("bg_3", {
        kind: "agent",
        command: "",
        label: "agent general: review the diff",
        agent: {
          name: "general",
          session_id: "sess_general",
          model: "rpa/qwen3.6-35b-a3b",
          input_tokens: 1_000,
          output_tokens: 200,
        },
      }),
    ],
  });

  // Still folded: nothing to open for it.
  expect(screen.queryByTestId("bgtask-body-bg_7")).toBeNull();
  const usage = screen.getByTestId("bgtask-usage-bg_7");
  expect(usage).toHaveTextContent("qwen3.8-27b · 212k tokens");
  // The short name on the card; the full id and the exact split on hover. The whole
  // summary is the card's opener, so the hover text is the opener's.
  expect(screen.getByTestId("bgtask-model-bg_7")).toHaveTextContent(
    "qwen3.8-27b",
  );
  const hover = screen.getByTestId("bgtask-open-bg_7").getAttribute("title");
  expect(hover).toMatch(/neuraldeep\/qwen3\.8-27b/);
  expect(hover).toMatch(/198,000/);
  expect(hover).toMatch(/14,345/);
  // It is part of the meta line, which keeps saying how the run is going.
  expect(screen.getByTestId("bgtask-meta-bg_7")).toContainElement(usage);
  expect(screen.getByTestId("bgtask-meta-bg_7")).toHaveTextContent(/^30s/);

  // A shell command has no model behind it.
  expect(screen.queryByTestId("bgtask-usage-bg_1")).toBeNull();

  // A finished run keeps what it spent.
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  expect(screen.getByTestId("bgtask-usage-bg_3")).toHaveTextContent(
    "qwen3.6-35b-a3b · 1.2k tokens",
  );
});

test("a folded subagent card reaches the child transcript without being opened", () => {
  // A subagent run is the conversation it holds, so the way into that
  // conversation belongs on the card the operator already sees. Opening a card
  // to find a link, then leaving the panel through it, put a step in the middle
  // of the one move the panel exists for.
  const onOpenSession = vi.fn();
  renderPanel({ tasks: [task(), agentTask()], onOpenSession });

  const opener = screen.getByTestId("bgtask-open-bg_7");
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  const transcript = screen.getByTestId("bgtask-open-transcript-bg_7");
  expect(transcript).toHaveTextContent("Show transcript");
  expect(transcript).toHaveAccessibleName(
    "Show the transcript of survey the repo",
  );
  // It rides the meta line, under the status and the usage.
  expect(screen.getByTestId("bgtask-meta-bg_7")).toContainElement(transcript);

  // It stands above the card's stretched opener: it opens the transcript, not
  // the card - the same way Stop stops the task without expanding it.
  expect(opener.contains(transcript)).toBe(false);
  fireEvent.click(transcript);
  expect(onOpenSession).toHaveBeenCalledWith("sess_0a1b2c");
  expect(screen.queryByTestId("bgtask-body-bg_7")).toBeNull();
  expect(opener.getAttribute("aria-expanded")).toBe("false");

  // Opening the card does not give it a second one.
  fireEvent.click(opener);
  expect(screen.getAllByTestId("bgtask-open-transcript-bg_7")).toHaveLength(1);

  // A shell command has no child conversation behind it.
  expect(screen.queryByTestId("bgtask-open-transcript-bg_1")).toBeNull();
});

test("the card is one control: a click expands it in place, another folds it", async () => {
  const loadOutput = outputsOf({ bg_1: "compiling package…" });
  renderPanel({ loadOutput });

  const opener = screen.getByTestId("bgtask-open-bg_1");
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByTestId("bgtask-body-bg_1")).toBeNull();

  fireEvent.click(opener);
  expect(opener.getAttribute("aria-expanded")).toBe("true");
  expect(await screen.findByText("compiling package…")).toBeInTheDocument();
  expect(loadOutput).toHaveBeenCalledWith("bg_1");
  // The list stays: there is no second pane and nothing to go back from.
  expect(screen.queryByTestId("bgtask-detail")).toBeNull();
  expect(screen.queryByTestId("bgtask-back")).toBeNull();

  fireEvent.click(opener);
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByTestId("bgtask-body-bg_1")).toBeNull();
});

test("any number of cards stay open side by side, each with its own output", async () => {
  renderPanel({
    tasks: [task(), agentTask(), done("bg_2")],
    loadOutput: outputsOf({
      bg_1: "compiling package…",
      bg_7: "→ read",
      bg_2: "ok  pkg/a 0.4s",
    }),
  });

  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_7"));
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_2"));

  expect(await screen.findByText("compiling package…")).toBeInTheDocument();
  expect(await screen.findByText("→ read")).toBeInTheDocument();
  expect(await screen.findByText("ok pkg/a 0.4s")).toBeInTheDocument();
  for (const id of ["bg_1", "bg_7", "bg_2"]) {
    expect(screen.getByTestId(`bgtask-body-${id}`)).toBeInTheDocument();
  }

  // Folding one leaves the others as they were.
  fireEvent.click(screen.getByTestId("bgtask-open-bg_7"));
  expect(screen.queryByTestId("bgtask-body-bg_7")).toBeNull();
  expect(screen.getByTestId("bgtask-body-bg_1")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-body-bg_2")).toBeInTheDocument();
});

test("an expanded command card shows the command with a copy control, the output and how it ended", async () => {
  renderPanel({
    tasks: [task(), done("bg_2", { elapsed_seconds: 95 })],
    loadOutput: outputsOf({ bg_2: "ok  pkg/a 0.4s\nok  pkg/b 1.2s" }),
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_2"));

  expect(screen.getByTestId("bgtask-command-bg_2")).toHaveTextContent(
    "make build TAGS=http",
  );
  expect(screen.getByTestId("bgtask-copy-command-bg_2")).toBeEnabled();
  await waitFor(() =>
    expect(screen.getByTestId("bgtask-output-bg_2")).toHaveTextContent(
      "ok pkg/b 1.2s",
    ),
  );
  const foot = screen.getByTestId("bgtask-foot-bg_2");
  expect(foot).toHaveTextContent(/^Succeeded · Exit code 0$/);
});

test("a card the shell points at opens on its own, its section with it", async () => {
  // "Open in Tasks" on a transcript row, or an old link that named a task.
  const loadOutput = outputsOf({ bg_2: "ok  pkg/a 0.4s" });
  const { rerender, props } = renderPanel({
    tasks: [task(), done("bg_2")],
    loadOutput,
  });
  expect(screen.queryByTestId("bgtask-finished-list")).toBeNull();

  rerender(
    <BackgroundTasksPanel {...props} focus={{ taskId: "bg_2", seq: 1 }} />,
  );
  expect(screen.getByTestId("bgtask-finished-list")).toBeInTheDocument();
  expect(screen.getByTestId("bgtask-body-bg_2")).toBeInTheDocument();
  expect(await screen.findByText("ok pkg/a 0.4s")).toBeInTheDocument();

  // The reader folds it; the same pointer does not force it open again...
  fireEvent.click(screen.getByTestId("bgtask-open-bg_2"));
  rerender(
    <BackgroundTasksPanel {...props} focus={{ taskId: "bg_2", seq: 1 }} />,
  );
  expect(screen.queryByTestId("bgtask-body-bg_2")).toBeNull();
  // ...a new one does.
  rerender(
    <BackgroundTasksPanel {...props} focus={{ taskId: "bg_2", seq: 2 }} />,
  );
  expect(screen.getByTestId("bgtask-body-bg_2")).toBeInTheDocument();
});

test("a card the shell points at is shown even when the history is longer than what the list renders", async () => {
  // "Open in Tasks" on an early row of a long session: the task is far down the
  // finished list, past the cards the panel renders by default.
  const history = Array.from({ length: 45 }, (_, i) =>
    done(`bg_${i + 1}`, {
      started_at: new Date(START_MS + i * 1000).toISOString(),
    }),
  );
  renderPanel({
    tasks: history,
    loadOutput: outputsOf({ bg_1: "the oldest run" }),
    focus: { taskId: "bg_1", seq: 1 },
  });

  expect(screen.getByTestId("bgtask-body-bg_1")).toBeInTheDocument();
  expect(await screen.findByText("the oldest run")).toBeInTheDocument();
  // The rest of the history past the cap stays on disk, as before.
  expect(screen.queryByTestId("bgtask-card-bg_2")).toBeNull();
  expect(screen.getByTestId("bgtask-finished-more")).toBeInTheDocument();
});

test("the shell is told once the card it pointed at is open, so the pointer is spent", () => {
  // The panel unmounts with the drawer. A pointer the shell kept would open the same
  // card again on the next mount - or the card of another chat that has a task with
  // the same id, since every session numbers its tasks from bg_1.
  const onFocusHonoured = vi.fn();
  const { rerender, props } = renderPanel({
    tasks: [task(), done("bg_2")],
    focus: { taskId: "bg_2", seq: 7 },
    onFocusHonoured,
  });
  expect(onFocusHonoured).toHaveBeenCalledTimes(1);
  expect(onFocusHonoured).toHaveBeenCalledWith(7);

  rerender(<BackgroundTasksPanel {...props} focus={null} />);
  expect(onFocusHonoured).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("bgtask-body-bg_2")).toBeInTheDocument();
});

test("the output of an open running card is read again while it runs, and once more when it ends", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    let text = "step 1";
    const loadOutput = vi.fn(async () => text);
    const { rerender, props } = renderPanel({ loadOutput });
    fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
    expect(await screen.findByText("step 1")).toBeInTheDocument();

    text = "step 1\nstep 2";
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2600);
    });
    expect(await screen.findByText(/step 2/)).toBeInTheDocument();

    // The task ends between two polls: the card reads its final output once more.
    text = "step 1\nstep 2\ndone";
    const calls = loadOutput.mock.calls.length;
    rerender(
      <BackgroundTasksPanel
        {...props}
        loadOutput={loadOutput}
        tasks={[done("bg_1")]}
      />,
    );
    expect(await screen.findByText(/done/)).toBeInTheDocument();
    expect(loadOutput.mock.calls.length).toBeGreaterThan(calls);

    // A finished card is not polled.
    const settled = loadOutput.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });
    expect(loadOutput.mock.calls.length).toBe(settled);
  } finally {
    vi.useRealTimers();
  }
});

test("a read that answers late does not put older output over a newer read", async () => {
  // Two reads of one card are in flight when its task ends - the poll and the final
  // read - and the network answers them in the wrong order. Nothing reads a finished
  // card again, so the stale answer would stay on screen for good.
  const answers: Array<(text: string) => void> = [];
  const loadOutput = vi.fn(
    () => new Promise<string | null>((resolve) => answers.push(resolve)),
  );
  renderPanel({ loadOutput, tasks: [done("bg_1")] });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));

  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  expect(answers).toHaveLength(2);

  await act(async () => {
    answers[1]?.("step 1\nstep 2\ndone");
  });
  expect(await screen.findByText(/done/)).toBeInTheDocument();
  await act(async () => {
    answers[0]?.("step 1");
  });
  expect(screen.getByTestId("bgtask-output-bg_1").textContent).toContain(
    "done",
  );
});

test("stopping an open card reads what the task printed last", async () => {
  let text = "running…";
  const loadOutput = vi.fn(async () => text);
  const onStopTask = vi.fn(async () => {
    text = "running…\nterminated";
  });
  renderPanel({ loadOutput, onStopTask });
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  expect(await screen.findByText("running…")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("bgtask-stop-bg_1"));
  expect(await screen.findByText(/terminated/)).toBeInTheDocument();
});

test("a failed run reads its error and its exit code in the card", async () => {
  renderPanel({
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
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));

  expect(screen.getByTestId("bgtask-meta-bg_1")).not.toHaveTextContent(
    "Failed",
  );
  expect(
    screen.getByText("make: *** [site-docs-check] Error 2"),
  ).toBeInTheDocument();
  const foot = screen.getByTestId("bgtask-foot-bg_1");
  expect(foot).toHaveTextContent(/^Failed · Exit code 2$/);
});

test("a failed command says its exit code once, at the foot", () => {
  // The pool records a command's non-zero exit as the error "exit status 2"; above
  // a foot that says "Exit code 2" it only repeats it.
  renderPanel({
    tasks: [
      done("bg_1", { status: "failed", exit_code: 2, error: "exit status 2" }),
    ],
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  const card = screen.getByTestId("bgtask-card-bg_1");
  expect(card).not.toHaveTextContent("exit status 2");
  expect(card.querySelector(".bgtask-card-error")).toBeNull();
  expect(screen.getByTestId("bgtask-foot-bg_1")).toHaveTextContent(
    "Exit code 2",
  );
});

test("a running card has no ending to report yet", async () => {
  renderPanel({ loadOutput: outputsOf({ bg_1: "compiling…" }) });
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  expect(await screen.findByText("compiling…")).toBeInTheDocument();
  expect(screen.queryByTestId("bgtask-foot-bg_1")).toBeNull();
});

test("a task with no output yet says so", async () => {
  renderPanel({ loadOutput: outputsOf({ bg_1: "   " }) });
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  await waitFor(() =>
    expect(screen.getByTestId("bgtask-output-bg_1")).toHaveTextContent(
      "(no output yet)",
    ),
  );
});

test("marks truncated output in the card", () => {
  renderPanel({ tasks: [task({ output_truncated: true })] });
  fireEvent.click(screen.getByTestId("bgtask-open-bg_1"));
  expect(screen.getByText("truncated")).toBeInTheDocument();
});

test("empty and error states replace the sections", () => {
  const { rerender } = renderPanel({ tasks: [] });
  expect(screen.getByTestId("bgtasks-list-empty")).toBeInTheDocument();

  rerender(
    <BackgroundTasksPanel
      open
      tasks={[]}
      listError="HTTP 500"
      loading={false}
      nowMs={START_MS}
      loadOutput={outputsOf({})}
      onClose={() => {}}
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

test("an expanded subagent card opens the child transcript and shows the run's log, not a command", async () => {
  const onOpenSession = vi.fn();
  renderPanel({
    tasks: [
      agentTask({
        running: false,
        status: "succeeded",
        exit_code: 0,
        elapsed_seconds: 200,
        finished_at: new Date(START_MS + 200_000).toISOString(),
      }),
    ],
    loadOutput: outputsOf({
      bg_7: "→ read\n=== subagent report ===\nstatus: succeeded",
    }),
    onOpenSession,
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  fireEvent.click(screen.getByTestId("bgtask-open-bg_7"));

  // No shell stands behind an agent run: no command block, no exit code.
  expect(screen.queryByTestId("bgtask-command-bg_7")).toBeNull();
  await waitFor(() =>
    expect(screen.getByTestId("bgtask-output-bg_7")).toHaveTextContent(
      "=== subagent report ===",
    ),
  );
  const foot = screen.getByTestId("bgtask-foot-bg_7");
  expect(foot).not.toHaveTextContent("Exit code");
  expect(foot).toHaveTextContent(/^Succeeded$/);

  // The way to the transcript is the folded card's and is not repeated here.
  const transcript = screen.getByTestId("bgtask-open-transcript-bg_7");
  expect(screen.getByTestId("bgtask-body-bg_7").contains(transcript)).toBe(
    false,
  );
  expect(onOpenSession).not.toHaveBeenCalled();
});

test("Show transcript stays disabled until the child session is known", () => {
  const onOpenSession = vi.fn();
  renderPanel({
    tasks: [agentTask({ agent: { name: "explore" } })],
    onOpenSession,
  });

  const button = screen.getByTestId("bgtask-open-transcript-bg_7");
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(onOpenSession).not.toHaveBeenCalled();
});

// A running task that will wake the agent when it ends says so with a bell
// after its title; one that will not carries none.
test("a running task that wakes the agent carries a bell after its title", () => {
  renderPanel({
    tasks: [
      task({ id: "bg_1", label: "make test", notify_on_finish: true }),
      task({ id: "bg_2", label: "make lint" }),
    ],
  });
  const bell = screen.getByTestId("bgtask-notify-bg_1");
  expect(bell).toHaveAttribute("title", "Wakes the agent when it ends");
  expect(bell).toHaveAccessibleName("Wakes the agent when it ends");
  // The bell sits after the title, inside the card's one control.
  const opener = screen.getByTestId("bgtask-open-bg_1");
  expect(opener.lastElementChild).toBe(bell);
  expect(screen.queryByTestId("bgtask-notify-bg_2")).toBeNull();
});

// The turn a finished task started shows nothing in the transcript, so its card
// keeps the bell and says it woke the agent. A task that was to wake it and has
// not - the turn has not begun - carries none once it has ended.
test("a finished task that woke the agent keeps its bell", () => {
  renderPanel({
    tasks: [
      done("bg_3", { label: "make test", notify_on_finish: true, woke_agent: true }),
      done("bg_4", { label: "make build", notify_on_finish: true }),
    ],
  });
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  const bell = screen.getByTestId("bgtask-notify-bg_3");
  expect(bell).toHaveAttribute("title", "Woke the agent when it ended");
  expect(bell).toHaveAccessibleName("Woke the agent when it ended");
  expect(screen.getByTestId("bgtask-open-bg_3").lastElementChild).toBe(bell);
  expect(screen.queryByTestId("bgtask-notify-bg_4")).toBeNull();
});

// A preview server (preview_server): the card is the way to the page.

function serverTask(over: Partial<BackgroundTask> = {}): BackgroundTask {
  // A server row carries no command.
  const { command: _command, ...row } = task({
    id: "bg_9",
    kind: "server",
    label: "preview demo",
    url: "http://127.0.0.1:4321/",
    timeout_seconds: 0,
    ...over,
  });
  return row;
}

test("a folded preview server card carries its address, tagged as a server", () => {
  // A preview server is the page it answers with, the way a subagent run is the
  // conversation it holds: the way in belongs on the card the operator already
  // sees, on the same row of its own under the status.
  const onStopTask = vi.fn();
  renderPanel({ tasks: [task(), serverTask()], onStopTask });

  expect(screen.getByTestId("bgtask-tag-bg_9")).toHaveTextContent("server");
  expect(screen.getByTestId("bgtask-title-bg_9")).toHaveTextContent(
    "preview demo",
  );
  const opener = screen.getByTestId("bgtask-open-bg_9");
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  const link = screen.getByTestId("bgtask-link-bg_9");
  expect(link).toHaveTextContent("http://127.0.0.1:4321/");
  expect(link).toHaveAttribute("href", "http://127.0.0.1:4321/");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link).toHaveAttribute("rel", "noopener noreferrer");

  // It rides the meta line, on a row of its own.
  expect(screen.getByTestId("bgtask-meta-bg_9")).toContainElement(link);
  expect(link.parentElement).toHaveClass("bgtask-card-address-row");

  // The link is not the opener: following it leaves the card folded.
  expect(opener.contains(link)).toBe(false);
  fireEvent.click(link);
  expect(screen.queryByTestId("bgtask-body-bg_9")).toBeNull();
  expect(opener.getAttribute("aria-expanded")).toBe("false");

  // A card carries one way in at most: a task is one kind or the other.
  expect(screen.queryByTestId("bgtask-open-transcript-bg_9")).toBeNull();
  // A shell command has no page behind it.
  expect(screen.queryByTestId("bgtask-link-bg_1")).toBeNull();

  fireEvent.click(screen.getByTestId("bgtask-stop-bg_9"));
  expect(onStopTask).toHaveBeenCalledWith("bg_9");
});

test("an open preview server card does not repeat the address, and a stopped one has no dead link or exit code", async () => {
  renderPanel({
    tasks: [
      serverTask(),
      serverTask({
        id: "bg_10",
        label: "preview old",
        url: "http://127.0.0.1:4000/",
        running: false,
        status: "stopped",
        exit_code: 0,
        finished_at: new Date(START_MS + 30_000).toISOString(),
        elapsed_seconds: 30,
      }),
    ],
    loadOutput: outputsOf({ bg_9: "GET / 200" }),
  });

  // No shell stands behind a preview server: no command block, and the address
  // is the folded card's, not said a second time inside it.
  fireEvent.click(screen.getByTestId("bgtask-open-bg_9"));
  const body = await screen.findByTestId("bgtask-body-bg_9");
  expect(screen.queryByTestId("bgtask-command-bg_9")).toBeNull();
  expect(body).not.toHaveTextContent("http://127.0.0.1:4321/");
  expect(body.contains(screen.getByTestId("bgtask-link-bg_9"))).toBe(false);
  await waitFor(() =>
    expect(screen.getByTestId("bgtask-output-bg_9")).toHaveTextContent(
      "GET / 200",
    ),
  );

  // A server that has stopped answers nothing: no link to a refused connection,
  // and no exit code either - the pool's code for it is synthetic, as it is for
  // an agent run.
  fireEvent.click(screen.getByTestId("bgtask-finished-toggle"));
  expect(screen.queryByTestId("bgtask-link-bg_10")).toBeNull();
  expect(screen.getByTestId("bgtask-meta-bg_10")).not.toHaveTextContent(
    "exit",
  );
  fireEvent.click(screen.getByTestId("bgtask-open-bg_10"));
  const foot = screen.getByTestId("bgtask-foot-bg_10");
  expect(foot).not.toHaveTextContent("Exit code");
  expect(foot).toHaveTextContent(/^Stopped$/);
});

test("an address that is not http(s) never becomes a link", () => {
  renderPanel({ tasks: [serverTask({ url: "javascript:alert(1)" })] });
  expect(screen.queryByTestId("bgtask-link-bg_9")).toBeNull();
});
