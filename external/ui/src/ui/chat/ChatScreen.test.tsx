import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ChatScreen } from "./ChatScreen";
import type { BackgroundTask } from "../tasks/types";

afterEach(() => cleanup());

test("new background permission prompts follow the reader at the bottom, but polling does not", () => {
  const common = {
    title: "Audit",
    sessionId: "sess_parent",
    heroAccentVerb: "know" as const,
    heroComposerFocusEpoch: 0,
    onTitleSave: () => {},
    items: [{ type: "user_message" as const, id: "u1", content: "audit" }],
    draft: "",
    tokenUsage: null,
    mode: "agent",
    modes: ["agent"],
    onModeChange: () => {},
    onDraftChange: () => {},
    onSend: () => {},
  };
  const task: BackgroundTask = {
    id: "bg_1",
    session_id: "sess_parent",
    kind: "agent",
    label: "writer",
    status: "running",
    started_at: "2026-09-14T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 1,
    overdue: false,
    running: true,
    agent: { name: "writer", session_id: "sess_child" },
    pending_permission: {
      sessionId: "sess_child",
      toolCall: { toolCallId: "call_1", title: "Run: run_command" },
      options: [],
    },
  };
  const { container, rerender } = render(
    <ChatScreen {...common} backgroundTasks={[]} />,
  );
  const scroller = container.querySelector("#messages") as HTMLElement;
  Object.defineProperties(scroller, {
    scrollHeight: { configurable: true, value: 2000 },
    clientHeight: { configurable: true, value: 500 },
  });
  scroller.scrollTop = 1500;
  fireEvent.scroll(scroller);
  Object.defineProperty(scroller, "scrollHeight", {
    configurable: true,
    value: 2300,
  });
  rerender(<ChatScreen {...common} backgroundTasks={[task]} />);
  expect(scroller.scrollTop).toBe(2300);
  // A freshly fetched row with the same prompt must not move the viewport.
  scroller.scrollTop = 1800;
  rerender(
    <ChatScreen
      {...common}
      backgroundTasks={[{ ...task, elapsed_seconds: 2 }]}
    />,
  );
  expect(scroller.scrollTop).toBe(1800);
  // A reader inspecting older messages keeps their position on a new prompt.
  scroller.scrollTop = 300;
  fireEvent.scroll(scroller);
  rerender(
    <ChatScreen
      {...common}
      backgroundTasks={[
        {
          ...task,
          pending_permission: {
            ...task.pending_permission!,
            toolCall: { toolCallId: "call_2" },
          },
        },
      ]}
    />,
  );
  expect(scroller.scrollTop).toBe(300);
});

test("empty hero shows headline with accent span", () => {
  const { getByTestId, getByRole } = render(
    <ChatScreen
      title=""
      sessionId=""
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  expect(getByRole("heading", { level: 1 })).toHaveTextContent(
    "What do you want to know?",
  );
  expect(getByTestId("hero-title-accent")).toHaveTextContent("know");
  expect(getByRole("textbox")).toHaveFocus();
});

test("active chat wraps title in chat-title-column aligned with composer column", () => {
  const { container } = render(
    <ChatScreen
      title="Hi"
      sessionId="s1"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "x" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  const col = container.querySelector(".chat-title-column");
  expect(col).toBeTruthy();
  expect(col?.querySelector(".chat-header")).toBeTruthy();
});

test("disabled attachments survive the empty-to-active composer transition", async () => {
  const common = {
    title: "",
    sessionId: "",
    heroAccentVerb: "know" as const,
    heroComposerFocusEpoch: 0,
    onTitleSave: () => {},
    draft: "",
    tokenUsage: null,
    mode: "agent",
    modes: ["agent", "plan"],
    llmModels: ["openai/vision", "openai/text"],
    llmModel: "openai/vision",
    onLlmModelChange: () => {},
    onModeChange: () => {},
    onDraftChange: () => {},
    onSend: () => {},
  };
  const { rerender } = render(
    <ChatScreen {...common} items={[]} llmModelMultimodal={true} />,
  );
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: {
      files: [new File(["img"], "photo.png", { type: "image/png" })],
    },
  });
  await waitFor(() => screen.getByText("photo.png"));

  rerender(
    <ChatScreen
      {...common}
      sessionId="s1"
      items={[{ type: "user_message", id: "1", content: "hello" }]}
      llmModel="openai/text"
      llmModelMultimodal={false}
    />,
  );

  expect(
    screen.getByText("photo.png").closest(".composer-attachment-chip"),
  ).toHaveClass("composer-attachment-chip--disabled");
});

const childTranscript = {
  parentSessionId: "s_parent",
  name: "explore",
  taskId: "bg_3",
};

test("a subagent transcript replaces the docked composer with a read-only notice", () => {
  const onOpenSession = vi.fn();
  const { container } = render(
    <ChatScreen
      title="agent explore"
      sessionId="sess_0a1b2c"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "survey the repo" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      subagentTranscript={childTranscript}
      onOpenSession={onOpenSession}
    />,
  );

  expect(container.querySelector(".composer-card")).toBeNull();
  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "Read-only transcript of subagent explore",
  );
  fireEvent.click(screen.getByTestId("subagent-readonly-parent-link"));
  expect(onOpenSession).toHaveBeenCalledWith("s_parent");
});

test("the notice also takes the hero composer's slot on an empty child transcript", () => {
  const { container } = render(
    <ChatScreen
      title=""
      sessionId="sess_0a1b2c"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      subagentTranscript={childTranscript}
    />,
  );

  expect(container.querySelector(".composer-card")).toBeNull();
  expect(screen.getByTestId("subagent-readonly-notice")).toBeInTheDocument();
});

// A background subagent asks after its parent turn ended. The chat of that parent
// session is where the person reads the conversation, so the prompt waits at the
// end of it, inside the transcript column - not in a panel that is closed by
// default - and answering it re-reads the task rows.
test("a background subagent's prompt waits at the end of its parent chat", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockImplementation(() => Promise.resolve({ ok: true, status: 204 })),
  );
  const refreshed = vi.fn();
  const { container } = render(
    <ChatScreen
      title="Audit"
      sessionId="sess_parent"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "audit it" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      backgroundTasks={[
        {
          id: "bg_1",
          session_id: "sess_parent",
          kind: "agent",
          label: "agent writer: audit",
          status: "running",
          started_at: "2026-09-14T10:00:00Z",
          timeout_seconds: 900,
          output_bytes: 0,
          output_truncated: false,
          elapsed_seconds: 5,
          overdue: false,
          running: true,
          agent: { name: "writer", session_id: "sess_child" },
          pending_permission: {
            sessionId: "sess_child",
            toolCall: {
              toolCallId: "call_7",
              title: "[subagent writer] Run: run_command",
            },
            options: [
              { optionId: "allow", name: "Allow once", kind: "allow_once" },
              { optionId: "reject", name: "Reject", kind: "reject_once" },
            ],
            agent_name: "writer",
          },
        },
      ]}
      onOpenBackgroundTasks={() => {}}
      onBackgroundTasksChanged={refreshed}
    />,
  );

  const card = screen.getByTestId("subagent-permission-bg_1");
  expect(container.querySelector(".messages-inner")?.contains(card)).toBe(true);
  fireEvent.click(screen.getByTestId("subagent-permission-reject-bg_1"));
  await waitFor(() => expect(refreshed).toHaveBeenCalledTimes(1));
  vi.unstubAllGlobals();
});
