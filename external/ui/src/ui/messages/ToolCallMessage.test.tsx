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

afterEach(() => cleanup());

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

  render(<Harness />);
  openToolDetails();

  // Truncated args parse to nothing, so the card starts with an empty "+0 −0" preview.
  await waitFor(() =>
    expect(fetchSpy).toHaveBeenCalledWith("tc-edit-restored"),
  );
  await waitFor(() =>
    expect(screen.getByTitle("src/edited.ts")).toBeInTheDocument(),
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
  expect(screen.getByText("glob")).toBeInTheDocument();
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

test("a backgrounded run_command keeps a live status chip on the collapsed row", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      argsText='{"command":"make test","background":true,"expected_seconds":120}'
      resultText="Started background task bg_1: make test"
      backgroundTask={backgroundTask({ expected_seconds: 120 })}
      backgroundNowMs={BG_START_MS + 30_000}
    />,
  );

  const chip = screen.getByTestId("tool-bgtask-chip-bg_1");
  expect(chip).toHaveTextContent("Running");
  expect(chip).toHaveTextContent("30s");
  expect(chip).toHaveTextContent("est. 2m");
});

test("the background chip reports the final state once the task ends", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-bg"
      title="run_command"
      status="completed"
      backgroundTask={backgroundTask({
        running: false,
        status: "timed_out",
        elapsed_seconds: 900,
      })}
      backgroundNowMs={BG_START_MS + 9_000_000}
    />,
  );
  expect(screen.getByTestId("tool-bgtask-chip-bg_1")).toHaveTextContent(
    "Timed out",
  );
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

test("an ordinary tool row carries no background chip", () => {
  render(
    <ToolCallMessage
      toolCallId="tc-plain"
      title="read"
      status="completed"
      resultText="file contents"
    />,
  );
  expect(screen.queryByTestId(/^tool-bgtask-chip-/)).toBeNull();
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
  render(
    <ToolCallMessage
      toolCallId="tc-write-file"
      title="write_file"
      status="completed"
      argsText={JSON.stringify({ filePath: "src/wf.ts", content: "hello" })}
      resultText="ok"
    />,
  );
  openToolDetails();
  expect(screen.getByTitle("src/wf.ts")).toBeInTheDocument();
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
