import React from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import type { TranscriptItem } from "./chat/types";
import type { SessionRow } from "./sessions/types";

const PARENT = "sess_parent";
const CHILD = "sess_child";
const NEW_SESSION = "sess_new";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

type ChatProps = {
  sessionId?: string;
  sessionLoading?: boolean;
  items?: TranscriptItem[];
  onOpenSession?: (sessionId: string) => void;
};

let chat: ChatProps = {};
let sidebarOnPick: ((id: string) => void) | undefined;
let sidebarSessions: SessionRow[] = [];

vi.mock("./nav/NavRail", () => ({
  NavRail: () => null,
}));

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: ChatProps) => {
    chat = props;
    return (
      <div>
        <output data-testid="loading">
          {props.sessionLoading ? "yes" : "no"}
        </output>
        <output data-testid="transcript">
          {(props.items ?? [])
            .map((item) =>
              item.type === "user_message" ? item.content : item.id,
            )
            .join("|")}
        </output>
      </div>
    );
  },
}));

vi.mock("./sessions/SessionsSidebar", () => ({
  SessionsSidebar: (props: {
    sessions: SessionRow[];
    onPick: (id: string) => void;
  }) => {
    sidebarOnPick = props.onPick;
    sidebarSessions = props.sessions;
    return (
      <ul data-testid="history">
        {props.sessions.map((session) => (
          <li key={session.id}>{session.id}</li>
        ))}
      </ul>
    );
  },
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function messagesFor(sid: string) {
  return json({
    messages: [{ role: "user", content: `message in ${sid}` }],
    ...(sid === CHILD
      ? {
          subagent: { parentSessionId: PARENT, name: "general" },
          readOnly: true,
        }
      : {}),
  });
}

let storedIds: string[];
let parentMessageReads: Array<ReturnType<typeof deferred<Response>>>;
let holdParentMessages: boolean;
let failParentStats: boolean;

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), "http://coddy.test");
  const path = url.pathname;
  if (path === "/coddy/events") {
    return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
      headers: { "Content-Type": "text/event-stream" },
    });
  }
  if (path === "/v1/models") return json({ data: [] });
  if (String(input).startsWith("/coddy/sessions?")) {
    return json({
      sessions: storedIds.map((id) => ({ id, title: id })),
      hasMore: false,
    });
  }
  const match = path.match(/^\/coddy\/sessions\/([^/]+)(.*)$/);
  if (match) {
    const sid = decodeURIComponent(match[1]!);
    const suffix = match[2]!;
    if (suffix === "/messages") {
      if (sid === PARENT && holdParentMessages) {
        const gate = deferred<Response>();
        parentMessageReads.push(gate);
        return gate.promise;
      }
      return messagesFor(sid);
    }
    if (suffix === "/tool-calls") return json({ toolCalls: [] });
    if (suffix === "/stats") {
      if (sid === PARENT && failParentStats) {
        throw new TypeError("stats connection lost");
      }
      return json({ stats: {} });
    }
    if (suffix === "/activity") {
      return json({ sessionId: sid, turnActive: false });
    }
    if (suffix === "/background-tasks") return json({ data: [], running: 0 });
    if (suffix === "/queue") return json({ messages: [] });
  }
  if (path === "/coddy/workspace/context") {
    return json({ cwd: "/workspace", is_git_repo: false });
  }
  return json({});
});

function historyReadCount() {
  return fetchMock.mock.calls.filter(([input]) =>
    String(input).startsWith("/coddy/sessions?"),
  ).length;
}

function mount() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

async function openSession(sid: string) {
  await act(async () => chat.onOpenSession?.(sid));
}

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  storedIds = [PARENT];
  parentMessageReads = [];
  holdParentMessages = false;
  failParentStats = false;
  chat = {};
  sidebarOnPick = undefined;
  sidebarSessions = [];
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

test("picking another session refreshes an already open History drawer", async () => {
  history.replaceState(null, "", `/#/s/${PARENT}?history=1`);
  mount();
  await waitFor(() =>
    expect(sidebarSessions.map((row) => row.id)).toEqual([PARENT]),
  );
  await waitFor(() =>
    expect(screen.getByTestId("loading").textContent).toBe("no"),
  );
  expect(historyReadCount()).toBe(1);

  storedIds = [NEW_SESSION, CHILD, PARENT];
  await act(async () => sidebarOnPick?.(CHILD));

  await waitFor(() =>
    expect(sidebarSessions.map((row) => row.id)).toEqual([
      NEW_SESSION,
      CHILD,
      PARENT,
    ]),
  );
  expect(historyReadCount()).toBe(2);
});

test("a failed stats read cannot strand the parent chat on its loading skeleton", async () => {
  history.replaceState(null, "", `/#/s/${PARENT}`);
  mount();
  await waitFor(() =>
    expect(screen.getByTestId("loading").textContent).toBe("no"),
  );
  await openSession(CHILD);
  await waitFor(() =>
    expect(screen.getByTestId("transcript").textContent).toBe(
      `message in ${CHILD}`,
    ),
  );

  failParentStats = true;
  await openSession(PARENT);

  await waitFor(() =>
    expect(screen.getByTestId("loading").textContent).toBe("no"),
  );
  expect(screen.getByTestId("transcript").textContent).toBe(
    `message in ${PARENT}`,
  );
});

test("returning from a subagent transcript clears the parent's loading state across a stale read", async () => {
  holdParentMessages = true;
  history.replaceState(null, "", `/#/s/${PARENT}`);
  mount();
  await waitFor(() => expect(parentMessageReads).toHaveLength(1));

  await openSession(CHILD);
  await waitFor(() =>
    expect(screen.getByTestId("transcript").textContent).toBe(
      `message in ${CHILD}`,
    ),
  );
  await openSession(PARENT);
  await waitFor(() => expect(parentMessageReads).toHaveLength(2));

  parentMessageReads[0]!.resolve(messagesFor(PARENT));
  parentMessageReads[1]!.resolve(messagesFor(PARENT));

  await waitFor(() =>
    expect(screen.getByTestId("loading").textContent).toBe("no"),
  );
  expect(screen.getByTestId("transcript").textContent).toBe(
    `message in ${PARENT}`,
  );
});
