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
import {
  CODDY_WORKSPACE_DIR_COOKIE,
  CODDY_WORKTREE_COOKIE,
  readLastWorkspaceDir,
  readWorktreePref,
  writeLastWorkspaceDir,
} from "./chat/workspaceCookies";

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
    workspaceCtx?: { path?: string; branch?: string } | null;
    worktreePref?: boolean;
    onWorktreeToggle?: () => void;
    onWorkspacePickFolder?: (path: string) => void;
  }) => (
    <>
      <output
        data-testid="workspace-state"
        data-branch={props.workspaceCtx?.branch || ""}
        data-worktree={String(props.worktreePref === true)}
      >
        {props.sessionId || "home"}:{props.workspaceCtx?.path || ""}
      </output>
      <button type="button" data-testid="toggle-worktree" onClick={() => props.onWorktreeToggle?.()}>
        worktree
      </button>
      <button
        type="button"
        data-testid="pick-folder"
        onClick={() => props.onWorkspacePickFolder?.("/projects/picked")}
      >
        pick
      </button>
    </>
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

const ACTIVE_WORKSPACE = "/projects/active";
const DEFAULT_WORKSPACE = "/projects/default";
const MAIN_CHECKOUT = "/projects/main";
/** The branch the folder is on right now, as git would say; a test switches it. */
let headBranch = "main";
const LINKED_WORKTREE = "/projects/main/.coddy/worktrees/feat-x";

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return heldStream();
    if (path === "/v1/models") return json({ data: [] });
    if (path.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    if (path.startsWith("/coddy/sessions/sess_wt/messages")) {
      return json({
        session_id: "sess_wt",
        messages: [{ role: "user", content: "hello" }],
      });
    }
    if (path.startsWith("/coddy/workspace/context?path=")) {
      const picked = decodeURIComponent(path.slice("/coddy/workspace/context?path=".length));
      if (picked === "/projects/gone") {
        return json({ error: { message: "folder not found" } }, 400);
      }
      return json({
        path: picked,
        name: picked.split("/").at(-1),
        is_git_repo: true,
        is_worktree: picked === LINKED_WORKTREE,
        repo_root: MAIN_CHECKOUT,
        branch: picked === LINKED_WORKTREE ? "feat/x" : headBranch,
      });
    }
    if (path.startsWith("/coddy/sessions/sess_active/messages")) {
      return json({
        session_id: "sess_active",
        messages: [{ role: "user", content: "hello" }],
      });
    }
    if (path === "/coddy/workspace/context") {
      const sid = new Headers(init?.headers).get("X-Coddy-Session-ID");
      if (sid === "sess_wt") {
        return json({
          path: LINKED_WORKTREE,
          name: "feat-x",
          is_git_repo: true,
          is_worktree: true,
          repo_root: MAIN_CHECKOUT,
          branch: "feat/x",
        });
      }
      const selected =
        sid === "sess_active" ? ACTIVE_WORKSPACE : DEFAULT_WORKSPACE;
      return json({
        path: selected,
        name: selected.split("/").at(-1),
        is_git_repo: true,
        is_worktree: false,
        branch: headBranch,
      });
    }
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    return json({}, 404);
  },
);

function clearCookies() {
  for (const name of [CODDY_WORKSPACE_DIR_COOKIE, CODDY_WORKTREE_COOKIE]) {
    document.cookie = `${name}=; Path=/; Max-Age=0`;
  }
}

beforeEach(() => {
  clearCookies();
  headBranch = "main";
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/#/s/sess_active");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  clearCookies();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

const PICKED = "/projects/picked";

function mountApp() {
  return render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

const state = () => screen.getByTestId("workspace-state");

// The start screen opens on the folder last picked in this browser, not on the
// folder of the chat just left: what a new chat keeps is the operator's pick.
test("a new chat starts in the folder last picked in this browser, not the chat's", async () => {
  writeLastWorkspaceDir(PICKED);
  mountApp();
  await waitFor(() => expect(state().textContent).toBe(`sess_active:${ACTIVE_WORKSPACE}`));
  fireEvent.click(screen.getByTestId("new-chat"));
  await waitFor(() => expect(state().textContent).toBe(`home:${PICKED}`));
  // Never posted to the chat being left.
  expect(
    fetchMock.mock.calls.some(
      ([input, init]) =>
        String(input).includes("/workspace") &&
        (init as RequestInit | undefined)?.method === "POST",
    ),
  ).toBe(false);
});

test("with no folder picked in this browser a new chat starts in the server's default", async () => {
  history.replaceState(null, "", "/#/s/sess_wt");
  mountApp();
  await waitFor(() => expect(state().textContent).toBe(`sess_wt:${LINKED_WORKTREE}`));
  // The chat's linked worktree is its own: the next chat does not inherit it.
  fireEvent.click(screen.getByTestId("new-chat"));
  await waitFor(() => expect(state().textContent).toBe(`home:${DEFAULT_WORKSPACE}`));
});

// The start screen names the branch the folder is on now: one switched with
// git switch in a terminal since the last chat shows when a new chat starts,
// and again when the page gets the focus back.
test("the start screen shows the branch the folder is on now", async () => {
  mountApp();
  await waitFor(() => expect(state().getAttribute("data-branch")).toBe("main"));
  headBranch = "feat/switched";
  fireEvent.click(screen.getByTestId("new-chat"));
  await waitFor(() => expect(state().textContent).toBe(`home:${DEFAULT_WORKSPACE}`));
  await waitFor(() => expect(state().getAttribute("data-branch")).toBe("feat/switched"));
  headBranch = "fix/later";
  await act(async () => {
    window.dispatchEvent(new Event("focus"));
  });
  await waitFor(() => expect(state().getAttribute("data-branch")).toBe("fix/later"));
  expect(state().textContent).toBe(`home:${DEFAULT_WORKSPACE}`);
});

// The folder picked and the worktree checkbox are this browser's: they hold
// across chats and reloads, the checkbox for every folder.
test("the folder picked and the worktree checkbox are remembered in this browser", async () => {
  history.replaceState(null, "", "/");
  const first = mountApp();
  await waitFor(() => expect(state().textContent).toBe(`home:${DEFAULT_WORKSPACE}`));
  fireEvent.click(screen.getByTestId("pick-folder"));
  await waitFor(() => expect(state().textContent).toBe(`home:${PICKED}`));
  expect(readLastWorkspaceDir()).toBe(PICKED);
  fireEvent.click(screen.getByTestId("toggle-worktree"));
  expect(state().getAttribute("data-worktree")).toBe("true");
  expect(readWorktreePref()).toBe(true);
  // Into a chat and back.
  await act(async () => {
    history.replaceState(null, "", "/#/s/sess_active");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await waitFor(() => expect(state().textContent).toBe(`sess_active:${ACTIVE_WORKSPACE}`));
  fireEvent.click(screen.getByTestId("new-chat"));
  await waitFor(() => expect(state().textContent).toBe(`home:${PICKED}`));
  expect(state().getAttribute("data-worktree")).toBe("true");
  // A reload of the page keeps both.
  first.unmount();
  history.replaceState(null, "", "/");
  mountApp();
  await waitFor(() => expect(state().textContent).toBe(`home:${PICKED}`));
  expect(state().getAttribute("data-worktree")).toBe("true");
});

// A folder picked once and gone since: the start screen falls back to the
// server's default and forgets it.
test("a remembered folder that is gone falls back to the server's default", async () => {
  writeLastWorkspaceDir("/projects/gone");
  history.replaceState(null, "", "/");
  mountApp();
  await waitFor(() => expect(state().textContent).toBe(`home:${DEFAULT_WORKSPACE}`));
  expect(readLastWorkspaceDir()).toBe("");
});
