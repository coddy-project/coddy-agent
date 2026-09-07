import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { ToolCallMessage } from "./ToolCallMessage";

afterEach(cleanup);

const args = JSON.stringify({
  agent: "explore",
  description: "Investigate tests and agents",
  prompt: "Inspect the project.\n1. Find tests.\n2. Describe the agents.",
  timeout_seconds: 120,
});

test("spawn_agent displays agent identity, description, prompt and timeout", () => {
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
  expect(screen.getByText("explore")).toBeInTheDocument();
  expect(screen.getByText("Investigate tests and agents")).toBeInTheDocument();
  expect(screen.getByLabelText("Agent prompt").textContent).toBe(
    JSON.parse(args).prompt,
  );
  expect(screen.getByText("Timeout 120s")).toBeInTheDocument();
  expect(screen.queryByLabelText("Tool arguments")).toBeNull();
  expect(screen.getByLabelText("Tool result")).toHaveTextContent(
    "Found 12 tests.",
  );
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
  expect(screen.getByLabelText("Tool arguments")).toBeInTheDocument();
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
    expect(screen.getByLabelText("Tool arguments")).toBeInTheDocument();
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
  expect(screen.getByLabelText("Tool arguments")).toHaveTextContent(
    "truncated...",
  );
});
