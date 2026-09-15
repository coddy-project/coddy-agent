import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { initLocale } from "../i18n/i18n";
import type { BackgroundTask } from "../tasks/types";
import {
  SubagentPermissionCards,
  stripSubagentTitlePrefix,
} from "./SubagentPermissionCard";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.unstubAllGlobals();
});

function task(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "sess_parent",
    kind: "agent",
    label: "agent writer: audit the layout",
    status: "running",
    started_at: "2026-09-14T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 12,
    overdue: false,
    running: true,
    agent: { name: "writer", session_id: "sess_child" },
    ...over,
  };
}

const pending = {
  sessionId: "sess_child",
  toolCall: {
    toolCallId: "call_7",
    title: "[subagent writer] Run: run_command",
    kind: "execute",
    status: "pending",
    content: [
      {
        type: "content",
        content: { type: "text", text: "echo checked 3 modules" },
      },
    ],
  },
  options: [
    { optionId: "allow", name: "Allow once", kind: "allow_once" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
  agent_name: "writer",
  asked_at: "2026-09-14T10:00:05Z",
};

test("a background subagent's prompt is answered in the parent chat against the child session", async () => {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init: RequestInit) => {
      calls.push({ url: String(url), init });
      return Promise.resolve({ ok: true, status: 204 });
    }),
  );
  const answered = vi.fn();
  render(
    <SubagentPermissionCards
      tasks={[task({ pending_permission: pending })]}
      onAnswered={answered}
    />,
  );

  const card = screen.getByTestId("subagent-permission-bg_1");
  expect(card).toHaveTextContent('The subagent "writer" asks for permission');
  fireEvent.click(screen.getByTestId("subagent-permission-allow-bg_1"));

  await waitFor(() => expect(answered).toHaveBeenCalledTimes(1));
  expect(calls).toHaveLength(1);
  // The child session is the one waiting, so the answer is addressed to it.
  expect(calls[0]?.url).toBe("/coddy/sessions/sess_child/permission");
  expect(calls[0]?.init.method).toBe("POST");
  expect(
    (calls[0]?.init.headers as Record<string, string>)["X-Coddy-Session-ID"],
  ).toBe("sess_child");
  expect(JSON.parse(String(calls[0]?.init.body))).toEqual({
    toolCallId: "call_7",
    optionId: "allow",
  });
});

test("only a task that is really waiting gets a card, oldest question first", () => {
  render(
    <SubagentPermissionCards
      tasks={[
        task({
          id: "bg_late",
          pending_permission: { ...pending, asked_at: "2026-09-14T10:05:00Z" },
        }),
        task({ id: "bg_idle" }),
        task({
          id: "bg_done",
          running: false,
          status: "succeeded",
          pending_permission: pending,
        }),
        task({ id: "bg_early", pending_permission: pending }),
      ]}
      onAnswered={() => {}}
    />,
  );
  const cards = screen
    .getAllByTestId(/^subagent-permission-bg_/)
    .map((el) => el.getAttribute("data-testid"));
  expect(cards).toEqual([
    "subagent-permission-bg_early",
    "subagent-permission-bg_late",
  ]);
});

test("nothing is rendered while no subagent waits", () => {
  const { container } = render(
    <SubagentPermissionCards tasks={[task()]} onAnswered={() => {}} />,
  );
  expect(container).toBeEmptyDOMElement();
});

test("the relay's subagent prefix is dropped from a title and nothing else is", () => {
  expect(stripSubagentTitlePrefix("[subagent writer] Run: run_command")).toBe(
    "Run: run_command",
  );
  expect(stripSubagentTitlePrefix("Run: run_command")).toBe("Run: run_command");
  expect(stripSubagentTitlePrefix(undefined)).toBe("");
});

test("the card reads in Russian", () => {
  initLocale("ru");
  render(
    <SubagentPermissionCards
      tasks={[task({ pending_permission: pending })]}
      onAnswered={() => {}}
    />,
  );
  expect(screen.getByTestId("subagent-permission-bg_1")).toHaveTextContent(
    "Субагент «writer» просит разрешение",
  );
});
