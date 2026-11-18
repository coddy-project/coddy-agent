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
 * The image generation tool is included only when the chosen model enables
 * image generation (models[].image_generation, shown by GET /v1/models as
 * image_generation), and each turn carries metadata.image_generation.
 */

const MODEL = "fake/alpha-model";

/** Whether the model row of GET /v1/models carries image_generation. */
let imageGeneration: boolean;
let failStream: boolean;
let holdStream: boolean;
let posted: Array<{ input?: string; metadata?: Record<string, unknown> }>;

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
        toolsets: [],
      });
    }
    if (path === "/v1/responses") {
      posted.push(JSON.parse(String(init?.body)));
      if (holdStream) return eventsStream();
      if (failStream)
        return new Response(
          `event: error\ndata: {"error":{"message":"Image failed"}}\n\n`,
          { headers: { "Content-Type": "text/event-stream" } },
        );
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
            multimodal: true,
            ...(imageGeneration ? { image_generation: true } : {}),
          },
          {
            id: "fake/beta-model",
            owned_by: "fake",
            max_context_tokens: 128000,
            ...(imageGeneration ? { image_generation: true } : {}),
          },
        ],
      });
    }
    if (path.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/canvas")
        return json({
          session_id: sid,
          file: "canvas/site.canvas",
          current: true,
          revision: 1,
          stamp: "f1",
          frames: [
            {
              id: "aaaaaaaaaaaaaaaa",
              path: "site/index.html",
              root: "site",
              device: "desktop",
              width: 1280,
              height: 800,
              x: 0,
              y: 0,
              added_by: "agent",
              gen: 1,
              stamp: "s1",
              url: "/coddy/canvas/tok.sig/index.html",
              expires_at: "2099-01-01T00:00:00Z",
              title: "Shop",
            },
          ],
          items: [],
          edges: [],
        });
      if (suffix === "/canvas/open") return json({});
      if (suffix === "/messages") return json({ messages: [] });
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/workspace")
        return json({ cwd: "/projects/app", is_git_repo: false });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: holdStream });
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
  imageGeneration = false;
  failStream = false;
  holdStream = false;
  posted = [];
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

async function sendHello() {
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
}

test("a model without the opt-in offers no image control", async () => {
  await mountHome();
  expect(screen.queryByTestId("composer-imagegen")).toBeNull();
  await sendHello();
  expect(posted[0]!.metadata?.image_generation).toBeUndefined();
});

test("model opt-in is sent on every turn", async () => {
  imageGeneration = true;
  await mountHome();
  await sendHello();
  await sendHello();
  expect(posted).toHaveLength(2);
  expect(posted[0]!.metadata?.image_generation).toBe(true);
  expect(posted[1]!.metadata?.image_generation).toBe(true);
});
