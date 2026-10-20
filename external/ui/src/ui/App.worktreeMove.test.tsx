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

/**
 * The agent moves the chat into a worktree (worktree_open) or back
 * (worktree_exit) in the middle of a turn. The plate over the composer names
 * the branch the chat works on now as soon as that call has finished, and
 * again when the tab's own turn ends, even in a tab the events stream does not
 * reach (no turn_ended ever arrives here).
 */

const A = "sess_a";
const HDR = "X-Coddy-Session-ID";
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

class Stream {
  controller!: ReadableStreamDefaultController<Uint8Array>;
  closed = false;
  response = new Response(
    new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
      cancel: () => {
        this.closed = true;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
  constructor(signal?: AbortSignal | null) {
    signal?.addEventListener("abort", () => {
      if (this.closed) return;
      this.closed = true;
      this.controller.error(new DOMException("Aborted", "AbortError"));
    });
  }
  frame(event: string, data: unknown) {
    if (this.closed) return;
    this.controller.enqueue(
      new TextEncoder().encode(
        `${event ? `event: ${event}\n` : ""}data: ${JSON.stringify(data)}\n\n`,
      ),
    );
  }
  done() {
    if (this.closed) return;
    this.controller.enqueue(new TextEncoder().encode("data: [DONE]\n\n"));
    this.closed = true;
    this.controller.close();
  }
}

let branch = "main";
let active = false;
let posts: Stream[] = [];
let contextReads = 0;
const messages: { role: string; content: string }[] = [];

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = String(input);
    if (path === "/coddy/events") return new Stream(init.signal).response;
    if (path === "/v1/responses") {
      active = true;
      messages.push({
        role: "user",
        content: JSON.parse(String(init.body)).input,
      });
      const stream = new Stream(init.signal);
      posts.push(stream);
      return stream.response;
    }
    if (path.startsWith("/coddy/workspace/context")) {
      if (new Headers(init.headers).get(HDR) === A) contextReads += 1;
      return json({
        path:
          branch === "main"
            ? "/projects/demo"
            : "/projects/demo/.coddy/worktrees/feature-demo",
        name: "demo",
        is_git_repo: true,
        is_worktree: branch !== "main",
        repo_root: "/projects/demo",
        branch,
      });
    }
    if (path.startsWith("/coddy/sessions?"))
      return json({ sessions: [{ id: A, title: "Chat" }] });
    const match = path.match(/^\/coddy\/sessions\/([^/]+)(.*)$/);
    if (match) {
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/activity")
        return json({ sessionId: A, turnActive: active });
      if (suffix === "/messages") return json({ messages });
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/queue") return json({ messages: [], version: 1 });
      if (!suffix) return json({});
      return json({}, 404);
    }
    if (path === "/v1/models")
      return json({ data: [{ id: "test-model", owned_by: "test" }] });
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    return json({}, 404);
  },
);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", `/#/s/${A}`);
  branch = "main";
  active = false;
  posts = [];
  contextReads = 0;
  messages.splice(
    0,
    messages.length,
    { role: "user", content: "Earlier prompt" },
    { role: "assistant", content: "Earlier answer" },
  );
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(async () => {
  await act(async () => {
    cleanup();
    for (const stream of posts) stream.done();
  });
  vi.unstubAllGlobals();
});

async function mountAndSend(text: string) {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText("Earlier prompt");
  await waitFor(() => expect(contextReads).toBeGreaterThan(0));
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: text },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  return posts[0]!;
}

test("a finished worktree_open moves the plate during the turn", async () => {
  const turn = await mountAndSend("Work on a feature branch");
  const before = contextReads;
  await act(async () => {
    turn.frame("tool_call", {
      toolCallId: "call_wt",
      title: "worktree_open",
      kind: "other",
      status: "in_progress",
    });
  });
  // Still running: the chat has not moved yet.
  await new Promise((resolve) => setTimeout(resolve, 30));
  expect(contextReads).toBe(before);

  branch = "feature/demo";
  await act(async () => {
    turn.frame("tool_call_update", {
      toolCallId: "call_wt",
      status: "completed",
      content: [
        {
          type: "content",
          content: { type: "text", text: '{"branch":"feature/demo"}' },
        },
      ],
    });
  });
  await waitFor(() => expect(contextReads).toBeGreaterThan(before));
  // The turn is still running, and the plate already names the branch.
  expect(posts[0]!.closed).toBe(false);
  expect(await screen.findByText("feature/demo")).toBeInTheDocument();
});

test("the end of the tab's own turn reads the workspace again without a turn_ended event", async () => {
  const turn = await mountAndSend("/worktree feature/demo");
  const before = contextReads;
  branch = "feature/demo";
  await act(async () => {
    turn.frame("", { choices: [{ delta: { content: "Moved." } }] });
    active = false;
    messages.push({ role: "assistant", content: "Moved." });
    turn.done();
  });
  await waitFor(() => expect(contextReads).toBeGreaterThan(before));
  expect(await screen.findByText("feature/demo")).toBeInTheDocument();
});
