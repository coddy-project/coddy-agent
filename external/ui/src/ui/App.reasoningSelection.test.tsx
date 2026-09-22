import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * A session carries a reasoning level only once something chose one for it. A
 * session started on another surface has none, and when the model that serves it
 * declares no `reasoning_default` the server reports the effective level as empty.
 * Opening such a session must not leave the composer with no level at all: the
 * turn still runs at the model's own default, so a blank chip says something
 * untrue about what is about to happen.
 */

const WITH = "sess_with_level";
const WITHOUT = "sess_without_level";
const MODEL = "rpa/qwen3.8-27b";

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

const reasoningBySession: Record<string, string> = {
  [WITH]: "high",
  [WITHOUT]: "",
};

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const path = String(input);
  if (path === "/coddy/events") return eventsStream();
  if (path === "/v1/models") {
    return json({
      default_agent_model: MODEL,
      data: [
        { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
        {
          id: MODEL,
          owned_by: "rpa",
          max_context_tokens: 128000,
          // Levels configured, no default named - the shape that produced the bug.
          reasoning_levels: ["low", "medium", "high"],
        },
      ],
    });
  }
  if (path.startsWith("/coddy/sessions?")) {
    return json({
      sessions: [WITH, WITHOUT].map((id) => ({ id, title: id })),
    });
  }
  const match = path.match(/^\/coddy\/sessions\/([^/]+)(.*)$/);
  if (match) {
    const sid = decodeURIComponent(match[1]!);
    const suffix = match[2];
    if (suffix === "/messages") {
      return json({
        model: MODEL,
        selectedModelId: MODEL,
        selectedReasoning: reasoningBySession[sid] ?? "",
        messages: [{ role: "user", content: `prompt in ${sid}` }],
      });
    }
    if (suffix === "/composer-stream") return emptyStream();
    if (suffix === "/tool-calls") return json({ toolCalls: [] });
    if (suffix === "/rewind")
      return json({
        object: "coddy.session_rewound",
        sessionId: sid,
        messagesRev: 1,
      });
    if (suffix === "/stats") return json({ stats: {} });
    if (suffix === "/background-tasks") return json({ data: [], running: 0 });
    if (suffix === "/activity") return json({ sessionId: sid, turnActive: false });
    if (!suffix) return json({});
  }
  if (path === "/coddy/config") return json({});
  if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
  if (path === "/coddy/workspace/context")
    return json({ cwd: "/workspace", is_git_repo: false });
  return json({}, 404);
});

beforeEach(() => {
  eventsController = null;
  initLocale("en");
  localStorage.clear();
  document.cookie = "coddy_llm_reasoning=; Path=/; Max-Age=0";
  history.replaceState(null, "", `/#/s/${WITH}`);
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const reasoningChip = () =>
  screen.getByRole("button", { name: "Reasoning level" });

async function navigate(sid: string) {
  await act(async () => {
    history.replaceState(null, "", `/#/s/${sid}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await screen.findByText(`prompt in ${sid}`);
}

test("a session with no level of its own still shows the model's", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText(`prompt in ${WITH}`);
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("High"));

  // Switching to the other session keeps the same model, so nothing else
  // recomputes the level: the empty value the server reported used to land in
  // the composer as it came and the chip fell back to its generic label.
  await navigate(WITHOUT);
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("Medium"));
  expect(reasoningChip()).not.toHaveTextContent("Reasoning");

  // And back: a session that does name a level still wins.
  await navigate(WITH);
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("High"));
});

// Cross-review: the level the session was opened with is a snapshot, and the
// effect that applies it runs again whenever the models list is refetched - which
// a config save or a config_commit does while the session stays open. Applied a
// second time it undoes whatever the reader picked in between.
test("a config reload leaves the level the reader picked", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText(`prompt in ${WITH}`);
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("High"));

  fireEvent.click(reasoningChip());
  fireEvent.click(await screen.findByRole("menuitem", { name: "Low" }));
  await waitFor(() => expect(reasoningChip()).toHaveTextContent("Low"));

  const before = fetchMock.mock.calls.filter((c) => String(c[0]) === "/v1/models").length;
  await act(async () => {
    eventsController?.enqueue(
      new TextEncoder().encode(`event: config_reloaded\ndata: {}\n\n`),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.filter((c) => String(c[0]) === "/v1/models").length,
    ).toBeGreaterThan(before),
  );

  await new Promise((r) => setTimeout(r, 50));
  expect(reasoningChip()).toHaveTextContent("Low");
});
