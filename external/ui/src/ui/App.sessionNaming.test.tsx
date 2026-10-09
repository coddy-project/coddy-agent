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
import type { SessionRow } from "./sessions/types";

// Issue #435: while the first message of a new chat is being named, the chat
// header and the chat's row in History show a placeholder rather than the
// first message itself (a "/rpa-init" token) or "New chat", and both take the
// name and the tags the moment describe answers.

vi.mock("./nav/NavRail", () => ({
  NavRail: () => null,
}));

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    sessionId?: string;
    title?: string;
    titlePending?: boolean;
    onSend?: (text: string, files?: File[]) => void;
  }) => (
    <div>
      <output data-testid="session">{props.sessionId || "home"}</output>
      <output data-testid="title">{props.title}</output>
      <output data-testid="title-pending">
        {props.titlePending ? "pending" : "named"}
      </output>
      <button
        type="button"
        data-testid="send"
        onClick={() => props.onSend?.("/rpa-init")}
      >
        send
      </button>
      <button
        type="button"
        data-testid="send-files-only"
        onClick={() =>
          props.onSend?.("", [
            new File(["x"], "shot.png", { type: "image/png" }),
          ])
        }
      >
        send files
      </button>
    </div>
  ),
}));

vi.mock("./sessions/SessionsSidebar", () => ({
  SessionsSidebar: (props: {
    sessions: SessionRow[];
    namingSessionIds?: ReadonlySet<string>;
  }) => (
    <ul data-testid="history">
      {props.sessions.map((s) => (
        <li key={s.id} data-testid={`row-${s.id}`}>
          {props.namingSessionIds?.has(s.id)
            ? "naming"
            : `${s.title ?? ""}|${(s.tags ?? []).join(",")}`}
        </li>
      ))}
    </ul>
  ),
}));

const WORKSPACE = "/projects/папка";

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

// What the server lists: a session is there from its first send, under the
// title derived from its first message until describe names it.
const serverSessions: SessionRow[] = [];
let releaseDescribe: (body: unknown) => void = () => {};
const describeCalls: Array<{ url: string; sid: string | null }> = [];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = String(input);
    const url = new URL(raw, "http://coddy.test");
    const sid = new Headers(init?.headers).get("X-Coddy-Session-ID");
    if (raw === "/coddy/events") return heldStream();
    if (raw === "/v1/models") return json({ data: [] });
    if (raw.startsWith("/coddy/sessions?")) {
      return json({ sessions: serverSessions });
    }
    if (url.pathname === "/coddy/workspace/context") {
      return json({
        path: WORKSPACE,
        name: "папка",
        is_git_repo: false,
        is_worktree: false,
      });
    }
    if (url.pathname === "/coddy/describe") {
      describeCalls.push({ url: raw, sid });
      return new Promise<Response>((resolve) => {
        releaseDescribe = (body) => resolve(json(body));
      });
    }
    if (raw === "/v1/responses") {
      if (sid && !serverSessions.some((r) => r.id === sid)) {
        serverSessions.unshift({ id: sid, title: "/rpa-init", cwd: WORKSPACE });
      }
      return heldStream();
    }
    if (/^\/coddy\/sessions\/sess_[0-9a-f]+$/.test(url.pathname)) {
      const id = decodeURIComponent(url.pathname.split("/")[3] ?? "");
      const patch = JSON.parse(String(init?.body ?? "{}")) as {
        title?: string;
        tags?: string[];
      };
      const row = serverSessions.find((r) => r.id === id);
      if (row && patch.title) row.title = patch.title;
      if (row && patch.tags) row.tags = patch.tags;
      return json({ object: "coddy.session_patched" });
    }
    return json({}, 404);
  },
);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  describeCalls.length = 0;
  serverSessions.length = 0;
  releaseDescribe = () => {};
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

test("a new chat shows a placeholder for its name until describe answers", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(([u]) =>
        String(u).startsWith("/coddy/workspace/context"),
      ),
    ).toBe(true),
  );

  fireEvent.click(screen.getByTestId("send"));

  await waitFor(() => expect(describeCalls).toHaveLength(1));
  const sid = screen.getByTestId("session").textContent ?? "";
  expect(sid).toMatch(/^sess_/);
  // The describe call names the workspace of the chat it names.
  expect(describeCalls[0]?.sid).toBe(sid);
  expect(describeCalls[0]?.url).toBe(
    `/coddy/describe?cwd=${encodeURIComponent(WORKSPACE)}`,
  );

  // Naming: the header and the History row wait for the name.
  expect(screen.getByTestId("title-pending").textContent).toBe("pending");
  act(() => {
    window.location.hash = "#/history";
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await waitFor(() =>
    expect(screen.getByTestId(`row-${sid}`).textContent).toBe("naming"),
  );

  releaseDescribe({
    object: "coddy.describe",
    short: "Repository onboarding",
    tags: ["onboarding"],
  });

  await waitFor(() =>
    expect(screen.getByTestId("title-pending").textContent).toBe("named"),
  );
  expect(screen.getByTestId("title").textContent).toBe("Repository onboarding");
  expect(screen.getByTestId(`row-${sid}`).textContent).toBe(
    "Repository onboarding|onboarding",
  );
});

test("a describe that names nothing ends the placeholder", async () => {
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
  fireEvent.click(screen.getByTestId("send"));
  await waitFor(() => expect(describeCalls).toHaveLength(1));
  expect(screen.getByTestId("title-pending").textContent).toBe("pending");

  releaseDescribe({ object: "coddy.describe", short: "", tags: [] });

  await waitFor(() =>
    expect(screen.getByTestId("title-pending").textContent).toBe("named"),
  );
});

// A first send of attachments alone has no text for describe to name: the
// chat is not put under a placeholder that nothing would ever take down.
test("a first send without text shows no naming placeholder", async () => {
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
  fireEvent.click(screen.getByTestId("send-files-only"));
  await waitFor(() =>
    expect(screen.getByTestId("session").textContent).toMatch(/^sess_/),
  );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(screen.getByTestId("title-pending").textContent).toBe("named");
  expect(describeCalls).toHaveLength(0);
});
