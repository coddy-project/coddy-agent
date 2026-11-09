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

// First-send tier choices must follow the model before a session exists.

const MODEL = "api/alpha-model";

let accepted = 0;
let posted: Array<{ input?: string; metadata?: Record<string, string> }>;

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

function eventsStream() {
  return new Response(
    new ReadableStream<Uint8Array>({
      start: () => {},
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
      });
    }
    if (path === "/v1/responses") {
      const body = JSON.parse(String(init?.body));
      posted.push(body);
      if (
        body.metadata?.model === "claude/claude-model" &&
        body.metadata?.service_tier
      )
        return json({ error: { message: "unsupported service tier" } }, 400);
      accepted++;
      return emptyStream();
    }
    if (path === "/v1/models") {
      return json({
        data: [
          { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
          {
            id: MODEL,
            owned_by: "api",
            service_tiers: ["default", "priority"],
            max_context_tokens: 128000,
          },
          {
            id: "api/beta-model",
            owned_by: "api",
            service_tiers: ["default", "priority"],
          },
          { id: "claude/claude-model", owned_by: "claude" },
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
  posted = [];
  accepted = 0;
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
  vi.unstubAllGlobals();
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

async function selectFast() {
  fireEvent.click(screen.getByRole("button", { name: "Service tier" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Fast" }));
}
async function changeModel(name: string) {
  fireEvent.click(screen.getByRole("button", { name: "Model" }));
  fireEvent.click(screen.getByRole("menuitem", { name }));
}
async function sendFirst() {
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted).toHaveLength(1));
}
test("new chat clears an unsupported tier with a notice before its first send", async () => {
  await mountHome();
  await selectFast();
  await changeModel("claude-model");
  expect(screen.queryByRole("button", { name: "Service tier" })).toBeNull();
  expect(
    screen.getByText(
      "The selected service tier is not supported by this model. Using the model default.",
    ),
  ).toBeVisible();
  await sendFirst();
  expect(posted[0]?.metadata?.model).toBe("claude/claude-model");
  expect(posted[0]?.metadata).not.toHaveProperty("service_tier");
  await waitFor(() => expect(accepted).toBe(1));
  expect(screen.queryByText("unsupported service tier")).toBeNull();
});
test("new chat preserves a supported tier across model changes", async () => {
  await mountHome();
  await selectFast();
  await changeModel("beta-model");
  await sendFirst();
  expect(posted[0]?.metadata?.service_tier).toBe("priority");
  expect(posted[0]?.metadata?.model).toBe("api/beta-model");
});

test("switching model and sending in one event cannot send the previous tier", async () => {
  await mountHome();
  await selectFast();
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Model" }));
  const next = screen.getByRole("menuitem", {
    name: "claude-model",
  });
  const send = screen.getByRole("button", { name: "Send" });
  act(() => {
    fireEvent.click(next);
    fireEvent.click(send);
  });
  await waitFor(() => expect(posted).toHaveLength(1));
  expect(posted[0]?.metadata?.model).toBe("claude/claude-model");
  expect(posted[0]?.metadata).not.toHaveProperty("service_tier");
});

test("model default stays unset when the new chat changes models", async () => {
  await mountHome();
  await changeModel("claude-model");
  await sendFirst();
  expect(posted[0]?.metadata).not.toHaveProperty("service_tier");
});
