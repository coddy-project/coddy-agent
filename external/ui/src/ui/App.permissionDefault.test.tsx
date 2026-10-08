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
 * The start screen has no session, so no settings snapshot says which
 * permission mode the first turn will run under. GET /coddy/info names the one
 * the server is configured with (tools.permission_mode): the chip shows it, and
 * a mode picked there rides in with the first message as `/permissions <mode>`
 * whenever it is not that mode - an explicit "Ask first" under a bypass
 * configuration included, and any pick while the server has not said.
 */

const MODEL = "fake/alpha-model";
const SESSION = "sess_existing";

/** What GET /coddy/info names; null answers 404 (a server that says nothing). */
let infoMode: string | null;
/** The configured mode the stored session's snapshot names. */
let sessionConfiguredMode: string;
/** Whether the next POST /v1/responses fails before any answer. */
let failNextPost: boolean;
let posted: Array<{ input?: string }>;
let version: number;

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

/** The server-events stream, held open so a test can push a frame into it. */
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
      return infoMode === null
        ? json({}, 404)
        : json({
            object: "coddy.info",
            version: "1.2.3",
            hostname: "box",
            permissionMode: infoMode,
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
          { id: MODEL, owned_by: "fake", max_context_tokens: 128000 },
        ],
      });
    }
    if (path.startsWith("/coddy/sessions?")) {
      return json({ sessions: [{ id: SESSION, title: SESSION }] });
    }
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/messages" && sid === SESSION) {
        return json({
          model: MODEL,
          selectedModelId: MODEL,
          // An existing session that runs under ask, whatever is configured.
          settings: {
            sessionId: sid,
            version: ++version,
            model: MODEL,
            reasoning: "",
            reasoningChoices: [],
            mode: "agent",
            permissionMode: "ask",
            configuredPermissionMode: sessionConfiguredMode,
            overrides: [],
          },
          messages: [{ role: "user", content: `prompt in ${sid}` }],
        });
      }
      if (suffix === "/messages") return json({ messages: [] });
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: false });
      if (!suffix) return json({});
    }
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path === "/coddy/workspace/context")
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  },
);

beforeEach(() => {
  eventsController = null;
  infoMode = "bypass";
  sessionConfiguredMode = "bypass";
  failNextPost = false;
  posted = [];
  version = 10;
  initLocale("en");
  localStorage.clear();
  document.cookie = "coddy_llm_model=; Path=/; Max-Age=0";
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
});

// As in App.sessionSettings.test.tsx, the stub stays for the whole file: a
// turn a test started ends after the test does, and its last reads must still
// meet the stub rather than the real fetch of a torn-down page.

const permissionChip = () => screen.getByTestId("composer-permission");
const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));
const infoReads = () =>
  fetchMock.mock.calls.filter((c) => String(c[0]) === "/coddy/info").length;

async function mountHome() {
  history.replaceState(null, "", "/");
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

async function pickPermission(label: string) {
  fireEvent.click(permissionChip());
  const items = await screen.findAllByText(label);
  fireEvent.click(items[items.length - 1]!);
  await waitFor(() => expect(permissionChip()).toHaveTextContent(label));
}

async function send(text: string) {
  posted = [];
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: text },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  return posted[0]!.input;
}

async function pushEvent(name: string, data: unknown) {
  await act(async () => {
    eventsController?.enqueue(
      new TextEncoder().encode(
        `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`,
      ),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
}

test("the start screen's chip names the permission mode the server is configured with", async () => {
  await mountHome();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  // Untouched, the chip names the configured mode: nothing rides in with the
  // first message, which runs under that mode anyway.
  expect(await send("hello")).toBe("hello");
});

test("an explicit Ask first under a bypass configuration rides in with the first message", async () => {
  await mountHome();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  await pickPermission("Accept edits");
  await pickPermission("Ask first");
  expect(await send("hello")).toBe("/permissions ask\nhello");
});

test("a pick of another mode rides in with the first message", async () => {
  // The chip's default reading aside: the first send already holds the id of
  // the session it creates, which is no reason to leave the pick behind.
  infoMode = null;
  await mountHome();
  await pickPermission("Accept edits");
  expect(await send("hello")).toBe("/permissions accept_edits\nhello");
});

test("a pick of the configured mode sends nothing extra", async () => {
  await mountHome();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  await pickPermission("Accept edits");
  await pickPermission("Bypass");
  expect(await send("hello")).toBe("hello");
});

test("a pick made while the server does not say its mode is sent", async () => {
  infoMode = null;
  await mountHome();
  // Nothing known: the chip keeps its default, and a pick of that very mode is
  // still sent, because the server may be configured with another one.
  expect(permissionChip()).toHaveTextContent("Ask first");
  await pickPermission("Accept edits");
  await pickPermission("Ask first");
  expect(await send("hello")).toBe("/permissions ask\nhello");
});

test("a configuration reload moves the start screen's chip, never a pick", async () => {
  await mountHome();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));

  const before = infoReads();
  infoMode = "accept_edits";
  await pushEvent("config_reloaded", {
    object: "coddy.config_reloaded",
    at: "2026-10-08T00:00:00Z",
  });
  await waitFor(() => expect(infoReads()).toBeGreaterThan(before));
  await waitFor(() =>
    expect(permissionChip()).toHaveTextContent("Accept edits"),
  );

  // A pick stays what the operator chose across the next reload, and it is
  // compared with the mode the server has now.
  await pickPermission("Bypass");
  infoMode = "bypass";
  const again = infoReads();
  await pushEvent("config_reloaded", {
    object: "coddy.config_reloaded",
    at: "2026-10-08T00:00:01Z",
  });
  await waitFor(() => expect(infoReads()).toBeGreaterThan(again));
  await settle();
  expect(permissionChip()).toHaveTextContent("Bypass");
  expect(await send("hello")).toBe("hello");
});

test("a new chat after a session starts from the mode configured now", async () => {
  infoMode = "ask";
  sessionConfiguredMode = "ask";
  history.replaceState(null, "", `/#/s/${SESSION}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText(`prompt in ${SESSION}`);
  await settle();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Ask first"));

  // The configuration moved to bypass while the session was on screen: its
  // snapshot, read before, still names ask as the configured mode.
  const before = infoReads();
  infoMode = "bypass";
  await pushEvent("config_reloaded", {
    object: "coddy.config_reloaded",
    at: "2026-10-08T00:00:00Z",
  });
  await waitFor(() => expect(infoReads()).toBeGreaterThan(before));
  await settle();
  // The session keeps its own mode.
  expect(permissionChip()).toHaveTextContent("Ask first");

  fireEvent.click(screen.getByTestId("nav-home"));
  await settle();
  expect(permissionChip()).toHaveTextContent("Bypass");
  expect(await send("hello")).toBe("hello");
});

test("a first message the server never took keeps the pick for the next try", async () => {
  await mountHome();
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  await pickPermission("Ask first");

  failNextPost = true;
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  // The prompt comes back to the composer (Recoverable composer sends).
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue(
      "hello",
    ),
  );
  await settle();
  expect(permissionChip()).toHaveTextContent("Ask first");

  posted = [];
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  expect(posted[0]!.input).toBe("/permissions ask\nhello");
});
