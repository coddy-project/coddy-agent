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
 * The composer's toolset chip on the start screen: there is none while the
 * server names no toolsets[] preset (GET /coddy/info), it appears once a
 * configuration reload brings one, it names the toolset the chosen model names
 * until another is picked, and a pick rides in with the first message as
 * metadata.toolset.
 */

const MODEL = "fake/alpha-model";

/** The presets GET /coddy/info names. */
let toolsets: string[];
let posted: Array<{ input?: string; metadata?: Record<string, string> }>;
/** Whether the next POST /v1/responses fails before any answer. */
let failNextPost: boolean;

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

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return eventsStream();
    if (path === "/coddy/info") {
      return json({
        object: "coddy.info",
        version: "1.2.3",
        hostname: "box",
        permissionMode: "ask",
        toolsets,
      });
    }
    if (path === "/v1/responses") {
      if (failNextPost) {
        failNextPost = false;
        throw new TypeError("Failed to fetch");
      }
      posted.push(JSON.parse(String(init?.body)));
      return emptyStream();
    }
    if (path === "/v1/models") {
      return json({
        data: [
          { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
          {
            id: MODEL,
            owned_by: "fake",
            max_context_tokens: 128000,
            ...(toolsets.includes("coding") ? { toolset: "coding" } : {}),
          },
        ],
      });
    }
    if (path.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/messages") return json({ messages: [] });
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/workspace")
        return json({ cwd: "/projects/app", is_git_repo: false });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: false });
      if (!suffix) return json({});
    }
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path.startsWith("/coddy/workspace/context"))
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  },
);

beforeEach(() => {
  eventsController = null;
  toolsets = [];
  posted = [];
  failNextPost = false;
  initLocale("en");
  localStorage.clear();
  document.cookie = "coddy_llm_model=; Path=/; Max-Age=0";
  document.cookie = "coddy_workspace_dir=; Path=/; Max-Age=0";
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
});

const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));

async function mountHome() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(
      "alpha-model",
    ),
  );
  await settle();
}

test("the toolset chip appears once a reload brings a preset", async () => {
  await mountHome();
  expect(screen.queryByTestId("composer-toolset")).toBeNull();

  toolsets = ["coding", "no-net"];
  await act(async () => {
    eventsController?.enqueue(
      new TextEncoder().encode(
        `event: config_reloaded\ndata: ${JSON.stringify({ object: "coddy.config_reloaded", at: "now" })}\n\n`,
      ),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
  // The chosen model names coding, so that is what a new chat runs with.
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent("coding"),
  );
});

test("a toolset picked on the start screen rides in with the first message", async () => {
  toolsets = ["coding", "no-net"];
  await mountHome();
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent("coding"),
  );
  fireEvent.click(screen.getByTestId("composer-toolset"));
  fireEvent.click(screen.getByRole("menuitem", { name: "All tools" }));
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent(
      "All tools",
    ),
  );

  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  expect(posted[0]!.input).toBe("hello");
  expect(posted[0]!.metadata?.toolset).toBe("default");
});

async function pickAllTools() {
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent("coding"),
  );
  fireEvent.click(screen.getByTestId("composer-toolset"));
  fireEvent.click(screen.getByRole("menuitem", { name: "All tools" }));
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent(
      "All tools",
    ),
  );
}

test("a first message the server never took keeps the toolset for the next try", async () => {
  toolsets = ["coding", "no-net"];
  await mountHome();
  await pickAllTools();

  failNextPost = true;
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue(
      "hello",
    ),
  );
  await settle();
  expect(screen.getByTestId("composer-toolset")).toHaveTextContent("All tools");

  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  expect(posted[0]!.metadata?.toolset).toBe("default");
});

test("a toolset picked on the start screen is dropped when a new chat starts over", async () => {
  toolsets = ["coding", "no-net"];
  await mountHome();
  await pickAllTools();

  // Another chat, then a new one: the pick of the first start screen is gone.
  await act(async () => {
    window.location.hash = "#/s/sess_other";
    await new Promise((r) => setTimeout(r, 50));
  });
  await act(async () => {
    window.location.hash = "#/";
    await new Promise((r) => setTimeout(r, 50));
  });
  await waitFor(() =>
    expect(screen.getByTestId("composer-toolset")).toHaveTextContent("coding"),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  expect(posted[0]!.metadata?.toolset).toBeUndefined();
});
