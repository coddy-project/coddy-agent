import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import type { TranscriptItem } from "./chat/types";
import type { SessionRow } from "./sessions/types";
import {
  readWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "./skills/workspaceAtRecents";

// Issue #357: the first message of a new chat is on screen in the frame it is
// sent, under a line saying the session is being prepared, even while the
// folder and the branch picked on the start screen are still being applied;
// and when they cannot be, the chat says so and the text goes back to the
// composer.

vi.mock("./nav/NavRail", () => ({
  NavRail: () => null,
}));

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    sessionId?: string;
    items?: TranscriptItem[];
    draft?: string;
    generating?: boolean;
    onSend?: (text: string) => void;
    onWorkspacePickFolder?: (path: string) => void;
    chatWorkspacePath?: string;
    startNotice?: string;
  }) => (
    <div>
      <output data-testid="session">{props.sessionId || "home"}</output>
      <output data-testid="chat-workspace">{props.chatWorkspacePath}</output>
      <output data-testid="start-notice">{props.startNotice ?? ""}</output>
      <output data-testid="draft">{props.draft ?? ""}</output>
      <output data-testid="generating">
        {props.generating ? "yes" : "no"}
      </output>
      <ul data-testid="items">
        {(props.items ?? []).map((it) => (
          <li key={it.id} data-type={it.type}>
            {it.type === "user_message"
              ? it.content
              : it.type === "system_notice"
                ? it.message
                : ""}
          </li>
        ))}
      </ul>
      <button
        type="button"
        data-testid="pick-data"
        onClick={() => props.onWorkspacePickFolder?.(DATA_WORKSPACE)}
      >
        data
      </button>
      <button
        type="button"
        data-testid="send"
        onClick={() => props.onSend?.("hello there")}
      >
        send
      </button>
    </div>
  ),
}));

vi.mock("./sessions/SessionsSidebar", () => ({
  SessionsSidebar: (props: { sessions: SessionRow[] }) => (
    <ul data-testid="history">
      {props.sessions.map((s) => (
        <li key={s.id} data-testid={`row-${s.id}`}>
          {s.title ?? ""}
        </li>
      ))}
    </ul>
  ),
}));

const DEFAULT_WORKSPACE = "/projects/default";
const DATA_WORKSPACE = "/projects/data";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function heldStream() {
  return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
    headers: { "Content-Type": "text/event-stream" },
  });
}

function ctx(path: string) {
  return json({
    path,
    name: path.split("/").at(-1),
    is_git_repo: false,
    is_worktree: false,
  });
}

let answerWorkspace: (res: Response) => void = () => {};
let responsesPosted = 0;
// Held when a test needs describe to answer after the workspace did.
let holdDescribe = false;
let answerDescribe: (body: unknown) => void = () => {};
const namedSessions: string[] = [];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = String(input);
    const url = new URL(raw, "http://coddy.test");
    if (raw === "/coddy/events") return heldStream();
    if (raw === "/v1/models") return json({ data: [] });
    if (raw.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    if (url.pathname === "/coddy/workspace/context") {
      return ctx(url.searchParams.get("path") || DEFAULT_WORKSPACE);
    }
    if (/^\/coddy\/sessions\/[^/]+\/activity$/.test(url.pathname)) {
      // No turn ran: the server never took the send.
      const id = decodeURIComponent(url.pathname.split("/")[3] ?? "");
      return json({ sessionId: id, turnActive: false });
    }
    if (/^\/coddy\/sessions\/[^/]+\/workspace$/.test(url.pathname)) {
      void init;
      return new Promise<Response>((resolve) => {
        answerWorkspace = resolve;
      });
    }
    if (url.pathname === "/coddy/describe") {
      if (holdDescribe) {
        return new Promise<Response>((resolve) => {
          answerDescribe = (body) => resolve(json(body));
        });
      }
      return json({ object: "coddy.describe", short: "", tags: [] });
    }
    if (
      init?.method === "PATCH" &&
      /^\/coddy\/sessions\/[^/]+$/.test(url.pathname)
    ) {
      // The server never admitted the send: it has no such session. A name
      // sent to it is what the tests look for.
      const body = JSON.parse(String(init.body ?? "{}")) as { title?: string };
      if (body.title) namedSessions.push(url.pathname);
      return json({ error: { message: "session not found" } }, 404);
    }
    if (raw === "/v1/responses") {
      responsesPosted++;
      return heldStream();
    }
    return json({}, 404);
  },
);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  answerWorkspace = () => {};
  responsesPosted = 0;
  holdDescribe = false;
  answerDescribe = () => {};
  namedSessions.length = 0;
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

async function startOnPickedFolder() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(([u]) =>
        String(u).startsWith("/coddy/workspace/context"),
      ),
    ).toBe(true),
  );
  fireEvent.click(screen.getByTestId("pick-data"));
}

const rows = () =>
  [...screen.getByTestId("items").querySelectorAll("li")].map((li) => ({
    type: li.getAttribute("data-type"),
    text: li.textContent,
  }));

test("the first message shows while the picked folder is still being applied", async () => {
  await startOnPickedFolder();
  fireEvent.click(screen.getByTestId("send"));

  await waitFor(() =>
    expect(rows()).toContainEqual({
      type: "user_message",
      text: "hello there",
    }),
  );
  // Nothing has answered: the workspace is still being applied and the turn
  // has not been asked for, and the chat already says it is at work.
  expect(responsesPosted).toBe(0);
  expect(screen.getByTestId("generating").textContent).toBe("yes");

  answerWorkspace(ctx(DATA_WORKSPACE));
  await waitFor(() => expect(responsesPosted).toBe(1));
});

// The workspace did not take: a retry from a chat created without it would go
// into another workspace, so the start screen comes back with the picks and
// the text, and says why the message was not sent.
test("a folder that cannot be applied brings the start screen back with the picks and the text", async () => {
  await startOnPickedFolder();
  fireEvent.click(screen.getByTestId("send"));
  await waitFor(() =>
    expect(rows()).toContainEqual({
      type: "user_message",
      text: "hello there",
    }),
  );

  answerWorkspace(
    json({ error: { message: "folder not found: /projects/data" } }, 400),
  );

  await waitFor(() =>
    expect(screen.getByTestId("session").textContent).toBe("home"),
  );
  expect(screen.getByTestId("start-notice").textContent).toContain(
    "folder not found: /projects/data",
  );
  expect(screen.getByTestId("draft").textContent).toBe("hello there");
  expect(screen.getByTestId("chat-workspace").textContent).toBe(DATA_WORKSPACE);
  expect(rows()).toEqual([]);
  expect(responsesPosted).toBe(0);

  // Sending again applies the picks again, for a session of its own.
  fireEvent.click(screen.getByTestId("send"));
  await waitFor(() =>
    expect(screen.getByTestId("start-notice").textContent).toBe(""),
  );
  answerWorkspace(ctx(DATA_WORKSPACE));
  await waitFor(() => expect(responsesPosted).toBe(1));
});

const failWorkspace = () =>
  answerWorkspace(
    json({ error: { message: "folder not found: /projects/data" } }, 400),
  );

async function sendFirstMessage(): Promise<string> {
  fireEvent.click(screen.getByTestId("send"));
  await waitFor(() =>
    expect(rows()).toContainEqual({
      type: "user_message",
      text: "hello there",
    }),
  );
  return screen.getByTestId("session").textContent ?? "";
}

function goTo(hash: string) {
  act(() => {
    window.location.hash = hash;
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
}

// The chat the failed send opened never reached the server: a name describe
// finds for it afterwards neither puts it back in History nor is sent to a
// session that does not exist.
test("a name that arrives after the folder failed adds no chat to History", async () => {
  holdDescribe = true;
  await startOnPickedFolder();
  const sid = await sendFirstMessage();
  expect(sid).toMatch(/^sess_/);

  failWorkspace();
  await waitFor(() =>
    expect(screen.getByTestId("session").textContent).toBe("home"),
  );
  goTo("#/history");
  await waitFor(() => expect(screen.getByTestId("history")).toBeTruthy());

  answerDescribe({
    object: "coddy.describe",
    short: "Late name",
    tags: ["late"],
  });
  await new Promise((resolve) => setTimeout(resolve, 100));

  expect(screen.queryByTestId(`row-${sid}`)).toBeNull();
  expect(namedSessions).toEqual([]);
});

// The "@" picks made on the start screen move to the chat a send opens; a
// send that never left takes them back with the text.
test("the @ recents of the start screen stay there when the folder failed", async () => {
  recordWorkspaceAtRecent(WORKSPACE_AT_RECENTS_NO_SESSION_KEY, {
    path_rel: "src/main.go",
    kind: "file",
  });
  await startOnPickedFolder();
  const sid = await sendFirstMessage();
  expect(readWorkspaceAtRecents(sid).map((r) => r.path_rel)).toEqual([
    "src/main.go",
  ]);

  failWorkspace();
  await waitFor(() =>
    expect(screen.getByTestId("session").textContent).toBe("home"),
  );

  expect(
    readWorkspaceAtRecents(WORKSPACE_AT_RECENTS_NO_SESSION_KEY).map(
      (r) => r.path_rel,
    ),
  ).toEqual(["src/main.go"]);
  expect(readWorkspaceAtRecents(sid)).toEqual([]);
});

// The operator went back to the start screen while the folder was being
// applied: the text still comes back, to the composer in front of them.
test("the text comes back when the operator left the chat before the folder failed", async () => {
  await startOnPickedFolder();
  await sendFirstMessage();
  goTo("#/");
  await waitFor(() =>
    expect(screen.getByTestId("session").textContent).toBe("home"),
  );
  expect(screen.getByTestId("draft").textContent).toBe("");

  failWorkspace();

  await waitFor(() =>
    expect(screen.getByTestId("draft").textContent).toBe("hello there"),
  );
  expect(screen.getByTestId("start-notice").textContent).toContain(
    "folder not found: /projects/data",
  );
  expect(responsesPosted).toBe(0);
});
