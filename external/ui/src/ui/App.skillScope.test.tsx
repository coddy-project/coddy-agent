import React from "react";
import {
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

// The known skill names chip `/name` tokens in the composer and in the
// transcript. They must describe the workspace the chat runs in: the session's
// own, or before the first message the folder picked on the start screen.

vi.mock("./nav/NavRail", () => ({
  NavRail: (props: { onNewChat: () => void }) => (
    <button type="button" data-testid="new-chat" onClick={props.onNewChat}>
      New chat
    </button>
  ),
}));

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    sessionId?: string;
    workspaceCtx?: { path?: string } | null;
    knownSkillNames?: Set<string>;
    chatWorkspacePath?: string;
    onWorkspacePickFolder?: (path: string) => void;
    onOpenSession?: (id: string) => void;
    onSend?: (text: string) => void;
  }) => (
    <div>
      <output data-testid="workspace-state">
        {props.sessionId || "home"}:{props.workspaceCtx?.path || ""}
      </output>
      <output data-testid="chat-workspace">{props.chatWorkspacePath}</output>
      <output data-testid="known-skills">
        {[...(props.knownSkillNames ?? [])].sort().join(",")}
      </output>
      <button
        type="button"
        data-testid="pick-data"
        onClick={() => props.onWorkspacePickFolder?.(DATA_WORKSPACE)}
      >
        data
      </button>
      <button
        type="button"
        data-testid="open-b"
        onClick={() => props.onOpenSession?.("sess_b")}
      >
        b
      </button>
      <button
        type="button"
        data-testid="send"
        onClick={() => props.onSend?.("hello")}
      >
        send
      </button>
      <button
        type="button"
        data-testid="pick-other"
        onClick={() => props.onWorkspacePickFolder?.(OTHER_WORKSPACE)}
      >
        other
      </button>
    </div>
  ),
}));

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

const DEFAULT_WORKSPACE = "/projects/default";
const DATA_WORKSPACE = "/projects/data";
const OTHER_WORKSPACE = "/projects/other";
const ACTIVE_WORKSPACE = "/projects/active";
const B_WORKSPACE = "/projects/b";

// Skills each workspace holds in its .coddy/skills, plus one global skill.
const SKILLS_BY_WORKSPACE: Record<string, string[]> = {
  [DEFAULT_WORKSPACE]: ["global", "default-only"],
  [DATA_WORKSPACE]: ["global", "dat-report", "dat-export"],
  [OTHER_WORKSPACE]: ["global"],
  [ACTIVE_WORKSPACE]: ["global", "active-only"],
  [B_WORKSPACE]: ["global", "b-only"],
};

function ctx(path: string) {
  return json({
    path,
    name: path.split("/").at(-1),
    is_git_repo: false,
    is_worktree: false,
  });
}

let slowDataAnswer: Promise<void> | null = null;
// The folder each new session was anchored on, as the server keeps it.
const sessionCwd = new Map<string, string>();
let slowDataContext: Promise<void> | null = null;

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = String(input);
    const url = new URL(raw, "http://coddy.test");
    const sid = new Headers(init?.headers).get("X-Coddy-Session-ID");
    if (raw === "/coddy/events") return heldStream();
    if (raw === "/v1/models") return json({ data: [] });
    if (raw.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    if (raw.startsWith("/coddy/sessions/sess_b/messages")) {
      return json({ session_id: "sess_b", messages: [] });
    }
    if (/^\/coddy\/sessions\/[^/]+\/workspace$/.test(url.pathname)) {
      const body = JSON.parse(String(init?.body ?? "{}")) as { path?: string };
      const id = decodeURIComponent(url.pathname.split("/")[3] ?? "");
      sessionCwd.set(id, body.path ?? DEFAULT_WORKSPACE);
      return ctx(body.path ?? DEFAULT_WORKSPACE);
    }
    if (raw === "/v1/responses") return heldStream();
    if (raw.startsWith("/coddy/sessions/sess_active/messages")) {
      return json({
        session_id: "sess_active",
        messages: [{ role: "user", content: "hello" }],
      });
    }
    if (url.pathname === "/coddy/workspace/context") {
      const picked = url.searchParams.get("path");
      if (picked === DATA_WORKSPACE && slowDataContext) await slowDataContext;
      if (picked) return ctx(picked);
      // The context of session b never answers: what it lists must not wait
      // for the chips.
      if (sid === "sess_b") return new Promise<Response>(() => {});
      if (sid && sessionCwd.has(sid)) return ctx(sessionCwd.get(sid)!);
      return ctx(sid === "sess_active" ? ACTIVE_WORKSPACE : DEFAULT_WORKSPACE);
    }
    if (raw === "/coddy/config") return json({});
    if (url.pathname === "/coddy/slash-commands") {
      const cwd = url.searchParams.get("cwd");
      const ws =
        sid === "sess_active"
          ? ACTIVE_WORKSPACE
          : sid === "sess_b"
            ? B_WORKSPACE
            : (sid && sessionCwd.get(sid)) || cwd || DEFAULT_WORKSPACE;
      if (ws === DATA_WORKSPACE && slowDataAnswer) await slowDataAnswer;
      return json({
        items: (SKILLS_BY_WORKSPACE[ws] ?? []).map((name) => ({ name })),
      });
    }
    return json({}, 404);
  },
);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  slowDataAnswer = null;
  slowDataContext = null;
  sessionCwd.clear();
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

function renderApp() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

const known = () => screen.getByTestId("known-skills").textContent;

test("a folder picked before the session exists decides the known skills", async () => {
  renderApp();
  await waitFor(() => expect(known()).toBe("default-only,global"));

  fireEvent.click(screen.getByTestId("pick-data"));
  await waitFor(() => expect(known()).toBe("dat-export,dat-report,global"));

  fireEvent.click(screen.getByTestId("pick-other"));
  await waitFor(() => expect(known()).toBe("global"));
});

test("skills of the previous folder are dropped while the next answer is in flight", async () => {
  renderApp();
  await waitFor(() => expect(known()).toBe("default-only,global"));

  let release: () => void = () => {};
  slowDataAnswer = new Promise<void>((resolve) => {
    release = resolve;
  });
  fireEvent.click(screen.getByTestId("pick-data"));
  await waitFor(() =>
    expect(screen.getByTestId("workspace-state")).toHaveTextContent(
      `home:${DATA_WORKSPACE}`,
    ),
  );
  expect(known()).toBe("");

  // The data answer arrives after another folder was picked: it is stale and
  // must not paint the skills of data over the other folder.
  fireEvent.click(screen.getByTestId("pick-other"));
  await waitFor(() => expect(known()).toBe("global"));
  release();
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(known()).toBe("global");
});

test("an open session lists the skills of its own workspace", async () => {
  history.replaceState(null, "", "/#/s/sess_active");
  renderApp();
  await waitFor(() =>
    expect(screen.getByTestId("workspace-state")).toHaveTextContent(
      `sess_active:${ACTIVE_WORKSPACE}`,
    ),
  );
  await waitFor(() => expect(known()).toBe("active-only,global"));
});

test("the folder picked last wins over a slower preview of the one before", async () => {
  renderApp();
  await waitFor(() => expect(known()).toBe("default-only,global"));

  let release: () => void = () => {};
  slowDataContext = new Promise<void>((resolve) => {
    release = resolve;
  });
  fireEvent.click(screen.getByTestId("pick-data"));
  // The pick decides the scope at once, before its preview answers.
  expect(screen.getByTestId("chat-workspace").textContent).toBe(DATA_WORKSPACE);
  fireEvent.click(screen.getByTestId("pick-other"));
  await waitFor(() =>
    expect(screen.getByTestId("workspace-state")).toHaveTextContent(
      `home:${OTHER_WORKSPACE}`,
    ),
  );
  release();
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.getByTestId("workspace-state")).toHaveTextContent(
    `home:${OTHER_WORKSPACE}`,
  );
  expect(screen.getByTestId("chat-workspace").textContent).toBe(
    OTHER_WORKSPACE,
  );
  expect(known()).toBe("global");
});

test("another session lists its own skills even before its chips answer", async () => {
  history.replaceState(null, "", "/#/s/sess_active");
  renderApp();
  await waitFor(() => expect(known()).toBe("active-only,global"));

  fireEvent.click(screen.getByTestId("open-b"));
  await waitFor(() => expect(known()).toBe("b-only,global"));
});

test("the first send keeps the picked folder although its preview never answered", async () => {
  renderApp();
  await waitFor(() => expect(known()).toBe("default-only,global"));
  slowDataContext = new Promise<void>(() => {});
  fireEvent.click(screen.getByTestId("pick-data"));
  await waitFor(() => expect(known()).toBe("dat-export,dat-report,global"));

  fireEvent.click(screen.getByTestId("send"));
  await waitFor(() =>
    expect(screen.getByTestId("workspace-state").textContent).toMatch(
      new RegExp(`^sess_[^:]+:${DATA_WORKSPACE}$`),
    ),
  );
  expect(screen.getByTestId("chat-workspace").textContent).toBe(DATA_WORKSPACE);
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(known()).toBe("dat-export,dat-report,global");
});
