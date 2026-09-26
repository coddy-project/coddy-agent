import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * Entering a session shows the settings that session runs with, as its
 * snapshot names them (#362): its own model, its own reasoning level - thinking
 * switched off included - and its permission mode. What the start page picked,
 * and the cookies that remember it, are the default of a new chat only: they
 * never replace a session's value on screen, and so never ride into it with
 * the next message.
 */

const ALPHA = "fake/alpha-model";
const BETA = "fake/beta-model";
const LEVELS = ["low", "medium", "high"];

/** The server's side of a stored session: what its snapshot reports. */
type StoredSession = {
  /** The session's own model and level (the snapshot). */
  model: string;
  reasoning: string;
  /** Levels the session may hold: the menu's, plus off where it exists. */
  choices: string[];
  permissionMode: string;
  /** What the running turn holds, when one does (top-level model/selectedReasoning). */
  turnModel?: string;
  overrides?: Array<{ setting: string; value: string; turnsLeft: number; active?: boolean }>;
};

const S_HIGH_BYPASS = "sess_high_bypass";
const S_NO_LEVEL = "sess_no_level";
const S_THINKING_OFF = "sess_thinking_off";
const S_TURN_OVERRIDE = "sess_turn_override";

let sessions: Record<string, StoredSession>;
let version: number;
let posted: Array<{ metadata?: Record<string, string> }>;

function resetSessions() {
  sessions = {
    [S_HIGH_BYPASS]: { model: ALPHA, reasoning: "high", choices: LEVELS, permissionMode: "bypass" },
    // A session started on another surface: no level of its own, on a model
    // that names no reasoning_default, so the server reports none.
    [S_NO_LEVEL]: { model: ALPHA, reasoning: "", choices: LEVELS, permissionMode: "ask" },
    [S_THINKING_OFF]: { model: ALPHA, reasoning: "off", choices: [...LEVELS, "off"], permissionMode: "ask" },
    // A turn runs on beta for this turn only; the session's own model is alpha.
    [S_TURN_OVERRIDE]: {
      model: ALPHA,
      reasoning: "medium",
      choices: LEVELS,
      permissionMode: "ask",
      turnModel: BETA,
      overrides: [{ setting: "model", value: BETA, turnsLeft: 0, active: true }],
    },
  };
  version = 10;
  posted = [];
}

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

function snapshot(sid: string) {
  const s = sessions[sid]!;
  return {
    sessionId: sid,
    version: ++version,
    model: s.model,
    reasoning: s.reasoning,
    reasoningChoices: s.choices,
    mode: "agent",
    permissionMode: s.permissionMode,
    configuredPermissionMode: "ask",
    overrides: s.overrides ?? [],
  };
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const path = String(input);
  if (path === "/coddy/events") return eventsStream();
  if (path === "/v1/responses") {
    posted.push(JSON.parse(String(init?.body)));
    return emptyStream();
  }
  if (path === "/v1/models") {
    return json({
      data: [
        { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
        // Levels configured, no default named; off is not a menu level.
        { id: ALPHA, owned_by: "fake", max_context_tokens: 128000, reasoning_levels: LEVELS },
        { id: BETA, owned_by: "fake", max_context_tokens: 128000, reasoning_levels: LEVELS },
      ],
    });
  }
  if (path.startsWith("/coddy/sessions?")) {
    return json({ sessions: Object.keys(sessions).map((id) => ({ id, title: id })) });
  }
  const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
  if (match) {
    const sid = decodeURIComponent(match[1]!);
    const suffix = match[2]!.split("?")[0];
    if (suffix === "/messages") {
      const s = sessions[sid]!;
      return json({
        // Top level: what the running turn uses; the snapshot: the session's own.
        model: s.turnModel ?? s.model,
        selectedModelId: s.model,
        selectedReasoning: s.reasoning,
        settings: snapshot(sid),
        messages: [{ role: "user", content: `prompt in ${sid}` }],
      });
    }
    if (!suffix && init?.method === "PATCH") {
      const body = JSON.parse(String(init.body)) as {
        permissionMode?: string;
        selectedReasoning?: string;
      };
      if (body.permissionMode) sessions[sid]!.permissionMode = body.permissionMode;
      if (body.selectedReasoning !== undefined) sessions[sid]!.reasoning = body.selectedReasoning;
      return json({ object: "coddy.session_patched", id: sid, settings: snapshot(sid) });
    }
    if (suffix === "/composer-stream") return emptyStream();
    if (suffix === "/tool-calls") return json({ toolCalls: [] });
    if (suffix === "/stats") return json({ stats: {} });
    if (suffix === "/background-tasks") return json({ data: [], running: 0 });
    if (suffix === "/activity") return json({ sessionId: sid, turnActive: false });
    if (!suffix) return json({});
  }
  if (path === "/coddy/config") return json({});
  if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
  if (path === "/coddy/workspace/context") return json({ cwd: "/workspace", is_git_repo: false });
  return json({}, 404);
});

beforeEach(() => {
  eventsController = null;
  resetSessions();
  initLocale("en");
  localStorage.clear();
  document.cookie = "coddy_llm_reasoning=; Path=/; Max-Age=0";
  document.cookie = "coddy_llm_model=; Path=/; Max-Age=0";
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const modelChip = () => screen.getByRole("button", { name: "Model" });
const reasoningChip = () => screen.getByRole("button", { name: "Reasoning level" });
const permissionChip = () => screen.getByTestId("composer-permission");
const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));

function mount(hash = "/") {
  history.replaceState(null, "", hash);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

async function mountHome() {
  mount();
  await waitFor(() => expect(modelChip()).toHaveTextContent("alpha-model"));
}

async function mountSession(sid: string) {
  mount(`/#/s/${sid}`);
  await screen.findByText(`prompt in ${sid}`);
  await settle();
}

async function navigate(sid: string) {
  await act(async () => {
    history.replaceState(null, "", `/#/s/${sid}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await screen.findByText(`prompt in ${sid}`);
  await settle();
}

async function pickOnStartPage(chip: () => HTMLElement, item: string | RegExp) {
  fireEvent.click(chip());
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
}

async function send(text: string) {
  posted = [];
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  return posted[0]!.metadata ?? {};
}

async function pushEvent(name: string, data: unknown) {
  await act(async () => {
    eventsController?.enqueue(new TextEncoder().encode(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`));
    await new Promise((r) => setTimeout(r, 0));
  });
}

test("a model picked on the start page does not replace an existing session's", async () => {
  await mountHome();
  await pickOnStartPage(modelChip, /beta-model/);
  await waitFor(() => expect(modelChip()).toHaveTextContent("beta-model"));

  await navigate(S_HIGH_BYPASS);
  expect(modelChip()).toHaveTextContent("alpha-model");
  expect((await send("next")).model).toBe(ALPHA);
});

test("a level picked on the start page stays out of a session with no level of its own", async () => {
  await mountHome();
  await pickOnStartPage(reasoningChip, "Low");
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("Low"));

  // The same session shows Medium when no start page pick exists
  // (App.reasoningSelection.test.tsx): the pick must not change that.
  await navigate(S_NO_LEVEL);
  expect(reasoningChip()).toHaveTextContent("Medium");
  // Medium is what the chip resolves for a session with no level of its own:
  // shown, never sent, so nothing pins the session to a level nobody chose.
  expect((await send("next")).reasoning).toBeUndefined();
});

test("a level picked in a session with none of its own is sent as its own", async () => {
  await mountSession(S_NO_LEVEL);
  fireEvent.click(reasoningChip());
  fireEvent.click(await screen.findByRole("menuitem", { name: "High" }));
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("High"));
  await settle();
  expect((await send("next")).reasoning).toBe("high");
});

test("a session's own level wins over the start page's", async () => {
  await mountHome();
  await pickOnStartPage(reasoningChip, "Low");
  await navigate(S_HIGH_BYPASS);
  expect(reasoningChip()).toHaveTextContent("High");
});

test("a later snapshot with no level keeps the level on screen", async () => {
  await mountSession(S_NO_LEVEL);
  expect(reasoningChip()).toHaveTextContent("Medium");

  // Switching the permission mode here answers with the whole snapshot, whose
  // level is empty for a model that names no default.
  fireEvent.click(permissionChip());
  const items = await screen.findAllByText("Bypass");
  fireEvent.click(items[items.length - 1]!);
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  await settle();
  expect(reasoningChip()).toHaveTextContent("Medium");

  // The same snapshot from another surface, on the events stream.
  await pushEvent("session_settings", {
    object: "coddy.session_settings",
    sessionId: S_NO_LEVEL,
    settings: snapshot(S_NO_LEVEL),
    notice: "Mode: agent for this session",
    source: "console",
  });
  await settle();
  expect(reasoningChip()).toHaveTextContent("Medium");
});

test("the permission mode follows the session entered, not the start page", async () => {
  await mountHome();
  expect(permissionChip()).toHaveTextContent("Ask first");
  fireEvent.click(permissionChip());
  const items = await screen.findAllByText("Accept edits");
  fireEvent.click(items[items.length - 1]!);
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Accept edits"));

  await navigate(S_HIGH_BYPASS);
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
  await navigate(S_NO_LEVEL);
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Ask first"));
  await navigate(S_HIGH_BYPASS);
  await waitFor(() => expect(permissionChip()).toHaveTextContent("Bypass"));
});

test("thinking switched off stays off on entering, across a config reload and in the next message", async () => {
  await mountSession(S_THINKING_OFF);
  expect(reasoningChip()).toHaveTextContent("Off");

  const before = fetchMock.mock.calls.filter((c) => String(c[0]) === "/v1/models").length;
  await pushEvent("config_reloaded", {});
  await waitFor(() =>
    expect(fetchMock.mock.calls.filter((c) => String(c[0]) === "/v1/models").length).toBeGreaterThan(before),
  );
  await settle();
  expect(reasoningChip()).toHaveTextContent("Off");
  expect((await send("next")).reasoning).toBe("off");
});

test("the menu offers off where the session's snapshot does, so a session can go back to it", async () => {
  await mountSession(S_THINKING_OFF);
  fireEvent.click(reasoningChip());
  fireEvent.click(await screen.findByRole("menuitem", { name: "High" }));
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("High"));
  await settle();
  fireEvent.click(reasoningChip());
  fireEvent.click(await screen.findByRole("menuitem", { name: "Off" }));
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("Off"));
  await settle();
  expect((await send("next")).reasoning).toBe("off");

  // A session whose model cannot turn thinking off has no such item.
  await navigate(S_HIGH_BYPASS);
  fireEvent.click(reasoningChip());
  await screen.findByRole("menuitem", { name: "High" });
  expect(screen.queryByRole("menuitem", { name: "Off" })).toBeNull();
});

test("a transcript read older than the snapshot on screen moves nothing back", async () => {
  await mountSession(S_HIGH_BYPASS);
  expect(modelChip()).toHaveTextContent("alpha-model");

  // Another surface switched the session to beta, and the events stream
  // brought the change before a read built earlier lands.
  sessions[S_HIGH_BYPASS]!.model = BETA;
  const newer = { ...snapshot(S_HIGH_BYPASS), version: 1000 };
  sessions[S_HIGH_BYPASS]!.model = ALPHA;
  await pushEvent("session_settings", {
    object: "coddy.session_settings",
    sessionId: S_HIGH_BYPASS,
    settings: newer,
    notice: "Model: fake/beta-model for this session",
    source: "console",
  });
  await waitFor(() => expect(modelChip()).toHaveTextContent("beta-model"));

  const reads = () =>
    fetchMock.mock.calls.filter((c) => String(c[0]).startsWith(`/coddy/sessions/${S_HIGH_BYPASS}/messages`)).length;
  const before = reads();
  await pushEvent("session_rewound", { sessionId: S_HIGH_BYPASS });
  await waitFor(() => expect(reads()).toBeGreaterThan(before));
  await settle();
  expect(modelChip()).toHaveTextContent("beta-model");
});

test("a model the running turn holds is not taken for the session's", async () => {
  await mountSession(S_TURN_OVERRIDE);
  expect(modelChip()).toHaveTextContent("alpha-model");
  expect(screen.getByTestId("composer-overrides")).toHaveTextContent("beta-model");
});

test("leaving a session for a new chat starts from the start page's defaults", async () => {
  document.cookie = "coddy_llm_reasoning=low; Path=/";
  await mountSession(S_HIGH_BYPASS);
  expect(reasoningChip()).toHaveTextContent("High");
  expect(permissionChip()).toHaveTextContent("Bypass");

  fireEvent.click(screen.getByTestId("nav-home"));
  await settle();
  expect(reasoningChip()).toHaveTextContent("Low");
  expect(permissionChip()).toHaveTextContent("Ask first");
});
