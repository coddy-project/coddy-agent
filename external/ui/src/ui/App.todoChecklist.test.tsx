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
 * The todo checklist under the transcript (DESIGN.md "Todo checklist under the
 * transcript"): the transcript read brings the session's list, the plan frames
 * of a running turn move it, and a finished list leaves once the turn is over.
 */

const SID = "sess_todo";

type Entry = { content: string; status: string };

const entries = (...statuses: string[]): Entry[] =>
  ["Read the failing test", "Fix the parser", "Run the whole suite"].map(
    (content, i) => ({ content, status: statuses[i]! }),
  );

let todoPlan: Entry[] | null;

class TurnStream {
  private controller!: ReadableStreamDefaultController<Uint8Array>;
  readonly response = new Response(
    new ReadableStream<Uint8Array>({
      start: (c) => {
        this.controller = c;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
  frame(event: string, data: unknown) {
    this.controller.enqueue(
      new TextEncoder().encode(
        `${event ? `event: ${event}\n` : ""}data: ${JSON.stringify(data)}\n\n`,
      ),
    );
  }
  running = true;
  end() {
    this.running = false;
    this.controller.enqueue(new TextEncoder().encode("data: [DONE]\n\n"));
    this.controller.close();
  }
}

let turn: TurnStream | null;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const openStream = () =>
  new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
    headers: { "Content-Type": "text/event-stream" },
  });

const MESSAGES = [
  { role: "user", content: "fix the parser" },
  { role: "assistant", content: "Planned the work." },
];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return openStream();
    if (path === "/v1/responses") {
      turn = new TurnStream();
      return turn.response;
    }
    if (path === "/v1/models") {
      return json({
        data: [{ id: "agent", owned_by: "coddy", max_context_tokens: 128000 }],
      });
    }
    if (path.startsWith("/coddy/sessions?")) {
      return json({ sessions: [{ id: SID, title: "todo chat" }] });
    }
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/messages") {
        return json({
          messages: MESSAGES,
          ...(todoPlan ? { todoPlan } : {}),
        });
      }
      if (suffix === "/composer-stream") return openStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: turn?.running ?? false });
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
  todoPlan = entries("completed", "in_progress", "pending");
  turn = null;
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
const checklist = () => screen.queryByTestId("todo-checklist");
const rowLabels = () =>
  within(screen.getByTestId("todo-checklist"))
    .getAllByRole("listitem")
    .map((row) => row.getAttribute("aria-label"));
const inProgress = () =>
  Array.from(document.querySelectorAll(".todo-checklist-row--in_progress")).map(
    (row) => row.textContent,
  );

async function mountSession() {
  history.replaceState(null, "", `/#/s/${SID}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText("Planned the work.");
  await settle();
}

async function startTurn() {
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "go on" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(turn).not.toBeNull());
  await settle();
}

async function planFrame(next: Entry[]) {
  await act(async () => {
    turn!.frame("plan", { sessionUpdate: "plan", entries: next });
    await new Promise((r) => setTimeout(r, 0));
  });
}

test("opening a conversation shows its checklist under the newest message", async () => {
  await mountSession();
  const list = checklist();
  expect(list).not.toBeNull();
  expect(
    within(list!).getByRole("list", { name: "Todo list, 1 of 3 done" }),
  ).toBeInTheDocument();
  expect(rowLabels()).toEqual([
    "Completed: Read the failing test",
    "In progress: Fix the parser",
    "Pending: Run the whole suite",
  ]);
  // The newest message is right above it.
  expect(list!.previousElementSibling).toHaveTextContent("Planned the work.");
  // The item the agent is on is marked as in progress.
  expect(inProgress()).toEqual(["Fix the parser"]);
});

test("the plan frames of a running turn move the checklist, its item in progress with it", async () => {
  await mountSession();
  await startTurn();
  await waitFor(() => expect(inProgress()).toEqual(["Fix the parser"]));

  await planFrame(entries("completed", "completed", "in_progress"));
  await waitFor(() =>
    expect(rowLabels()).toEqual([
      "Completed: Read the failing test",
      "Completed: Fix the parser",
      "In progress: Run the whole suite",
    ]),
  );
  expect(inProgress()).toEqual(["Run the whole suite"]);
  expect(
    screen.getByRole("list", { name: "Todo list, 2 of 3 done" }),
  ).toBeInTheDocument();
});

test("a finished checklist stays while its turn runs and leaves once the turn is over", async () => {
  await mountSession();
  await startTurn();
  const done = entries("completed", "completed", "completed");
  await planFrame(done);
  await waitFor(() =>
    expect(
      screen.getByRole("list", { name: "Todo list, 3 of 3 done" }),
    ).toBeInTheDocument(),
  );

  // The server keeps the finished list until it is replaced or archived;
  // the read after the turn brings it back, and still nothing shows.
  todoPlan = done;
  await act(async () => {
    turn!.frame("", { choices: [{ delta: { content: "All three done." } }] });
    turn!.end();
    await new Promise((r) => setTimeout(r, 0));
  });
  await waitFor(() => expect(checklist()).toBeNull());
  await settle();
  expect(checklist()).toBeNull();
});

test("a list already finished when a turn starts does not come back during it", async () => {
  // The agent ticked every item and left the list; the server keeps it.
  todoPlan = entries("completed", "completed", "completed");
  await mountSession();
  expect(checklist()).toBeNull();

  await startTurn();
  // The turn opens by sending the list it has, still finished.
  await planFrame(entries("completed", "completed", "completed"));
  await settle();
  expect(checklist()).toBeNull();

  // A list the turn writes shows, and stays once the turn finishes it.
  await planFrame(entries("in_progress", "pending", "pending"));
  await waitFor(() => expect(inProgress()).toEqual(["Read the failing test"]));
  await planFrame(entries("completed", "completed", "completed"));
  await waitFor(() =>
    expect(
      screen.getByRole("list", { name: "Todo list, 3 of 3 done" }),
    ).toBeInTheDocument(),
  );
});
