import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * The session goal in the shell (chat/goal.ts, DESIGN.md "Session goal"): the
 * mark mirrors the goal the messages read, the turn stream and the events
 * stream deliver, the popover's actions reach the goal's routes or go out as
 * /goal prompts, and the turns the supervisor started read as goal rows.
 */

const SID = "sess_goal";
const OBJECTIVE = "Make every parser test pass";

type Goal = Record<string, unknown> | null;

let goal: Goal;
let version: number;
let posted: Array<{ input?: string }>;
let goalCalls: Array<{ method: string; body: unknown }>;

function activeGoal(over: Record<string, unknown> = {}): Goal {
  return {
    id: "goal_1",
    objective: OBJECTIVE,
    status: "active",
    setAt: "2026-10-07T10:00:00Z",
    continuations: 1,
    maxContinuations: 10,
    checks: 1,
    activeMs: 30_000,
    tokensUsed: 1_500,
    lastCheck: {
      verdict: "not_met",
      reason: "the lexer tests fail",
      remaining: ["fix the lexer"],
    },
    ...over,
  };
}

const envelope = () => ({
  object: "coddy.session_goal",
  sessionId: SID,
  goal,
  version: ++version,
  notice: "",
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function emptyStream() {
  return new Response(
    new ReadableStream<Uint8Array>({ start: (c) => c.close() }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}

let eventsController: ReadableStreamDefaultController<Uint8Array> | null = null;
function eventsStream() {
  return new Response(
    new ReadableStream<Uint8Array>({
      start: (c) => {
        eventsController = c;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}

const MESSAGES = [
  { role: "user", content: "hello" },
  { role: "assistant", content: "Hi, what should I do?" },
  {
    role: "user",
    content: "You are working toward a goal. The kickoff instruction.",
    goal_turn: { kind: "kickoff", limit: 10, objective: OBJECTIVE },
  },
  { role: "assistant", content: "Fixed the parser." },
  {
    role: "user",
    content: "Continue working toward the goal. The continuation instruction.",
    goal_turn: {
      kind: "continue",
      index: 1,
      limit: 10,
      objective: OBJECTIVE,
      reason: "the lexer tests fail",
      remaining: ["fix the lexer"],
    },
  },
  { role: "assistant", content: "Fixed the lexer too." },
];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return eventsStream();
    if (path === "/v1/responses") {
      posted.push(JSON.parse(String(init?.body)));
      return emptyStream();
    }
    if (path === "/v1/models") {
      return json({
        data: [{ id: "agent", owned_by: "coddy", max_context_tokens: 128000 }],
      });
    }
    if (path.startsWith("/coddy/sessions?")) {
      return json({ sessions: [{ id: SID, title: "goal chat" }] });
    }
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/messages") {
        return json({ messages: MESSAGES, goal: envelope() });
      }
      if (suffix === "/goal") {
        const method = init?.method ?? "GET";
        const body = init?.body ? JSON.parse(String(init.body)) : undefined;
        goalCalls.push({ method, body });
        if (method === "PATCH" && goal) goal = { ...goal, status: "paused" };
        if (method === "DELETE") goal = null;
        return json(envelope());
      }
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: false });
      if (!suffix) return json({});
    }
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path === "/coddy/workspace/context")
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  },
);

beforeEach(() => {
  eventsController = null;
  goal = activeGoal();
  version = 100;
  posted = [];
  goalCalls = [];
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
});

const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));
const mark = () => screen.getByTestId("composer-goal");

async function mountSession() {
  history.replaceState(null, "", `/#/s/${SID}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText("Fixed the lexer too.");
  await settle();
}

async function pushEvent(name: string, data: unknown) {
  await act(async () => {
    eventsController?.enqueue(
      new TextEncoder().encode(
        `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`,
      ),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
}

test("the goal of the session read shows on the mark, and its turns as goal rows", async () => {
  await mountSession();
  expect(mark()).toHaveAttribute("aria-label", "Goal: active");
  expect(screen.getByTestId("composer-goal-tip")).toHaveTextContent(OBJECTIVE);

  const rows = screen.getAllByTestId("goal-turn-row");
  expect(rows.map((r) => r.dataset.goalKind)).toEqual(["kickoff", "continue"]);
  expect(rows[0]).toHaveTextContent(`Goal set: ${OBJECTIVE}`);
  expect(rows[1]).toHaveTextContent(
    "Goal continuation 1 of 10: the lexer tests fail",
  );
  // The instructions the model read are no bubbles: only "hello" is.
  expect(document.querySelectorAll(".msg-user")).toHaveLength(1);
  expect(screen.queryByText(/kickoff instruction/)).toBeNull();
  expect(screen.queryByText(/continuation instruction/)).toBeNull();
});

test("Pause reaches PATCH /goal and the answer moves the mark", async () => {
  await mountSession();
  fireEvent.click(mark());
  await act(async () => {
    fireEvent.click(screen.getByTestId("goal-pause"));
  });
  await waitFor(() =>
    expect(mark()).toHaveAttribute("aria-label", "Goal: paused"),
  );
  expect(goalCalls).toEqual([{ method: "PATCH", body: { status: "paused" } }]);
  expect(posted).toEqual([]);
});

test("Clear asks, then reaches DELETE /goal, and the mark goes", async () => {
  await mountSession();
  fireEvent.click(mark());
  fireEvent.click(screen.getByTestId("goal-clear"));
  const dialog = await screen.findByTestId("app-confirm-dialog");
  expect(goalCalls).toEqual([]);
  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Clear" }));
  });
  await waitFor(() => expect(screen.queryByTestId("composer-goal")).toBeNull());
  expect(goalCalls).toEqual([{ method: "DELETE", body: undefined }]);
});

test("Resume sends /goal resume as a prompt and leaves the draft alone", async () => {
  goal = activeGoal({ status: "paused", statusReason: "usage limit" });
  await mountSession();
  const field = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(field, { target: { value: "a draft in progress" } });
  fireEvent.click(mark());
  fireEvent.click(screen.getByTestId("goal-resume"));
  await waitFor(() => expect(posted.length).toBe(1));
  expect(posted[0]!.input).toBe("/goal resume");
  expect((field as HTMLTextAreaElement).value).toBe("a draft in progress");
  expect(goalCalls).toEqual([]);
});

test("the events stream moves the mark; an older or another session's snapshot does not", async () => {
  await mountSession();
  await pushEvent("session_goal", {
    object: "coddy.session_goal",
    sessionId: SID,
    goal: activeGoal({ status: "blocked", statusReason: "Which branch?" }),
    version: 500,
    notice: "Goal blocked: Which branch?",
  });
  await waitFor(() =>
    expect(mark()).toHaveAttribute("aria-label", "Goal: needs you"),
  );
  await pushEvent("session_goal", {
    object: "coddy.session_goal",
    sessionId: SID,
    goal: activeGoal({ status: "complete" }),
    version: 400,
    notice: "",
  });
  await pushEvent("session_goal", {
    object: "coddy.session_goal",
    sessionId: "sess_other",
    goal: null,
    version: 900,
    notice: "",
  });
  await settle();
  expect(mark()).toHaveAttribute("aria-label", "Goal: needs you");
  await pushEvent("session_goal", {
    object: "coddy.session_goal",
    sessionId: SID,
    goal: null,
    version: 501,
    notice: "Goal cleared",
  });
  await waitFor(() => expect(screen.queryByTestId("composer-goal")).toBeNull());
});

test("a bare /goal opens the goal popover and sends nothing", async () => {
  await mountSession();
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "/goal" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(await screen.findByTestId("goal-popover")).toBeInTheDocument();
  await settle();
  expect(posted).toEqual([]);
  expect(
    (screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement)
      .value,
  ).toBe("");
});

test("a new chat forgets the goal version, so a restarted server's snapshot is read on return", async () => {
  await mountSession();
  await pushEvent("session_goal", {
    object: "coddy.session_goal",
    sessionId: SID,
    goal: activeGoal({ status: "blocked", statusReason: "Which branch?" }),
    version: 5000,
  });
  await waitFor(() =>
    expect(mark()).toHaveAttribute("aria-label", "Goal: needs you"),
  );

  // The server restarts (its version counter starts over) while the
  // operator is on a new chat, then the operator comes back.
  await act(async () => {
    fireEvent.click(screen.getByTestId("nav-home"));
  });
  await settle();
  expect(screen.queryByTestId("composer-goal")).toBeNull();
  version = 1;
  goal = activeGoal({
    status: "paused",
    statusReason: "paused by the operator",
  });
  await act(async () => {
    history.replaceState(null, "", `/#/s/${SID}?r=1`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await screen.findByText("Fixed the lexer too.");
  await settle();
  await waitFor(() =>
    expect(mark()).toHaveAttribute("aria-label", "Goal: paused"),
  );
});
