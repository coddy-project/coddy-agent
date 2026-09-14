import React, { useCallback, useState } from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ToolCallMessage } from "./ToolCallMessage";
import type { BackgroundTask } from "../tasks/types";
import { setLocale } from "../i18n/i18n";
import { setHostShell } from "../chat/hostShell";

afterEach(() => {
  cleanup();
  setLocale("en");
});

function openToolDetails() {
  fireEvent.click(screen.getByLabelText("Tool summary"));
}

function mockPreviewOverflow() {
  const scrollHeight = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "scrollHeight",
  );
  const clientHeight = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "clientHeight",
  );
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get() {
      return (this as HTMLElement).dataset.testid ===
        "permission-preview-viewport"
        ? 520
        : 0;
    },
  });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get() {
      return (this as HTMLElement).dataset.testid ===
        "permission-preview-viewport"
        ? 120
        : 0;
    },
  });
  return () => {
    if (scrollHeight) {
      Object.defineProperty(
        HTMLElement.prototype,
        "scrollHeight",
        scrollHeight,
      );
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "scrollHeight");
    }
    if (clientHeight) {
      Object.defineProperty(
        HTMLElement.prototype,
        "clientHeight",
        clientHeight,
      );
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    }
  };
}

test("truncated tool shows the shared More button, fetches once, then Less restores preview", async () => {
  const fetchSpy = vi.fn();
  function Harness() {
    const [full, setFull] = useState<string | undefined>();
    const onFetch = useCallback(async (id: string) => {
      fetchSpy(id);
      await Promise.resolve();
      setFull("full line 1\nfull line 2\nfull line 3");
    }, []);
    return (
      <ToolCallMessage
        toolCallId="tc-1"
        title="list_dir"
        kind="bash"
        status="completed"
        argsText="{}"
        resultText={`${"a\n".repeat(18)}last preview line\n...`}
        fullResultText={full}
        resultWasTruncated
        durationMs={42}
        onFetchToolCallFull={onFetch}
      />
    );
  }
  render(<Harness />);
  openToolDetails();

  const pre = document.querySelector(".tool-result-pre");
  expect(pre?.textContent ?? "").toMatch(/\n\.\.\.\s*$/);
  expect(pre?.textContent?.split("\n").pop()?.trim()).toBe("...");

  const more = screen.getByTestId("tool-result-more");
  expect(more).toHaveTextContent("More…");
  expect(more).toHaveClass("tool-overflow-toggle");
  expect(
    screen
      .getByLabelText("Tool result")
      .querySelector(".tool-call-result-content")?.className,
  ).toContain("tool-result-viewport--tall");
  expect(
    screen
      .getByLabelText("Tool result")
      .querySelector(".tool-call-result-content")?.className,
  ).toContain("tool-result-viewport--clip");

  fireEvent.click(more);
  await waitFor(() => expect(fetchSpy).toHaveBeenCalledWith("tc-1"));
  await waitFor(() =>
    expect(screen.getByTestId("tool-result-less")).toBeInTheDocument(),
  );
  expect(screen.getByTestId("tool-result-less")).toHaveTextContent("Less");
  expect(screen.getByText(/full line 3/)).toBeInTheDocument();
  expect(
    screen
      .getByLabelText("Tool result")
      .querySelector(".tool-call-result-content")?.className,
  ).toContain("tool-result-viewport--scroll");

  fireEvent.click(screen.getByTestId("tool-result-less"));
  expect(screen.queryByTestId("tool-result-less")).toBeNull();
  expect(screen.getByTestId("tool-result-more")).toBeInTheDocument();
  expect(screen.getByText(/last preview line/)).toBeInTheDocument();
  expect(
    screen
      .getByLabelText("Tool result")
      .querySelector(".tool-call-result-content")?.className,
  ).toContain("tool-result-viewport--clip");

  fireEvent.click(screen.getByTestId("tool-result-more"));
  await waitFor(() =>
    expect(screen.getByTestId("tool-result-less")).toBeInTheDocument(),
  );
  expect(fetchSpy).toHaveBeenCalledTimes(1);
});

test("no load-more row when preview is not truncated", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-2"
      title="read_file"
      status="completed"
      resultText="short"
      durationMs={10}
      onFetchToolCallFull={vi.fn()}
    />,
  );
  openToolDetails();
  expect(screen.queryByTestId("tool-result-more")).toBeNull();
  expect(screen.getByLabelText("Tool result").className).not.toContain(
    "tool-result-viewport--tall",
  );
});

test("large write preview scrolls inside the tool card until Less is clicked", () => {
  const restoreMeasurements = mockPreviewOverflow();
  try {
    const content = Array.from(
      { length: 48 },
      (_, i) => `export const value${i + 1} = ${i + 1};`,
    ).join("\n");
    const { container } = render(
      <ToolCallMessage
        toolCallId="tc-write-large"
        title="write"
        kind="write"
        status="completed"
        argsText={JSON.stringify({ path: "src/generated.ts", content })}
        resultText="Wrote src/generated.ts"
        durationMs={12}
      />,
    );
    openToolDetails();

    const viewport = screen.getByTestId("permission-preview-viewport");
    expect(viewport).toHaveClass("permission-preview-viewport--clip");
    expect(screen.getByText("More…")).toHaveClass("tool-overflow-toggle");
    expect(container.querySelector(".permission-preview .md-copy")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "More…" }));
    expect(viewport).toHaveClass("permission-preview-viewport--scroll");
    viewport.scrollTop = 80;
    fireEvent.click(screen.getByRole("button", { name: "Less" }));
    expect(viewport).toHaveClass("permission-preview-viewport--clip");
    expect(viewport.scrollTop).toBe(0);
  } finally {
    restoreMeasurements();
  }
});

test("restored large write fetches full arguments before showing More", async () => {
  const restoreMeasurements = mockPreviewOverflow();
  try {
    const fetchSpy = vi.fn();
    const content = Array.from(
      { length: 48 },
      (_, i) => `restored line ${i + 1} with enough content for the viewport`,
    ).join("\n");

    function Harness() {
      const [argsText, setArgsText] = useState(
        '{"path":"restored.txt","content":"restored line 1...',
      );
      const onFetch = useCallback(async (id: string) => {
        fetchSpy(id);
        await Promise.resolve();
        setArgsText(JSON.stringify({ path: "restored.txt", content }));
      }, []);
      return (
        <ToolCallMessage
          toolCallId="tc-write-restored"
          title="write"
          kind="write"
          status="completed"
          argsText={argsText}
          resultText="Wrote restored.txt"
          onFetchToolCallFull={onFetch}
        />
      );
    }

    render(<Harness />);
    openToolDetails();

    await waitFor(() =>
      expect(fetchSpy).toHaveBeenCalledWith("tc-write-restored"),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "More…" })).toBeInTheDocument(),
    );
    expect(screen.getByText(/restored line 48/)).toBeInTheDocument();
  } finally {
    restoreMeasurements();
  }
});

test("large apply_patch preview uses the same internal overflow controls", () => {
  const restoreMeasurements = mockPreviewOverflow();
  try {
    const body = Array.from(
      { length: 40 },
      (_, i) => `+new line ${i + 1}`,
    ).join("\n");
    const patch = [
      "--- a/src/large.ts",
      "+++ b/src/large.ts",
      "@@ -0,0 +1,40 @@",
      body,
    ].join("\n");
    render(
      <ToolCallMessage
        toolCallId="tc-patch-large"
        title="apply_patch"
        kind="write"
        status="completed"
        argsText={JSON.stringify({ filePath: "src/large.ts", patch })}
        resultText="Patch applied successfully"
      />,
    );
    openToolDetails();

    const viewport = screen.getByTestId("permission-preview-viewport");
    expect(viewport).toHaveClass("permission-preview-viewport--clip");
    fireEvent.click(screen.getByRole("button", { name: "More…" }));
    expect(viewport).toHaveClass("permission-preview-viewport--scroll");
    expect(screen.getByRole("button", { name: "Less" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  } finally {
    restoreMeasurements();
  }
});

test("large edit preview is capped like write and apply_patch", () => {
  const restoreMeasurements = mockPreviewOverflow();
  try {
    const oldString = Array.from(
      { length: 60 },
      (_, i) => `const before${i + 1} = ${i + 1};`,
    ).join("\n");
    const newString = Array.from(
      { length: 60 },
      (_, i) => `const after${i + 1} = ${i + 1};`,
    ).join("\n");
    render(
      <ToolCallMessage
        toolCallId="tc-edit-large"
        title="edit"
        kind="write"
        status="completed"
        argsText={JSON.stringify({
          path: "src/edited.ts",
          oldString,
          newString,
        })}
        resultText="Edited src/edited.ts"
      />,
    );
    openToolDetails();

    const viewport = screen.getByTestId("permission-preview-viewport");
    expect(viewport).toHaveClass("permission-preview-viewport--clip");
    fireEvent.click(screen.getByRole("button", { name: "More…" }));
    expect(viewport).toHaveClass("permission-preview-viewport--scroll");
    viewport.scrollTop = 64;
    fireEvent.click(screen.getByRole("button", { name: "Less" }));
    expect(viewport).toHaveClass("permission-preview-viewport--clip");
    expect(viewport.scrollTop).toBe(0);
  } finally {
    restoreMeasurements();
  }
});

test("restored edit recovers the diff from truncated list arguments", async () => {
  const fetchSpy = vi.fn();
  const oldString = Array.from(
    { length: 30 },
    (_, i) => `const before${i + 1} = ${i + 1};`,
  ).join("\n");
  const newString = Array.from(
    { length: 30 },
    (_, i) => `const after${i + 1} = ${i + 1};`,
  ).join("\n");

  function Harness() {
    const [argsText, setArgsText] = useState(
      '{"path":"src/edited.ts","oldString":"const before1 = 1;\\nconst befo',
    );
    const onFetch = useCallback(async (id: string) => {
      fetchSpy(id);
      await Promise.resolve();
      setArgsText(
        JSON.stringify({ path: "src/edited.ts", oldString, newString }),
      );
    }, []);
    return (
      <ToolCallMessage
        toolCallId="tc-edit-restored"
        title="edit"
        kind="write"
        status="completed"
        argsText={argsText}
        resultText="Edited src/edited.ts"
        onFetchToolCallFull={onFetch}
      />
    );
  }

  const { container } = render(<Harness />);
  openToolDetails();

  // Truncated args parse to nothing, so the card starts with an empty "+0 −0" preview.
  await waitFor(() =>
    expect(fetchSpy).toHaveBeenCalledWith("tc-edit-restored"),
  );
  // The path is on both the summary row and the preview bar; assert the bar.
  await waitFor(() =>
    expect(
      container.querySelector(".permission-preview-location"),
    ).toHaveTextContent("src/edited.ts"),
  );
  expect(screen.getByText(/const after30/)).toBeInTheDocument();
});

test("truncated tool does not show toggle without fetch handler", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-3"
      title="run"
      status="completed"
      resultText="a\n..."
      resultWasTruncated
    />,
  );
  openToolDetails();
  expect(screen.queryByTestId("tool-result-more")).toBeNull();
});

test("summary matches thinking-row pattern: chevron, tool name, duration", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-4"
      title="glob"
      status="completed"
      resultText="ok"
      durationMs={125}
      onFetchToolCallFull={vi.fn()}
    />,
  );
  const row = container.querySelector(".thinking-row.coddy-tool-call-row");
  expect(row).toBeTruthy();
  expect(
    container.querySelector(
      ".thinking-row.coddy-tool-call-row .thinking-chevron",
    ),
  ).toBeTruthy();
  expect(screen.getByText("looking for files")).toBeInTheDocument();
  expect(container.querySelector(".thinking-dur")?.textContent).toBe("125ms");
});

test("todo update renders the saved plan row and omits a successful boilerplate result", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-todo-update"
      title="coddy_todo_item_update"
      kind="todo"
      status="completed"
      argsText={JSON.stringify({ index: 1, status: "completed" })}
      todoPlan={[
        { content: "Inspect existing cards", status: "pending" },
        { content: "Render structured preview", status: "completed" },
        { content: "Add interaction tests", status: "pending" },
      ]}
      resultText="updated item 1"
    />,
  );

  openToolDetails();

  expect(screen.getByText("Updated item")).toBeInTheDocument();
  expect(screen.getByText("2 of 3")).toBeInTheDocument();
  expect(screen.getByText("Render structured preview")).toBeInTheDocument();
  expect(screen.queryByText("Inspect existing cards")).toBeNull();
  expect(container.querySelector(".todo-tool-preview-row--completed")).not.toBeNull();
  expect(container.querySelector("[aria-label='Tool result']")).toBeNull();
});

test("plan exit shows the completed mode transition without its boilerplate result", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-plan-exit"
      title="plan_exit"
      status="completed"
      argsText="{}"
      resultText="switched session to agent mode"
    />,
  );

  openToolDetails();

  expect(screen.getByText("Plan mode")).toBeInTheDocument();
  expect(screen.getAllByText("Agent mode")).toHaveLength(2);
  expect(screen.getByText("Switched to Agent mode")).toBeInTheDocument();
  expect(container.querySelector(".plan-exit-preview--completed")).not.toBeNull();
  expect(container.querySelector("[aria-label='Tool result']")).toBeNull();
});

test("completed mkdir uses the rich tool preview without approval actions", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-mkdir"
      title="mkdir"
      kind="write"
      status="completed"
      argsText={JSON.stringify({ parents: true, path: "build" })}
      resultText={"created directory H:\\workspace\\build"}
      durationMs={22_000}
    />,
  );

  openToolDetails();

  expect(container.querySelector(".permission-preview")).not.toBeNull();
  expect(
    container.querySelector(".permission-preview-location")?.textContent,
  ).toBe("build");
  expect(
    container.querySelector(".permission-preview-meta")?.textContent,
  ).toContain("create parents");
  expect(container.querySelector("[aria-label='Tool arguments']")).toBeNull();
  expect(container.querySelector(".permission-prompt-actions")).toBeNull();
  expect(
    container.querySelector(".tool-call-result-card")?.textContent,
  ).toContain("created directory H:\\workspace\\build");
});

test("question tool omits duration from summary row", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-q"
      title="question"
      status="completed"
      argsText={JSON.stringify({
        questions: [{ question: "Continue?", options: [{ label: "Yes" }] }],
      })}
      resultText={JSON.stringify({ answers: [["Yes"]] })}
      durationMs={1006}
    />,
  );
  expect(container.querySelector(".thinking-dur")).toBeNull();
  expect(container.querySelector(".thinking-label")?.textContent?.trim()).toBe(
    "question",
  );
  openToolDetails();
  expect(screen.getByText("Continue?")).toBeInTheDocument();
  expect(screen.getByText("Yes")).toBeInTheDocument();
});

test("question tool shows human timeline readout instead of raw JSON blobs", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-q2"
      title="question"
      kind="question"
      status="completed"
      argsText={JSON.stringify({
        questions: [
          { question: "Go on?", options: [{ label: "Yes" }, { label: "No" }] },
        ],
      })}
      resultText={JSON.stringify({ answers: [["Yes"]] })}
      durationMs={10}
    />,
  );
  openToolDetails();
  expect(document.querySelector(".tool-result-pre")).toBeNull();
  expect(screen.getByLabelText("Tool call details")).toBeTruthy();
  expect(screen.getByText("Go on?")).toBeInTheDocument();
  expect(screen.queryByText(/"questions"/)).toBeNull();
});

test("in-progress tool shows ellipsis on label and elapsed from startedAtMs", () => {
  const t0 = Date.now() - 2500;
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-5"
      title="run_cmd"
      status="in_progress"
      startedAtMs={t0}
      argsText="{}"
    />,
  );
  expect(screen.getByText("run_cmd...")).toBeTruthy();
  const dur = container.querySelector(".thinking-dur")?.textContent ?? "";
  expect(dur).toMatch(/^\d+ms$|^\d/);
});

test("elapsed freezes while permission is pending", () => {
  vi.useFakeTimers();
  const t0 = Date.now() - 5000;
  const { container, rerender } = render(
    <ToolCallMessage
      toolCallId="tc-perm"
      title="run_command"
      status="in_progress"
      startedAtMs={t0}
      permissionWaiting
      argsText="{}"
    />,
  );
  const durBefore = container.querySelector(".thinking-dur")?.textContent ?? "";
  vi.advanceTimersByTime(10_000);
  rerender(
    <ToolCallMessage
      toolCallId="tc-perm"
      title="run_command"
      status="in_progress"
      startedAtMs={t0}
      permissionWaiting
      argsText="{}"
    />,
  );
  expect(container.querySelector(".thinking-dur")?.textContent).toBe(durBefore);
  vi.useRealTimers();
});

test("apply_patch renders the shared rich diff instead of raw args JSON", () => {
  const patch = [
    "--- a/src/app.ts",
    "+++ b/src/app.ts",
    "@@ -1,2 +1,3 @@",
    " line1",
    "+added",
    " line2",
  ].join("\n");
  const argsText = JSON.stringify({ filePath: "src/app.ts", patch });
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-1"
      title="apply_patch"
      kind="write"
      status="completed"
      argsText={argsText}
      resultText="patch applied successfully to src/app.ts"
      durationMs={12}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".permission-preview-diff")).not.toBeNull();
  // file path shown
  expect(
    container.querySelector(".permission-preview-location")?.textContent,
  ).toContain("src/app.ts");
  // add line class present
  expect(
    container.querySelectorAll(".diff-line--add").length,
  ).toBeGreaterThanOrEqual(1);
  // raw args JSON not shown
  expect(
    container.querySelector("pre.tool-block[aria-label='Tool arguments']"),
  ).toBeNull();
});

test("apply_patch omits raw result text and has no tool-result-pre", () => {
  const patch = "@@ -1 +1 @@\n+new";
  const argsText = JSON.stringify({ filePath: "x.ts", patch });
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-2"
      title="apply_patch"
      kind="write"
      status="completed"
      argsText={argsText}
      resultText="patch applied successfully to x.ts"
      durationMs={5}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".permission-preview-diff")).not.toBeNull();
  expect(container.querySelector(".tool-result-pre")).toBeNull();
  expect(container.querySelector("[aria-label='Tool result']")).toBeNull();
  expect(container.querySelector(".tool-overflow-toggle")).toBeNull();
  expect(container.querySelector(".permission-preview .md-copy")).toBeNull();
});

test("apply_patch with V4A patch format renders the shared rich diff", () => {
  const v4aPatch = [
    "*** Begin Patch",
    "*** Update File: src/app.ts",
    "@@",
    " line1",
    "-old",
    "+new",
    " line3",
    "*** End Patch",
  ].join("\n");
  const argsText = JSON.stringify({ filePath: "src/app.ts", patch: v4aPatch });
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-v4a"
      title="apply_patch"
      kind="write"
      status="completed"
      argsText={argsText}
      resultText="patch applied successfully to src/app.ts"
      durationMs={8}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".permission-preview-diff")).not.toBeNull();
  expect(
    container.querySelectorAll(".diff-line--del").length,
  ).toBeGreaterThanOrEqual(1);
  expect(
    container.querySelectorAll(".diff-line--add").length,
  ).toBeGreaterThanOrEqual(1);
  expect(container.querySelector(".tool-result-pre")).toBeNull();
});

test("apply_patch shows error text in body when execution fails", () => {
  const patch = "@@ -1 +1 @@\n-old\n+new";
  const argsText = JSON.stringify({ filePath: "src/x.ts", patch });
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-err"
      title="apply_patch"
      kind="write"
      status="failed"
      argsText={argsText}
      resultText="error: file not found: src/x.ts"
      durationMs={3}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".tool-result-pre")).not.toBeNull();
  expect(container.querySelector("[aria-label='Tool result']")).not.toBeNull();
  expect(container.querySelector(".tool-result-pre")?.textContent).toContain(
    "file not found",
  );
  const body = container.querySelector(".coddy-tool-call-body");
  expect(body).toHaveClass("coddy-tool-call-body--connected-result");
  expect(
    body?.querySelector(
      ":scope > .permission-preview + .tool-call-result-card",
    ),
  ).not.toBeNull();
});

test("failed apply_patch joins an empty diff header directly to its result", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-empty-error"
      title="apply_patch"
      kind="write"
      status="failed"
      argsText={JSON.stringify({
        filePath: "build/approval-preview.txt",
        patch: "*** Begin Patch\n*** End Patch",
      })}
      resultText="error: file not found: build/approval-preview.txt"
      durationMs={9_000}
    />,
  );

  openToolDetails();
  expect(container.querySelector(".permission-preview-viewport")).toBeNull();
  expect(container.querySelector(".permission-preview-bar")).toHaveClass(
    "permission-preview-bar--standalone",
  );
  expect(container.querySelector(".coddy-tool-call-body")).toHaveClass(
    "coddy-tool-call-body--connected-result",
  );
});

test("apply_patch with error shows diff alongside error text", () => {
  const patch = "@@ -1 +1 @@\n-old\n+new";
  const argsText = JSON.stringify({ filePath: "src/y.ts", patch });
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-patch-err2"
      title="apply_patch"
      kind="write"
      status="failed"
      argsText={argsText}
      resultText="hunk mismatch at line 1"
      durationMs={4}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".permission-preview-diff")).not.toBeNull();
  expect(container.querySelector(".tool-result-pre")).not.toBeNull();
  expect(container.querySelector("[aria-label='Tool result']")).not.toBeNull();
});

const BG_START_MS = Date.parse("2026-07-29T12:00:00Z");

function backgroundTask(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "s1",
    kind: "command",
    label: "make test",
    command: "make test",
    status: "running",
    started_at: new Date(BG_START_MS).toISOString(),
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
}

test("a backgrounded run_command reads like any command row, in the background", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      argsText='{"command":"make test","background":true,"expected_seconds":120}'
      resultText="Started background task bg_1: make test"
      durationMs={0}
      backgroundTask={backgroundTask({ expected_seconds: 120 })}
      backgroundNowMs={BG_START_MS + 30_000}
    />,
  );

  expect(screen.getByText("running a command in the background")).toBeInTheDocument();

  // The call returned the instant the task started, so its own 0ms is replaced
  // by the task's clock - in the duration slot every other row uses.
  const duration = container.querySelector(".thinking-dur");
  expect(duration).toHaveTextContent("30s");
  expect(duration?.closest(".thinking-head")).not.toBeNull();

  // How the run is going belongs to the task, and is read in the Tasks panel:
  // the transcript row carries no status, estimate or exit code.
  const row = container.querySelector(".coddy-tool-call-row") as HTMLElement;
  expect(container.querySelector(".tool-bgtask-state")).toBeNull();
  expect(container.querySelector(".tool-bgtask-chip")).toBeNull();
  expect(row.querySelector(".thinking-summary")?.textContent).not.toMatch(
    /Running|est\.|exit/,
  );
});

test("a finished background task shows its total time and nothing about how it ended", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      backgroundTask={backgroundTask({
        running: false,
        status: "timed_out",
        elapsed_seconds: 900,
        exit_code: 2,
      })}
      backgroundNowMs={BG_START_MS + 9_000_000}
    />,
  );
  const summary = container.querySelector(".thinking-summary") as HTMLElement;
  expect(container.querySelector(".thinking-dur")).toHaveTextContent("15m");
  expect(summary.textContent).not.toMatch(/Timed out|exit 2/);
});

test("expanded background row offers Open in Tasks, and Stop only while running", () => {
  const onOpen = vi.fn();
  const onStop = vi.fn();
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      backgroundTask={backgroundTask()}
      backgroundNowMs={BG_START_MS + 1_000}
      onOpenBackgroundTask={onOpen}
      onStopBackgroundTask={onStop}
    />,
  );
  openToolDetails();

  fireEvent.click(screen.getByTestId("tool-bgtask-open-bg_1"));
  expect(onOpen).toHaveBeenCalledWith("bg_1");
  fireEvent.click(screen.getByTestId("tool-bgtask-stop-bg_1"));
  expect(onStop).toHaveBeenCalledWith("bg_1");

  rerender(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      backgroundTask={backgroundTask({
        running: false,
        status: "succeeded",
        exit_code: 0,
      })}
      backgroundNowMs={BG_START_MS + 1_000}
      onOpenBackgroundTask={onOpen}
      onStopBackgroundTask={onStop}
    />,
  );
  expect(screen.queryByTestId("tool-bgtask-stop-bg_1")).toBeNull();
  expect(screen.getByTestId("tool-bgtask-open-bg_1")).toBeInTheDocument();
});

test("an ordinary tool row keeps its own duration", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-plain"
      title="read"
      status="completed"
      resultText="file contents"
      durationMs={12}
    />,
  );
  expect(screen.queryByTestId(/^tool-bgtask-elapsed-/)).toBeNull();
  expect(container.querySelector(".thinking-dur")).toHaveTextContent("12ms");
});

test("restored in_progress write still fetches full arguments", async () => {
  const fetchSpy = vi.fn(async () => {});
  render(
    <ToolCallMessage
      toolCallId="tc-write-inflight"
      title="write"
      kind="write"
      status="in_progress"
      argsText={'{"path":"restored.txt","content":"start of a long'}
      onFetchToolCallFull={fetchSpy}
    />,
  );
  await waitFor(() =>
    expect(fetchSpy).toHaveBeenCalledWith("tc-write-inflight"),
  );
});

test("arguments re-truncated by a reconcile trigger a second fetch", async () => {
  const full = JSON.stringify({
    path: "reconciled.txt",
    content: "line\n".repeat(60),
  });
  const truncated = full.slice(0, 200) + "...";
  const fetchSpy = vi.fn();
  let setArgsExternal: (v: string) => void = () => {};
  function Harness() {
    const [argsText, setArgsText] = useState(truncated);
    setArgsExternal = setArgsText;
    const onFetch = useCallback(async (id: string) => {
      fetchSpy(id);
      await Promise.resolve();
      setArgsText(full);
    }, []);
    return (
      <ToolCallMessage
        toolCallId="tc-write-reconcile"
        title="write"
        kind="write"
        status="completed"
        argsText={argsText}
        resultText="Wrote reconciled.txt"
        onFetchToolCallFull={onFetch}
      />
    );
  }
  render(<Harness />);
  await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1));
  // A later loadMessages reconcile overwrites the recovered args with the
  // truncated list preview again; the card must fetch once more, not go blank.
  act(() => setArgsExternal(truncated));
  await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(2));
});

test("overflow toggle appears after a collapsed foldout is opened", async () => {
  let revealed = false;
  const sh = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "scrollHeight",
  );
  const ch = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "clientHeight",
  );
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get() {
      return revealed &&
        (this as HTMLElement).dataset.testid === "permission-preview-viewport"
        ? 520
        : 0;
    },
  });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get() {
      return revealed &&
        (this as HTMLElement).dataset.testid === "permission-preview-viewport"
        ? 120
        : 0;
    },
  });
  try {
    const content = Array.from({ length: 48 }, (_, i) => `line ${i}`).join(
      "\n",
    );
    const { container } = render(
      <ToolCallMessage
        toolCallId="tc-write-foldout"
        title="write"
        kind="write"
        status="completed"
        argsText={JSON.stringify({ path: "src/foldout.ts", content })}
        resultText="Wrote src/foldout.ts"
      />,
    );
    // Collapsed foldout: the hidden viewport measures 0, so no toggle is offered.
    expect(screen.queryByTestId("tool-preview-more")).toBeNull();
    revealed = true;
    openToolDetails();
    // The <details> toggle event is what real browsers deliver when the body
    // stops being display:none; ResizeObserver is unavailable here like in
    // engines that miss the un-hide resize.
    const details = container.querySelector("details");
    fireEvent(details!, new Event("toggle"));
    await waitFor(() =>
      expect(screen.getByTestId("tool-preview-more")).toBeInTheDocument(),
    );
  } finally {
    if (sh) {
      Object.defineProperty(HTMLElement.prototype, "scrollHeight", sh);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "scrollHeight");
    }
    if (ch) {
      Object.defineProperty(HTMLElement.prototype, "clientHeight", ch);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    }
  }
});

test("preview and result toggles expose distinct test ids", () => {
  const restoreMeasurements = mockPreviewOverflow();
  try {
    const content = Array.from(
      { length: 48 },
      (_, i) => `export const value${i + 1} = ${i + 1};`,
    ).join("\n");
    render(
      <ToolCallMessage
        toolCallId="tc-write-testids"
        title="write"
        kind="write"
        status="completed"
        argsText={JSON.stringify({ path: "src/ids.ts", content })}
        resultText={"line\n".repeat(20)}
        resultWasTruncated={true}
        onFetchToolCallFull={vi.fn(async () => {})}
      />,
    );
    openToolDetails();
    expect(screen.getByTestId("tool-preview-more")).toBeInTheDocument();
    expect(screen.getByTestId("tool-result-more")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("tool-preview-more"));
    expect(screen.getByTestId("tool-preview-less")).toBeInTheDocument();
    expect(screen.getByTestId("tool-result-more")).toBeInTheDocument();
  } finally {
    restoreMeasurements();
  }
});

test("write_file cards render the shared write preview", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-write-file"
      title="write_file"
      status="completed"
      argsText={JSON.stringify({ filePath: "src/wf.ts", content: "hello" })}
      resultText="ok"
    />,
  );
  openToolDetails();
  expect(
    container.querySelector(".permission-preview-location"),
  ).toHaveTextContent("src/wf.ts");
  expect(screen.getByText("hello")).toBeInTheDocument();
});

test("short write previews never offer overflow controls without real overflow", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-write-short"
      title="write"
      status="completed"
      argsText={'{"path":"n.txt","content":"short"}'}
      resultText="ok"
    />,
  );
  openToolDetails();
  expect(screen.queryByTestId("tool-preview-more")).toBeNull();
  expect(screen.queryByText("More…")).toBeNull();
});

test("the summary row names the action, not the tool id", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-name"
      title="run_command"
      kind="execute"
      status="completed"
      argsText={JSON.stringify({ command: "ls" })}
      resultText="a.ts"
      durationMs={4}
    />,
  );
  expect(screen.getByText("running a command")).toHaveClass("thinking-label");
  expect(screen.queryByText("run_command")).toBeNull();
});

test("a tool outside the catalogue keeps its own id in the summary row", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-mcp"
      title="mcp__github__create_issue"
      status="completed"
      argsText={JSON.stringify({ title: "x" })}
      resultText="ok"
      durationMs={4}
    />,
  );
  expect(screen.getByText("mcp__github__create_issue")).toHaveClass(
    "thinking-label",
  );
});

test("a call with no arguments shows an action card instead of an empty JSON object", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-empty"
      title="coddy_todo_plan_archive"
      status="completed"
      argsText="{}"
      resultText="archived plan (3 items)"
      durationMs={0}
    />,
  );
  openToolDetails();

  // Same bar chrome and same status mark as the other plan tools.
  const card = screen.getByTestId("tool-action-preview");
  expect(card).toHaveClass("permission-preview-bar--standalone");
  expect(card).toHaveTextContent("archiving the plan");
  expect(card).toHaveTextContent("Done");
  expect(
    card.querySelector(".todo-tool-preview-mark--completed"),
  ).not.toBeNull();
  expect(container.textContent).not.toContain("{}");
  expect(container.querySelector(".permission-preview-code")).toBeNull();
  // The returned output still gets its own panel.
  expect(screen.getByText("archived plan (3 items)")).toBeTruthy();
});

test("the action card tracks the call it belongs to", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-empty-run"
      title="coddy_todo_plan_read"
      status="in_progress"
      argsText="{}"
      startedAtMs={Date.now()}
    />,
  );
  openToolDetails();
  expect(screen.getByTestId("tool-action-preview")).toHaveTextContent(
    "Running…",
  );
});

test("run_command puts the command in a shell block with a prompt and a copy control", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-shell"
      title="run_command"
      kind="execute"
      status="completed"
      argsText={JSON.stringify({ command: "make lint", timeout_seconds: 30 })}
      resultText="ok"
      durationMs={1200}
    />,
  );
  openToolDetails();

  const block = container.querySelector(".permission-preview-shell");
  expect(block).not.toBeNull();
  expect(
    block!.querySelector(".permission-preview-shell-prompt"),
  ).toHaveTextContent("$");
  expect(
    block!.querySelector(".permission-preview-shell-code"),
  ).toHaveTextContent("make lint");
  // The copy control lives on the command line, not in the card header above it.
  const copy = screen.getByTestId("tool-preview-copy");
  expect(block!.contains(copy)).toBe(true);
  expect(copy).toHaveAttribute("title", "Copy");
  expect(copy.textContent).toBe("");
  expect(
    container.querySelector(".permission-preview-bar .md-copy"),
  ).toBeNull();
});

test("previews that are not a shell command keep the transcript free of copy controls", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-write-no-copy"
      title="write"
      kind="write"
      status="completed"
      argsText={JSON.stringify({ path: "src/a.ts", content: "hello" })}
      resultText="Wrote src/a.ts"
      durationMs={3}
    />,
  );
  openToolDetails();
  expect(container.querySelector(".permission-preview .md-copy")).toBeNull();
});

test("load_skill names the skill next to the label and renders its markdown body", () => {
  const body = "# Subagent-Driven Development\n\nExecute the plan per task.";
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-skill"
      title="load_skill"
      status="completed"
      argsText={JSON.stringify({ name: "subagent-driven-development" })}
      resultText={body}
      durationMs={0}
    />,
  );
  expect(screen.getByText("loading a skill")).toHaveClass("thinking-label");
  expect(screen.getByTestId("tool-summary-target")).toHaveTextContent(
    "subagent-driven-development",
  );

  openToolDetails();
  // The skill name is already on the summary row, so the call needs no argument card,
  // and the instructions are the card - no section strip above them.
  expect(container.querySelector(".permission-preview")).toBeNull();
  expect(container.querySelector(".tool-call-result-head")).toBeNull();
  expect(screen.queryByText("Result")).toBeNull();
  const heading = container.querySelector(".tool-call-result-content h1");
  expect(heading).toHaveTextContent("Subagent-Driven Development");
  expect(container.querySelector(".tool-result-pre")).toBeNull();
});

test("a skill body renders its markdown lists, not raw dashes", () => {
  const body = "# Loop\n\n- first step;\n- second step.\n\n1. one\n2. two\n";
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-skill-lists"
      title="load_skill"
      status="completed"
      argsText={JSON.stringify({ name: "executing-plans" })}
      resultText={body}
      durationMs={0}
    />,
  );
  openToolDetails();
  const card = container.querySelector(".tool-call-result-content--markdown")!;
  expect(card.querySelectorAll("ul > li")).toHaveLength(2);
  expect(card.querySelectorAll("ol > li")).toHaveLength(2);
  expect(card.textContent).not.toContain("- first step");
});

test("load_skill without parseable arguments still renders the body", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-skill-raw"
      title="load_skill"
      status="completed"
      argsText='{"name":"code-rev'
      resultText="# Code review"
      durationMs={0}
    />,
  );
  expect(screen.queryByTestId("tool-summary-target")).toBeNull();
  openToolDetails();
  expect(container.querySelector(".tool-call-result-content h1")).toHaveTextContent(
    "Code review",
  );
});

test("the row names what the call acts on, clipped rather than wrapped", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-target"
      title="read"
      kind="read"
      status="completed"
      argsText={JSON.stringify({ path: "external/ui/src/ui/messages/Tool.tsx" })}
      resultText="ok"
      durationMs={3}
    />,
  );
  const target = screen.getByTestId("tool-summary-target");
  expect(target).toHaveTextContent("external/ui/src/ui/messages/Tool.tsx");
  // The full value stays reachable as the native tooltip when the row clips it.
  expect(target).toHaveAttribute(
    "title",
    "external/ui/src/ui/messages/Tool.tsx",
  );
  expect(screen.getByText("reading a file")).toHaveClass("thinking-label");
});

test("the row spells a path against the worktree it lives in, the tooltip keeps it whole", () => {
  // Work inside a worktree repeats the path to that worktree on every row, which
  // pushes the file name past the ellipsis. The row shows what tells the files
  // apart; the absolute path stays one hover (and one click) away.
  render(
    <ToolCallMessage
      toolCallId="tc-relative"
      title="read"
      kind="read"
      status="completed"
      pathRoots={[
        "/storage/Repository/coddy/coddy-agent",
        "/storage/Repository/coddy/coddy-agent/.coddy/worktrees/fix-session-stop-queue",
      ]}
      argsText={JSON.stringify({
        path: "/storage/Repository/coddy/coddy-agent/.coddy/worktrees/fix-session-stop-queue/DESIGN.md",
      })}
      resultText="ok"
      durationMs={3}
    />,
  );
  const target = screen.getByTestId("tool-summary-target");
  // Exact: the absolute path contains the relative one, so a substring match
  // would pass without the rewrite ever happening.
  expect(target.textContent).toBe("DESIGN.md");
  expect(target).toHaveAttribute(
    "title",
    "/storage/Repository/coddy/coddy-agent/.coddy/worktrees/fix-session-stop-queue/DESIGN.md",
  );
});

test("a command is never respelt against the session directory", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-command"
      title="run_command"
      kind="run_command"
      status="completed"
      pathRoots={["/storage/Repository/coddy/coddy-agent"]}
      argsText={JSON.stringify({
        command: "/storage/Repository/coddy/coddy-agent/scripts/checks.sh",
      })}
      resultText="ok"
      durationMs={3}
    />,
  );
  expect(screen.getByTestId("tool-summary-target").textContent).toBe(
    "/storage/Repository/coddy/coddy-agent/scripts/checks.sh",
  );
});

test("a call whose arguments name nothing opens with its body, not an empty strip", () => {
  // background_output takes a task id and a line count: no path, no command,
  // nothing for the header bar to say. The bar was rendered anyway, so the card
  // opened with a 34px empty strip above the arguments.
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-bgout"
      title="background_output"
      kind="background_output"
      status="completed"
      argsText={JSON.stringify({ task_id: "bg_3", tail_lines: 60 })}
      resultText="bg_3 [running] go test ./..."
      durationMs={4}
    />,
  );
  openToolDetails();

  expect(
    container.querySelector(".permission-preview-bar"),
    "a header bar with nothing in it is a strip of empty border",
  ).toBeNull();
  // With no bar the body carries the whole card, top corners included.
  expect(container.querySelector(".permission-preview-viewport")).toHaveClass(
    "permission-preview-viewport--headless",
  );
  // The arguments themselves still show.
  expect(
    container.querySelector(".permission-preview-code")?.textContent,
  ).toContain("bg_3");});

test("read tells a directory listing apart from a file when its arguments say so", () => {
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="tc-dir"
      title="read"
      kind="read"
      status="completed"
      argsText={JSON.stringify({ path: "external/ui/src/", recursive: true })}
      resultText="ui/"
      durationMs={1}
    />,
  );
  expect(screen.getByText("browsing a directory")).toBeInTheDocument();

  rerender(
    <ToolCallMessage
      toolCallId="tc-dir"
      title="read"
      kind="read"
      status="completed"
      argsText={JSON.stringify({ path: "external/ui/src/main.tsx" })}
      resultText="import"
      durationMs={1}
    />,
  );
  expect(screen.getByText("reading a file")).toBeInTheDocument();
});

test("a question row stays a bare label, with no target beside it", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-q"
      title="question"
      kind="question"
      status="completed"
      argsText={JSON.stringify({ questions: [{ question: "which?" }] })}
      resultText=""
    />,
  );
  expect(screen.queryByTestId("tool-summary-target")).toBeNull();
});

test("a failed load_skill stays raw text and says so on its row", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-skill-failed"
      title="load_skill"
      status="failed"
      argsText={JSON.stringify({ name: "nope" })}
      resultText={'# load_skill: unknown skill "nope"'}
      durationMs={1}
    />,
  );
  expect(screen.getByTestId("tool-failed-marker")).toHaveTextContent(
    "(failed)",
  );
  openToolDetails();

  // An error is not a skill: it keeps the monospace panel, not a markdown heading.
  expect(container.querySelector(".tool-call-result-content h1")).toBeNull();
  expect(container.querySelector(".tool-result-pre")).toHaveTextContent(
    'load_skill: unknown skill "nope"',
  );
});

test("output attaches straight under the call, with no Result strip anywhere", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-no-strip"
      title="run_command"
      kind="execute"
      status="completed"
      argsText={JSON.stringify({ command: "git status" })}
      resultText="nothing to commit"
      durationMs={40}
    />,
  );
  openToolDetails();

  expect(container.querySelector(".tool-call-result-head")).toBeNull();
  expect(container.querySelector(".tool-call-result-dot")).toBeNull();
  expect(screen.queryByText("Result")).toBeNull();
  // The output is still its own panel, right below the command block.
  expect(container.querySelector(".tool-result-pre")).toHaveTextContent(
    "nothing to commit",
  );
});

test("a failed call says so next to its label instead of in the output panel", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-failed"
      title="run_command"
      kind="execute"
      status="failed"
      argsText={JSON.stringify({ command: "make lint" })}
      resultText="exit status 1"
      durationMs={900}
    />,
  );

  const marker = screen.getByTestId("tool-failed-marker");
  expect(marker).toHaveTextContent("(failed)");
  // It belongs to the summary row, so a collapsed call already reads as failed.
  expect(marker.closest(".thinking-head")).not.toBeNull();
  openToolDetails();
  expect(container.querySelector(".tool-call-result-head")).toBeNull();
});

test("the failure marker is localized and absent when the call succeeded", () => {
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="tc-ok"
      title="run_command"
      kind="execute"
      status="completed"
      argsText={JSON.stringify({ command: "make lint" })}
      resultText="ok"
      durationMs={5}
    />,
  );
  expect(screen.queryByTestId("tool-failed-marker")).toBeNull();

  setLocale("ru");
  rerender(
    <ToolCallMessage
      toolCallId="tc-ok"
      title="run_command"
      kind="execute"
      status="failed"
      argsText={JSON.stringify({ command: "make lint" })}
      resultText="ошибка"
      durationMs={5}
    />,
  );
  expect(screen.getByTestId("tool-failed-marker")).toHaveTextContent("(ошибка)");
  setLocale("en");
});

test("the shell card names the interpreter the server actually runs", () => {
  setHostShell("/usr/bin/bash");
  try {
    const { container } = render(
      <ToolCallMessage
        toolCallId="tc-shell-name"
        title="run_command"
        kind="execute"
        status="completed"
        argsText={JSON.stringify({ command: "ls" })}
        resultText="a.ts"
        durationMs={4}
      />,
    );
    openToolDetails();
    expect(
      container.querySelector(".permission-preview-location"),
    ).toHaveTextContent("/usr/bin/bash");
    expect(screen.queryByText("Shell")).toBeNull();
  } finally {
    setHostShell("");
  }
});

test("without a reported interpreter the shell card keeps the generic label", () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-shell-fallback"
      title="run_command"
      kind="execute"
      status="completed"
      argsText={JSON.stringify({ command: "ls" })}
      resultText="a.ts"
      durationMs={4}
    />,
  );
  openToolDetails();
  expect(
    container.querySelector(".permission-preview-location"),
  ).toHaveTextContent("Shell");
});

test("a remote shell is not the local interpreter", () => {
  setHostShell("/usr/bin/bash");
  try {
    const { container } = render(
      <ToolCallMessage
        toolCallId="tc-ssh"
        title="ssh_run_command"
        kind="execute"
        status="completed"
        argsText={JSON.stringify({ command: "uptime" })}
        resultText="up 3 days"
        durationMs={4}
      />,
    );
    openToolDetails();
    expect(
      container.querySelector(".permission-preview-location"),
    ).toHaveTextContent("SSH shell");
  } finally {
    setHostShell("");
  }
});

// A refused spawn may be waiting for an approval. The row offers it outside its
// collapsed <details>, but only once the catalog says so - the error text is
// never parsed - and a spawn that did not fail asks nothing at all.
test("a refused spawn_agent row asks the catalog and offers the approval", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      calls.push(String(url));
      return Promise.resolve({
        ok: true,
        json: async () => ({
          workspace: "/work/repo",
          policy: "ask",
          items: [
            {
              name: "reviewer",
              description: "Reviews a diff.",
              scope: "project",
              path: "/work/repo/.coddy/agents/reviewer.md",
              builtin: false,
              hidden: false,
              trust: "needs_approval",
              trusted: false,
              needs_approval: true,
            },
          ],
        }),
      });
    }),
  );
  const args = JSON.stringify({ agent: "reviewer", prompt: "Review the diff." });
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="call_spawn"
      title="spawn_agent"
      status="completed"
      argsText={args}
      resultText="done"
      workspacePath="/work/repo"
    />,
  );
  expect(screen.queryByTestId("subagent-approval-reviewer")).toBeNull();
  expect(calls).toEqual([]);

  rerender(
    <ToolCallMessage
      toolCallId="call_spawn"
      title="spawn_agent"
      status="failed"
      argsText={args}
      resultText="subagent is not approved"
      workspacePath="/work/repo"
    />,
  );
  // Visible without opening the row.
  expect(
    await screen.findByTestId("subagent-approval-approve-reviewer"),
  ).toBeInTheDocument();
  expect(calls).toEqual(["/coddy/subagents?cwd=%2Fwork%2Frepo"]);
  vi.unstubAllGlobals();
});

test("a failed tool that is not spawn_agent offers no approval", () => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  render(
    <ToolCallMessage
      toolCallId="call_cmd"
      title="run_command"
      status="failed"
      argsText={JSON.stringify({ command: "ls", agent: "reviewer" })}
      resultText="error"
    />,
  );
  expect(fetchMock).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});
