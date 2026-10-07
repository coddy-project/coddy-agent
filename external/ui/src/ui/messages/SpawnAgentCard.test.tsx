import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ToolCallMessage } from "./ToolCallMessage";
import { initLocale } from "../i18n/i18n";
import type { BackgroundTask } from "../tasks/types";

afterEach(() => {
  cleanup();
  initLocale("en");
});

const args = JSON.stringify({
  agent: "explore",
  description: "Investigate tests and agents",
  prompt: "Inspect the project.\n1. Find tests.\n2. Describe the agents.",
  model: "neuraldeep/qwen3.8-27b",
  reasoning: "high",
  timeout_seconds: 120,
});

function agentTask(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_spawn",
    session_id: "parent",
    kind: "agent",
    label: "agent explore: inspect the project",
    agent: { name: "explore" },
    status: "running",
    started_at: "2026-10-01T12:00:00Z",
    timeout_seconds: 120,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 0,
    overdue: false,
    running: true,
    ...over,
  };
}

test("spawn_agent displays identity and bottom model/reasoning/timeout metadata", () => {
  render(
    <ToolCallMessage
      toolCallId="spawn-1"
      title="spawn_agent"
      status="completed"
      argsText={args}
      resultText="Found 12 tests."
      durationMs={77000}
    />,
  );
  expect(screen.getByLabelText("Agent details")).toBeInTheDocument();
  // The agent is named twice on purpose: on the collapsed summary row and on the card.
  expect(screen.getByTestId("tool-summary-target")).toHaveTextContent(
    "explore",
  );
  expect(
    screen.getByLabelText("Agent details").querySelector(".spawn-agent-name"),
  ).toHaveTextContent("explore");
  expect(screen.getByText("Investigate tests and agents")).toBeInTheDocument();
  expect(screen.getByLabelText("Agent prompt").textContent).toBe(
    JSON.parse(args).prompt,
  );
  const meta = screen.getByTestId("spawn-agent-meta");
  expect(meta).toHaveTextContent("neuraldeep/qwen3.8-27b");
  expect(meta).toHaveTextContent("High");
  expect(meta).toHaveTextContent("Timeout 120s");
  expect(meta.querySelector("svg")).toBeNull();
  expect(screen.queryByTestId("permission-preview-viewport")).toBeNull();
  expect(screen.getByLabelText("Tool result")).toHaveTextContent(
    "Found 12 tests.",
  );
});

test("spawn agent prompt expands through the shared localized overflow control", () => {
  render(
    <ToolCallMessage
      toolCallId="spawn-prompt"
      title="spawn_agent"
      status="completed"
      argsText={JSON.stringify({
        agent: "explore",
        prompt: "First line\n".repeat(40),
      })}
    />,
  );

  const prompt = screen.getByLabelText("Agent prompt");
  expect(prompt).toHaveClass("spawn-agent-prompt--collapsed");
  expect(screen.getByRole("button", { name: "More…" })).toHaveClass(
    "tool-overflow-toggle",
  );
  expect(prompt).toHaveAttribute("aria-expanded", "false");

  fireEvent.click(screen.getByRole("button", { name: "More…" }));
  expect(prompt).toHaveClass("spawn-agent-prompt--scroll");
  expect(prompt).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByRole("button", { name: "Less" })).toHaveClass(
    "tool-overflow-toggle",
  );

  fireEvent.click(screen.getByRole("button", { name: "Less" }));
  expect(prompt).toHaveClass("spawn-agent-prompt--collapsed");
  expect(prompt).toHaveAttribute("aria-expanded", "false");
});

test("spawn agent transcript action follows the mapped task child session", () => {
  const onOpenSession = vi.fn();
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="spawn-transcript"
      title="spawn_agent"
      status="completed"
      argsText={args}
      onOpenSession={onOpenSession}
    />,
  );
  expect(screen.queryByTestId("spawn-agent-open-transcript")).toBeNull();

  rerender(
    <ToolCallMessage
      toolCallId="spawn-transcript"
      title="spawn_agent"
      status="completed"
      argsText={args}
      backgroundTask={agentTask()}
      onOpenSession={onOpenSession}
    />,
  );
  const pending = screen.getByTestId("spawn-agent-open-transcript");
  expect(pending).toBeDisabled();
  expect(pending).toHaveAttribute(
    "title",
    "The child session is not known yet",
  );

  rerender(
    <ToolCallMessage
      toolCallId="spawn-transcript"
      title="spawn_agent"
      status="completed"
      argsText={args}
      backgroundTask={agentTask({
        agent: { name: "explore", session_id: "sess_child" },
      })}
      onOpenSession={onOpenSession}
    />,
  );
  const ready = screen.getByTestId("spawn-agent-open-transcript");
  expect(ready).toBeEnabled();
  fireEvent.click(ready);
  expect(onOpenSession).toHaveBeenCalledWith("sess_child");
});

test("restored truncated spawn args are fetched once and replaced with the card", async () => {
  const fetchFull = vi.fn().mockResolvedValue(undefined);
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="spawn-2"
      title="spawn_agent"
      status="completed"
      argsText={'{"agent":"explore","prompt":"Inspect...'}
      onFetchToolCallFull={fetchFull}
    />,
  );
  await waitFor(() => expect(fetchFull).toHaveBeenCalledOnce());
  expect(screen.getByTestId("permission-preview-viewport")).toBeInTheDocument();
  rerender(
    <ToolCallMessage
      toolCallId="spawn-2"
      title="spawn_agent"
      status="completed"
      argsText={args}
      onFetchToolCallFull={fetchFull}
    />,
  );
  expect(screen.getByLabelText("Agent prompt")).toBeInTheDocument();
  expect(fetchFull).toHaveBeenCalledOnce();
});

test.each(["null", "[]", "{", '{"agent":123,"prompt":{}}'])(
  "invalid spawn args remain readable: %s",
  (argsText) => {
    render(
      <ToolCallMessage
        toolCallId="bad"
        title="spawn_agent"
        status="failed"
        argsText={argsText}
      />,
    );
    expect(screen.queryByLabelText("Agent details")).toBeNull();
    expect(
      screen.getByTestId("permission-preview-viewport"),
    ).toBeInTheDocument();
  },
);

test.each([undefined, -1, 0, "120"])(
  "invalid or absent timeout is omitted: %s",
  (timeout_seconds) => {
    render(
      <ToolCallMessage
        toolCallId="optional"
        kind="spawn_agent"
        status="in_progress"
        argsText={JSON.stringify({
          agent: "explore",
          prompt: "<script>alert(1)</script>",
          timeout_seconds,
        })}
      />,
    );
    expect(screen.getByLabelText("Agent prompt").textContent).toBe(
      "<script>alert(1)</script>",
    );
    expect(screen.queryByText(/Timeout/)).toBeNull();
    expect(
      screen.queryByLabelText("Agent details")?.querySelector("script"),
    ).toBeNull();
  },
);

test("failed automatic argument fetch preserves the fallback", async () => {
  const fetchFull = vi.fn().mockRejectedValue(new Error("offline"));
  render(
    <ToolCallMessage
      toolCallId="offline"
      title="spawn_agent"
      status="completed"
      argsText="truncated..."
      onFetchToolCallFull={fetchFull}
    />,
  );
  await waitFor(() => expect(fetchFull).toHaveBeenCalledOnce());
  expect(screen.getByTestId("permission-preview-viewport")).toHaveTextContent(
    "truncated...",
  );
});

test("spawn agent labels follow the Russian locale", () => {
  initLocale("ru");
  render(
    <ToolCallMessage
      toolCallId="ru"
      title="spawn_agent"
      status="completed"
      argsText={args}
    />,
  );
  expect(screen.getByLabelText("Сведения об агенте")).toBeInTheDocument();
  expect(screen.getByLabelText("Промпт агента")).toHaveTextContent(
    "Inspect the project.",
  );
  expect(screen.getByText("Таймаут 120 с")).toHaveAttribute(
    "title",
    "Максимальное время работы агента",
  );
});
