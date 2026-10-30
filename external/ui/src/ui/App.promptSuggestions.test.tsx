import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    promptSuggestion?: string;
    draft: string;
    onDraftChange: (text: string) => void;
  }) => (
    <>
      <div data-testid="offered-suggestion">{props.promptSuggestion ?? ""}</div>
      <input
        aria-label="Suggestion draft"
        value={props.draft}
        onChange={(event) => props.onDraftChange(event.target.value)}
      />
    </>
  ),
}));

const SID = "sess_suggestion";
const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function openRunningChat() {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", `/#/s/${SID}`);
  let active = true;
  let events!: ReadableStreamDefaultController<Uint8Array>;
  let resolveSuggestion!: (value: Response) => void;
  const requests: AbortSignal[] = [];
  const answer = new Promise<Response>((resolve) => {
    resolveSuggestion = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input);
      if (path === "/coddy/events")
        return new Response(
          new ReadableStream({
            start(controller) {
              events = controller;
            },
          }),
          { headers: { "Content-Type": "text/event-stream" } },
        );
      if (path.startsWith("/coddy/sessions?"))
        return json({ sessions: [{ id: SID, turnActive: active }] });
      if (path.endsWith("/activity"))
        return json({ sessionId: SID, turnActive: active });
      if (path.endsWith("/prompt-suggestion")) {
        requests.push(init.signal!);
        return answer;
      }
      if (path.includes("/messages"))
        return json({
          messages: [
            { role: "user", content: "fix the bug" },
            { role: "assistant", content: "fixed it" },
          ],
        });
      if (path.endsWith("/queue")) return json({ messages: [], version: 1 });
      if (path.endsWith("/composer-stream"))
        return new Response(new ReadableStream(), {
          headers: { "Content-Type": "text/event-stream" },
        });
      if (path === "/v1/models")
        return json({ data: [{ id: "test-model", owned_by: "test" }] });
      if (path === "/coddy/workspace/context")
        return json({ cwd: "/workspace", is_git_repo: false });
      return json({});
    }),
  );
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(events).toBeDefined());
  const emit = async (event: string, data: unknown) =>
    act(async () => {
      events.enqueue(
        new TextEncoder().encode(
          `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`,
        ),
      );
    });
  await emit("ready", { object: "coddy.events_ready" });
  await waitFor(() =>
    expect(
      document.querySelector("[data-testid=offered-suggestion]"),
    ).not.toBeNull(),
  );
  // Let the activity read establish that this chat is running before its end.
  await waitFor(() =>
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some(([path]) =>
        String(path).endsWith("/activity"),
      ),
    ).toBe(true),
  );
  return {
    requests,
    emit,
    async end() {
      active = false;
      await emit("turn_ended", {
        object: "coddy.turn_event",
        sessionId: SID,
        phase: "ended",
        at: "2026-10-30T10:00:00Z",
      });
      await waitFor(() => expect(requests).toHaveLength(1));
    },
    async start() {
      active = true;
      await emit("turn_started", {
        object: "coddy.turn_event",
        sessionId: SID,
        phase: "started",
      });
    },
    async resolve() {
      await act(async () => {
        resolveSuggestion(json({ text: "run the tests" }));
        await answer;
      });
    },
  };
}

test("a new turn retires a visible prompt suggestion", async () => {
  const chat = await openRunningChat();
  await chat.end();
  await chat.resolve();
  await waitFor(() =>
    expect(screen.getByTestId("offered-suggestion").textContent).toBe(
      "run the tests",
    ),
  );
  await chat.start();
  expect(screen.getByTestId("offered-suggestion").textContent).toBe("");
});

test("a late suggestion from a previous turn is discarded", async () => {
  const chat = await openRunningChat();
  await chat.end();
  await chat.start();
  expect(chat.requests[0]!.aborted).toBe(true);
  await chat.resolve();
  expect(screen.getByTestId("offered-suggestion").textContent).toBe("");
});

test("editing and clearing a draft retains the finished turn's suggestion", async () => {
  const chat = await openRunningChat();
  await chat.end();
  await chat.resolve();
  await waitFor(() =>
    expect(screen.getByTestId("offered-suggestion").textContent).toBe(
      "run the tests",
    ),
  );
  const draft = screen.getByLabelText("Suggestion draft");
  fireEvent.change(draft, { target: { value: "my follow-up" } });
  expect(screen.getByTestId("offered-suggestion").textContent).toBe(
    "run the tests",
  );
  fireEvent.change(draft, { target: { value: "" } });
  expect(screen.getByTestId("offered-suggestion").textContent).toBe(
    "run the tests",
  );
  expect(chat.requests).toHaveLength(1);
  await chat.start();
  expect(screen.getByTestId("offered-suggestion").textContent).toBe("");
});
