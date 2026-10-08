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
 * The mode, the model and the reasoning level the composer shows belong to the
 * session on screen. The page keeps one set of selectors for the whole tab, so
 * two things used to let one session's values leave it: a new chat started on
 * the values of the chat just left, and a prompt sent while the opened
 * session's own settings were still being read carried the previous session's
 * values, which the server applies to the session and keeps.
 */

const ALPHA = "fake/alpha-model";
const BETA = "fake/beta-model";
const LEVELS = ["low", "medium", "high"];

/** The server's side of a stored session: what its snapshot reports. */
type StoredSession = {
  mode: string;
  model: string;
  reasoning: string;
};

const S_PLAN = "sess_plan_beta";
const S_AGENT = "sess_agent_alpha";
const S_ASK = "sess_ask_alpha";
const S_UNKNOWN = "sess_never_stored";

let sessions: Record<string, StoredSession>;
let version: number;
/** What the server took from a prompt: the session header and the body. */
let posted: Array<{
  sid: string;
  model: string;
  metadata: Record<string, string>;
}>;
/** Transcript reads a test holds back until it calls the function. */
let heldReads: Record<string, Promise<void>>;
/** When set, the answer to a prompt stays open, so the turn keeps running. */
let holdTurn: boolean;
let openTurns: Map<string, ReadableStreamDefaultController<Uint8Array>>;
/** What the queue route was asked, and what it answers: no turn is running. */
let queued: string[];

function resetServer() {
  sessions = {
    [S_PLAN]: { mode: "plan", model: BETA, reasoning: "high" },
    [S_AGENT]: { mode: "agent", model: ALPHA, reasoning: "low" },
    [S_ASK]: { mode: "ask", model: ALPHA, reasoning: "medium" },
  };
  version = 10;
  posted = [];
  heldReads = {};
  holdTurn = false;
  openTurns = new Map();
  queued = [];
}

/** endTurn lets the running turns finish. */
function endTurn() {
  for (const c of openTurns.values()) c.close();
  openTurns.clear();
}

/** hold makes the transcript read of a session wait; the result lets it go. */
function hold(sid: string): () => void {
  let release = () => {};
  heldReads[sid] = new Promise<void>((resolve) => {
    release = resolve;
  });
  return release;
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

function snapshot(sid: string) {
  const s = sessions[sid]!;
  return {
    sessionId: sid,
    version: ++version,
    model: s.model,
    reasoning: s.reasoning,
    reasoningChoices: LEVELS,
    mode: s.mode,
    permissionMode: "ask",
    configuredPermissionMode: "ask",
    overrides: [],
  };
}

/**
 * The server's way with a profile prompt (external/httpserver/server_metadata.go):
 * the session is created when it is new, and the mode, the model and the level
 * the request names are applied to it - kept - when they differ from its own.
 */
function takePrompt(sid: string, init: RequestInit | undefined) {
  const body = JSON.parse(String(init?.body)) as {
    model: string;
    metadata?: Record<string, string>;
  };
  const metadata = body.metadata ?? {};
  posted.push({ sid, model: body.model, metadata });
  const s = (sessions[sid] ??= {
    mode: "agent",
    model: ALPHA,
    reasoning: "",
  });
  s.mode = body.model;
  if (metadata.model) s.model = metadata.model;
  if (metadata.reasoning) s.reasoning = metadata.reasoning;
}

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") {
      return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
        headers: { "Content-Type": "text/event-stream" },
      });
    }
    if (path === "/v1/responses") {
      const sid = new Headers(init?.headers).get("X-Coddy-Session-ID") ?? "";
      takePrompt(sid, init);
      if (!holdTurn) return emptyStream();
      return new Response(
        new ReadableStream<Uint8Array>({
          start: (c) => {
            openTurns.set(sid, c);
          },
        }),
        { headers: { "Content-Type": "text/event-stream" } },
      );
    }
    if (path === "/v1/models") {
      return json({
        data: [
          { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
          {
            id: ALPHA,
            owned_by: "fake",
            max_context_tokens: 128000,
            reasoning_levels: LEVELS,
          },
          {
            id: BETA,
            owned_by: "fake",
            max_context_tokens: 128000,
            reasoning_levels: LEVELS,
          },
        ],
      });
    }
    if (path.startsWith("/coddy/sessions?")) {
      return json({
        sessions: Object.keys(sessions).map((id) => ({ id, title: id })),
      });
    }
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/messages") {
        await heldReads[sid];
        const s = sessions[sid];
        if (!s) return json({ error: { message: "session not found" } }, 404);
        return json({
          model: s.model,
          selectedModelId: s.model,
          selectedReasoning: s.reasoning,
          settings: snapshot(sid),
          messages: [{ role: "user", content: `prompt in ${sid}` }],
        });
      }
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: openTurns.has(sid) });
      if (suffix === "/queue" && init?.method === "POST") {
        queued.push(JSON.parse(String(init.body)).text);
        // The turn ended between the keystroke and the request.
        return json(
          { error: { code: "no_active_turn", message: "No live turn" } },
          409,
        );
      }
      if (!suffix && sessions[sid]) return json({});
    }
    if (path === "/coddy/config")
      return json({ agent: { queue_mode: "steer" } });
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path === "/coddy/workspace/context")
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  },
);

beforeEach(() => {
  resetServer();
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
});

// The stub stays for the whole file, as in App.sessionSettings.test.tsx: the
// reads a turn ends with must still meet it when they come late.

const modePill = () => screen.getByRole("button", { name: "Mode" });
const modelChip = () => screen.getByRole("button", { name: "Model" });
const reasoningChip = () =>
  screen.getByRole("button", { name: "Reasoning level" });
const sendButton = () => screen.getByRole("button", { name: "Send" });
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

/** goTo changes the address the way a link or the Back button does. */
async function goTo(hash: string) {
  await act(async () => {
    history.replaceState(null, "", hash);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
}

async function navigate(sid: string) {
  await goTo(`/#/s/${sid}`);
  await screen.findByText(`prompt in ${sid}`);
  await settle();
}

async function pickMode(name: string) {
  fireEvent.click(modePill());
  fireEvent.click(await screen.findByRole("menuitem", { name }));
}

function type(text: string) {
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: text },
  });
}

async function send(text: string) {
  posted = [];
  type(text);
  fireEvent.click(sendButton());
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
  await settle();
  return posted[0]!;
}

test("a new chat starts in Agent mode, not in the mode of the chat just left", async () => {
  await mountSession(S_PLAN);
  expect(modePill()).toHaveTextContent("Plan");

  fireEvent.click(screen.getByTestId("nav-home"));
  await settle();
  expect(modePill()).toHaveTextContent("Agent");

  // The first message creates the session in the mode the start page shows.
  const prompt = await send("hello");
  expect(prompt.model).toBe("agent");
  expect(sessions[prompt.sid]?.mode).toBe("agent");
});

test("the Back button to the start page leaves the chat's settings behind", async () => {
  await mountSession(S_PLAN);
  expect(modePill()).toHaveTextContent("Plan");
  expect(modelChip()).toHaveTextContent("beta-model");
  expect(reasoningChip()).toHaveTextContent("High");

  await goTo("/#/");
  await settle();
  expect(modePill()).toHaveTextContent("Agent");
  expect(modelChip()).toHaveTextContent("alpha-model");
  expect(reasoningChip()).toHaveTextContent("Medium");

  const prompt = await send("hello");
  expect(prompt.model).toBe("agent");
  expect(prompt.metadata.model).toBe(ALPHA);
});

test("a mode picked on the start page creates the chat in that mode", async () => {
  await mountHome();
  await pickMode("Plan");
  await waitFor(() => expect(modePill()).toHaveTextContent("Plan"));

  const prompt = await send("hello");
  expect(prompt.model).toBe("plan");
  expect(sessions[prompt.sid]?.mode).toBe("plan");
  // The chat that was just created keeps showing it.
  expect(modePill()).toHaveTextContent("Plan");
});

test("a prompt sent while the opened session's settings are read carries nothing of the previous session", async () => {
  await mountSession(S_PLAN);
  const release = hold(S_AGENT);

  // The address changes; the read of the opened session's transcript - and
  // with it its settings - is still out.
  await goTo(`/#/s/${S_AGENT}`);
  type("sent too early");
  fireEvent.click(sendButton());
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Enter",
  });
  await settle();
  expect(posted).toEqual([]);
  expect(sendButton()).toBeDisabled();
  // Held back, not lost.
  expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue(
    "sent too early",
  );

  await act(async () => release());
  await screen.findByText(`prompt in ${S_AGENT}`);
  await settle();
  expect(modePill()).toHaveTextContent("Agent");
  expect(modelChip()).toHaveTextContent("alpha-model");
  expect(reasoningChip()).toHaveTextContent("Low");
  expect(sendButton()).toBeEnabled();

  const prompt = await send("sent in time");
  expect(prompt.sid).toBe(S_AGENT);
  expect(prompt.model).toBe("agent");
  expect(prompt.metadata.model).toBe(ALPHA);
  expect(prompt.metadata.reasoning).toBe("low");
  // The opened session still holds what it had.
  expect(sessions[S_AGENT]).toEqual({
    mode: "agent",
    model: ALPHA,
    reasoning: "low",
  });
});

test("a mode picked in a session is sent with its next prompt, and stays in that session", async () => {
  await mountSession(S_AGENT);
  await pickMode("Plan");
  await waitFor(() => expect(modePill()).toHaveTextContent("Plan"));
  expect((await send("plan it")).model).toBe("plan");
  expect(sessions[S_AGENT]?.mode).toBe("plan");

  // The pick is that session's: another one shows its own mode.
  await navigate(S_ASK);
  expect(modePill()).toHaveTextContent("Ask");
});

test("a session the server does not know opens a chat that can be written to", async () => {
  await mountSession(S_PLAN);
  await goTo(`/#/s/${S_UNKNOWN}`);
  await settle();
  // Nothing to wait for: the read said there is no such session. The chat
  // starts on a new chat's settings, not on the plan chat's.
  expect(modePill()).toHaveTextContent("Agent");
  expect(modelChip()).toHaveTextContent("alpha-model");
  type("first words");
  expect(sendButton()).toBeEnabled();
  const prompt = await send("first words");
  expect(prompt.sid).toBe(S_UNKNOWN);
  expect(prompt.model).toBe("agent");
  expect(prompt.metadata.model).toBe(ALPHA);
});

test("coming back to a chat whose turn this tab runs shows that chat's own settings", async () => {
  await mountSession(S_PLAN);
  holdTurn = true;
  await send("a long task");
  // The turn goes on while another chat is read.
  await navigate(S_ASK);
  expect(modePill()).toHaveTextContent("Ask");

  // The way back keeps the rows from the turn's shadow and reads no
  // transcript: the settings are read on their own.
  await goTo(`/#/s/${S_PLAN}`);
  await waitFor(() => expect(modePill()).toHaveTextContent("Plan"));
  expect(modelChip()).toHaveTextContent("beta-model");
  expect(reasoningChip()).toHaveTextContent("High");
  endTurn();
});

test("a follow-up the server refuses to queue is not lost while the settings are still being read", async () => {
  await mountSession(S_PLAN);
  holdTurn = true;
  await send("a long task");
  await navigate(S_ASK);
  // The read of the way back stays out, so the selectors still name Ask.
  const release = hold(S_PLAN);
  await goTo(`/#/s/${S_PLAN}`);
  await settle();
  expect(modePill()).toHaveTextContent("Ask");

  // The turn is running, so Enter queues; the server finds it ended and the
  // page falls back to an ordinary prompt, which has to wait for the settings.
  type("and one more thing");
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Enter",
  });
  await waitFor(() => expect(queued).toEqual(["and one more thing"]));
  await settle();
  expect(posted).toHaveLength(1);
  expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue(
    "and one more thing",
  );

  await act(async () => release());
  await waitFor(() => expect(modePill()).toHaveTextContent("Plan"));
  endTurn();
});
