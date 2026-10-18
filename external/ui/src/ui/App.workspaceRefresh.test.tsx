import React from "react";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * Where a chat works can change during a turn: worktree_create and
 * worktree_exit move the session, and so does /worktree. The plate over the
 * composer reads the chat's folder again whenever a turn of the chat on
 * screen ends, so it names the worktree the chat went into, or the main
 * checkout it came back to, without a reload.
 */

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: () => <div data-testid="chat-screen" />,
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

class EventsStream {
  controller!: ReadableStreamDefaultController<Uint8Array>;
  response = new Response(
    new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
  frame(event: string, data: unknown) {
    this.controller.enqueue(
      new TextEncoder().encode(
        `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`,
      ),
    );
  }
}

let streams: EventsStream[] = [];
/** The session each GET /coddy/workspace/context named, in request order. */
let contextReads: string[] = [];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") {
      const stream = new EventsStream();
      streams.push(stream);
      return stream.response;
    }
    if (path.startsWith("/coddy/workspace/context")) {
      const headers = new Headers(init?.headers);
      contextReads.push(headers.get("X-Coddy-Session-ID") ?? "");
      return json({
        path: "/projects/demo",
        name: "demo",
        is_git_repo: true,
        repo_root: "/projects/demo",
        branch: "main",
      });
    }
    if (path === "/v1/models") return json({ data: [] });
    if (path.startsWith("/coddy/sessions?"))
      return json({ sessions: [], active_count: 0 });
    return json({}, 404);
  },
);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/#/s/sess_here");
  streams = [];
  contextReads = [];
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const readsOf = (sid: string) => contextReads.filter((s) => s === sid).length;

test("a turn of the chat on screen ends and the plate reads its folder again", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(streams.length).toBeGreaterThan(0));
  await waitFor(() => expect(readsOf("sess_here")).toBeGreaterThan(0));
  const before = readsOf("sess_here");

  // Another chat's turn says nothing about where this one works.
  await act(async () => {
    streams[0]!.frame("turn_ended", { sessionId: "sess_other" });
  });
  await new Promise((resolve) => setTimeout(resolve, 30));
  expect(readsOf("sess_here")).toBe(before);

  await act(async () => {
    streams[0]!.frame("turn_ended", { sessionId: "sess_here" });
  });
  await waitFor(() => expect(readsOf("sess_here")).toBe(before + 1));
});
