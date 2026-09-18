import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";
import { stripCoddyAttachmentsForUserDisplay } from "../skills/stripCoddyAttachments";

vi.mock("../skills/stripCoddyAttachments", { spy: true });

afterEach(() => cleanup());

test("renders system error notice", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    {
      id: "s1",
      type: "system_notice",
      level: "error",
      message: "LLM error: context exceeded",
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByRole("alert")).toBeInTheDocument();
  expect(screen.getByText("System")).toBeInTheDocument();
  expect(screen.getByText("LLM error: context exceeded")).toBeInTheDocument();
});

test("renders user, assistant, and tool call items", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "a1", type: "assistant_message", content: "Hi" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read_file",
      kind: "read",
      status: "completed",
      argsText: '{"path":"a.txt"}',
      resultText: "OK",
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByText("Hello")).toBeInTheDocument();
  expect(screen.getByText("Hi")).toBeInTheDocument();
  expect(screen.getByText("read_file")).toBeInTheDocument();
  expect(screen.getByLabelText("Tool summary")).toBeInTheDocument();
});

test("permission preview uses the matching tool call arguments", () => {
  const patch = [
    "--- a/src/app.ts",
    "+++ b/src/app.ts",
    "@@ -1,2 +1,2 @@",
    "-oldValue();",
    "+newValue();",
    " keep();",
  ].join("\n");
  const permissionPayload = {
    sessionId: "sess_x",
    toolCall: {
      toolCallId: "call_patch",
      title: "Run: apply_patch",
      kind: "write",
      content: [
        {
          type: "content",
          content: { type: "text", text: "Update the requested component" },
        },
      ],
    },
    options: [
      { optionId: "allow", name: "Allow", kind: "allow_once" },
      { optionId: "allow_always", name: "Allow always", kind: "allow_always" },
      { optionId: "reject", name: "Reject", kind: "reject_once" },
    ],
  };
  const items: TranscriptItem[] = [
    {
      id: "tool_patch",
      type: "tool_call",
      toolCallId: "call_patch",
      title: "apply_patch",
      kind: "write",
      status: "in_progress",
      argsText: JSON.stringify({ path: "src/app.ts", patch }),
    },
    {
      id: "permission_patch",
      type: "permission_prompt",
      payload: permissionPayload,
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByText("Apply this patch?")).toBeTruthy();
  const approvalDiff = within(
    screen.getByTestId("permission-prompt-card"),
  ).getByLabelText("Patch preview");
  expect(within(approvalDiff).getByText("oldValue();")).toBeTruthy();
  expect(within(approvalDiff).getByText("newValue();")).toBeTruthy();
  expect(screen.queryByText("Update the requested component")).toBeNull();
});

test("plan document forwards Run plan and Discard to the transcript handlers", () => {
  const items: TranscriptItem[] = [
    {
      id: "p1",
      type: "plan_document",
      slug: "demo-plan",
      name: "Demo plan",
      overview: "Short overview",
      content: "# Hello\n\nSteps",
      expanded: true,
    },
  ];
  const onRun = vi.fn();
  const onDiscard = vi.fn();

  render(
    <MessageList
      items={items}
      sessionId="sess_1"
      onPlanDocumentRun={onRun}
      onPlanDocumentDiscard={onDiscard}
    />,
  );

  fireEvent.click(screen.getByRole("button", { name: /run plan/i }));
  fireEvent.click(screen.getByRole("button", { name: /discard/i }));
  expect(onRun).toHaveBeenCalledWith("demo-plan");
  expect(onDiscard).toHaveBeenCalledWith("p1", "demo-plan");
});

test("plan document on a read-only transcript renders without Run plan and Discard", () => {
  // A subagent child transcript passes neither handler (like onEdit), so the
  // card must not show controls that would do nothing.
  const items: TranscriptItem[] = [
    {
      id: "p1",
      type: "plan_document",
      slug: "demo-plan",
      name: "Demo plan",
      overview: "Short overview",
      content: "# Hello\n\nSteps",
      expanded: true,
    },
  ];

  render(<MessageList items={items} sessionId="sess_0a1b2c" />);

  expect(screen.getByText("Demo plan")).toBeInTheDocument();
  expect(document.querySelector(".plan-document-card--readonly")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /run plan/i })).toBeNull();
  expect(screen.queryByRole("button", { name: /discard/i })).toBeNull();
});

test("renders memory copilot foldout", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hi" },
    {
      id: "m1",
      type: "memory_copilot",
      memoryRowId: "mem-1",
      userTurnIndex: 1,
      recallStatus: "completed",
      persistStatus: "completed",
      recallText: "- fact",
      recallReasoning: "",
      persistText: '{"save":false,"reason":"No durable fact to persist."}',
      persistReasoning: "",
      recallDurationMs: 10,
      persistDurationMs: 5,
      persistSaved: false,
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByTestId("memory-copilot-row")).toBeTruthy();
  expect(document.querySelector(".coddy-memory-recall")).toBeTruthy();
  expect(screen.getByText("fact")).toBeInTheDocument();
  expect(screen.getByText(/No durable fact to persist/)).toBeInTheDocument();
});

// The live line is on screen for the whole turn and always says what is happening,
// in general words at least: it used to drop its text under a reasoning row and to
// vanish altogether once the turn had written any text, which read as a turn that
// had stopped.
test("the live line names reasoning under a reasoning row", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    {
      id: "t1",
      type: "thinking",
      status: "in_progress",
      content: "weighing the options",
      startedAtMs: Date.now() - 4000,
    },
  ];

  render(<MessageList items={items} generating />);

  expect(screen.getByTestId("typing-dots-status")).toHaveTextContent("Thinking…");
});

test("the live line stays while the answer streams and names it", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "The price", streaming: true },
  ];

  render(<MessageList items={items} generating />);

  expect(screen.getByTestId("typing-dots-status")).toHaveTextContent(
    "Writing the answer",
  );
});

test("the live line stays under text written earlier in the turn", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "Checking.", streaming: true },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "tc1",
      title: "webfetch",
      status: "in_progress",
      argsText: '{"url":"https://coddy.dev/"}',
      startedAtMs: Date.now(),
    },
  ];

  render(<MessageList items={items} generating />);

  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots-status")).toHaveTextContent(
    "Fetching",
  );
});

test("a finished turn carries no live line", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "Done." },
  ];

  render(<MessageList items={items} />);

  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("the live line still speaks when the transcript is not already saying it", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
  ];

  render(<MessageList items={items} generating />);

  expect(screen.getByTestId("typing-dots-status")).toBeInTheDocument();
});

test("tool call message uses thinking-row wrapper next to thinking row", () => {
  const items: TranscriptItem[] = [
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "write_file",
      kind: "write",
      status: "completed",
      argsText: '{"path":"a.txt","content":"hi"}',
      resultText: "OK",
    },
    {
      id: "r1",
      type: "thinking",
      status: "completed",
      content: "thinking",
      durationMs: 10,
    },
  ];

  render(<MessageList items={items} />);

  const wrapper = screen.getByText("writing a file").closest(".thinking-row");
  expect(wrapper).toBeTruthy();
  expect(wrapper).toHaveClass("coddy-tool-call-row");

  // Tool and thinking are sibling foldout rows (same stack rhythm as messages-inner gap).
  expect(wrapper?.nextElementSibling).toHaveClass("thinking-row");
});

test("retry button surfaces only on the last system_notice", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "s1", type: "system_notice", level: "error", message: "first error" },
    { id: "u2", type: "user_message", content: "Again" },
    {
      id: "s2",
      type: "system_notice",
      level: "error",
      message: "model did not respond",
    },
  ];
  render(<MessageList items={items} onRetryLast={() => {}} />);
  expect(screen.getAllByTestId("system-message-retry")).toHaveLength(1);
});

test("no retry button when onRetryLast is not provided", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "s1", type: "system_notice", level: "error", message: "oops" },
  ];
  render(<MessageList items={items} />);
  expect(screen.queryByTestId("system-message-retry")).toBeNull();
});

test("untouched memoized rows skip re-render when another item streams", () => {
  const onEdit = vi.fn();
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "a1", type: "assistant_message", content: "strea", streaming: true },
  ];
  const { rerender } = render(<MessageList items={items} onEdit={onEdit} />);
  const userRenders = vi.mocked(stripCoddyAttachmentsForUserDisplay).mock.calls
    .length;
  expect(userRenders).toBeGreaterThan(0);

  // Streaming delta: only the assistant item gets a new object reference.
  const next: TranscriptItem[] = [
    items[0]!,
    { id: "a1", type: "assistant_message", content: "streaming", streaming: true },
  ];
  rerender(<MessageList items={next} onEdit={onEdit} />);
  expect(
    vi.mocked(stripCoddyAttachmentsForUserDisplay).mock.calls.length,
  ).toBe(userRenders);
  expect(screen.getByText("streaming")).toBeInTheDocument();
});

test("compaction summary renders as a foldout, not a user bubble", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "c1", type: "compaction", summary: "Key facts preserved here." },
  ];
  render(<MessageList items={items} />);
  expect(screen.getByText("context compacted")).toBeInTheDocument();
  expect(
    screen.getByLabelText("Context compacted summary"),
  ).toBeInTheDocument();
  expect(screen.getByText(/Key facts preserved here/)).toBeInTheDocument();
});

test("only the answer that hands the turn back carries an action row", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    {
      id: "a1",
      type: "assistant_message",
      content: "Reading the file.",
      createdAtUtc: "2026-01-01T10:00:00.000Z",
    },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read",
      kind: "read",
      status: "completed",
      argsText: '{"path":"a.txt"}',
      resultText: "OK",
    },
    {
      id: "a2",
      type: "assistant_message",
      content: "Done.",
      createdAtUtc: "2026-01-01T10:00:04.000Z",
    },
  ];

  const { container } = render(<MessageList items={items} />);

  expect(screen.getAllByTestId("assistant-message-copy")).toHaveLength(1);
  expect(container.querySelectorAll(".msg-assistant-foot")).toHaveLength(1);
  // The copy button and the minute both belong to the closing answer, not to the
  // answers the turn left behind between tool calls.
  const closing = screen
    .getByText("Done.")
    .closest<HTMLElement>(".msg-assistant")!;
  expect(within(closing).getByTestId("assistant-message-copy")).toBeTruthy();
  const intermediate = screen
    .getByText("Reading the file.")
    .closest(".msg-assistant")!;
  expect(intermediate.querySelector(".msg-assistant-foot")).toBeNull();
});

test("a turn still in flight offers no copy control on its last answer", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    { id: "a1", type: "assistant_message", content: "Working on it." },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read",
      kind: "read",
      status: "in_progress",
      argsText: '{"path":"a.txt"}',
    },
  ];

  const { container } = render(<MessageList items={items} generating />);

  expect(screen.queryByTestId("assistant-message-copy")).toBeNull();
  expect(container.querySelector(".msg-assistant-foot")).toBeNull();
});

test("every finished turn keeps the action row on the answer that closed it", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "First" },
    { id: "a1", type: "assistant_message", content: "Looking." },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read",
      kind: "read",
      status: "completed",
      argsText: '{"path":"a.txt"}',
      resultText: "OK",
    },
    { id: "a2", type: "assistant_message", content: "First answer." },
    { id: "u2", type: "user_message", content: "Second" },
    { id: "a3", type: "assistant_message", content: "Second answer." },
  ];

  render(<MessageList items={items} />);

  // One per turn: the older answer stays copyable, the mid-turn one does not.
  expect(screen.getAllByTestId("assistant-message-copy")).toHaveLength(2);
  expect(
    screen.getByText("First answer.").closest(".msg-assistant")!
      .querySelector(".msg-assistant-foot"),
  ).not.toBeNull();
  expect(
    screen.getByText("Looking.").closest(".msg-assistant")!
      .querySelector(".msg-assistant-foot"),
  ).toBeNull();
});

test("a new turn does not strip the action row off the previous answer", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "First" },
    { id: "a1", type: "assistant_message", content: "First answer." },
    { id: "u2", type: "user_message", content: "Second" },
  ];

  render(<MessageList items={items} generating />);

  expect(
    screen.getByText("First answer.").closest(".msg-assistant")!
      .querySelector(".msg-assistant-foot"),
  ).not.toBeNull();
});

// An assistant row holding nothing but whitespace is zero pixels tall and still
// takes the column's gap, which reads as a hole between the rows around it.
test("a whitespace-only assistant row takes no place in the transcript", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "t1", type: "tool_call", toolCallId: "tc1", title: "read", status: "completed" },
    { id: "a1", type: "assistant_message", content: "\n\n", streaming: true },
    { id: "t2", type: "tool_call", toolCallId: "tc2", title: "read", status: "completed" },
  ];
  const { container } = render(<MessageList items={items} />);
  expect(container.querySelectorAll(".msg-assistant-stack")).toHaveLength(0);
});
