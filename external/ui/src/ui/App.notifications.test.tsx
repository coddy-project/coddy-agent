/**
 * System notifications from the whole app (issue #508): the events stream and
 * the turn's own stream reach the tracker, the tracker reaches the browser,
 * and a click on a notification opens its chat.
 */
import React from "react";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import { CODDY_UI_NOTIFY_COOKIE } from "./pwa/notifications";
import {
  installFakeNotification,
  restorePageAttention,
  setPageAttention,
  type FakeNotificationClass,
} from "./pwa/notifications.fakes";
import { TURN_END_SETTLE_MS } from "./pwa/attentionTracker";

const A = "sess_a";
const B = "sess_b";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

class ControlledStream {
  controller!: ReadableStreamDefaultController<Uint8Array>;
  closed = false;
  response: Response;
  constructor(signal?: AbortSignal | null) {
    this.response = new Response(
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
    signal?.addEventListener("abort", () => {
      if (this.closed) return;
      this.closed = true;
      this.controller.error(new DOMException("Aborted", "AbortError"));
    });
  }
  frame(event: string, data: unknown) {
    if (!this.closed)
      this.controller.enqueue(
        new TextEncoder().encode(
          `${event ? `event: ${event}\n` : ""}data: ${JSON.stringify(data)}\n\n`,
        ),
      );
  }
}

class Backend {
  activity = new Map<string, boolean>([
    [A, false],
    [B, false],
  ]);
  events: ControlledStream[] = [];
  relays: { sid: string; stream: ControlledStream }[] = [];
  fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = String(input);
    if (path === "/coddy/events") {
      const stream = new ControlledStream(init.signal);
      this.events.push(stream);
      return stream.response;
    }
    if (path.startsWith("/coddy/sessions?"))
      return json({
        sessions: [A, B].map((id) => ({
          id,
          title: `Chat ${id}`,
          turnActive: this.activity.get(id),
        })),
      });
    const match = path.match(/^\/coddy\/sessions\/([^/]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/activity")
        return json({
          sessionId: sid,
          turnActive: this.activity.get(sid) ?? false,
        });
      if (suffix === "/messages")
        return json({
          messages: [
            { role: "user", content: `Earlier ${sid} prompt` },
            { role: "assistant", content: `Earlier ${sid} answer` },
          ],
        });
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/queue") return json({ messages: [], version: 1 });
      if (suffix === "/composer-stream") {
        const stream = new ControlledStream(init.signal);
        this.relays.push({ sid, stream });
        return stream.response;
      }
      return json({});
    }
    if (path === "/v1/models")
      return json({ data: [{ id: "test-model", owned_by: "test" }] });
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path === "/coddy/workspace/context")
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  });
  get stream(): ControlledStream {
    return this.events.at(-1)!;
  }
}

let backend: Backend;
let Fake: FakeNotificationClass;

async function mount() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(backend.events.length).toBeGreaterThan(0));
  await act(async () => {
    backend.stream.frame("ready", { object: "coddy.events_ready" });
  });
}

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", `/#/s/${A}`);
  backend = new Backend();
  vi.stubGlobal("fetch", backend.fetch);
  Fake = installFakeNotification("granted");
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=on; Path=/`;
  setPageAttention(false);
});

afterEach(async () => {
  await act(async () => {
    cleanup();
  });
  vi.unstubAllGlobals();
  restorePageAttention();
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0`;
});

describe("notifications from the app", () => {
  test("a hidden tab says the turn of its chat ended, and a click opens the chat", async () => {
    await mount();
    // Another chat is on screen by the time the notice is clicked.
    await act(async () => {
      backend.stream.frame("turn_ended", {
        object: "coddy.turn_event",
        sessionId: A,
        phase: "ended",
        at: "2026-10-10T10:00:00Z",
      });
    });
    await waitFor(() => expect(Fake.instances).toHaveLength(1), {
      timeout: TURN_END_SETTLE_MS + 2000,
    });
    const shown = Fake.instances[0]!;
    expect(shown.title).toBe(`Chat ${A}`);
    expect(shown.options.body).toBe("The agent finished its turn.");
    expect(shown.options.tag).toContain(
      `:${A}:turn_finished:2026-10-10T10:00:00Z`,
    );

    await act(async () => {
      history.replaceState(null, "", `/#/s/${B}`);
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await waitFor(() => expect(window.location.hash).toBe(`#/s/${B}`));
    const focus = vi.spyOn(window, "focus").mockImplementation(() => {});
    await act(async () => {
      shown.onclick?.(new Event("click"));
    });
    await waitFor(() => expect(window.location.hash).toBe(`#/s/${A}`));
    focus.mockRestore();
  });

  test("a turn of a chat this tab has nothing to do with stays quiet", async () => {
    await mount();
    await act(async () => {
      backend.stream.frame("turn_ended", {
        object: "coddy.turn_event",
        sessionId: "sess_telegram_chat",
        phase: "ended",
        at: "2026-10-10T10:00:00Z",
      });
      await new Promise((r) => setTimeout(r, TURN_END_SETTLE_MS + 200));
    });
    expect(Fake.instances).toHaveLength(0);
  });

  test("nothing is said while the person looks at the page", async () => {
    setPageAttention(true);
    await mount();
    await act(async () => {
      backend.stream.frame("turn_ended", {
        object: "coddy.turn_event",
        sessionId: A,
        phase: "ended",
        at: "2026-10-10T10:00:00Z",
      });
      await new Promise((r) => setTimeout(r, TURN_END_SETTLE_MS + 200));
    });
    expect(Fake.instances).toHaveLength(0);
  });

  test("a permission request on the turn the tab shows is announced", async () => {
    backend.activity.set(A, true);
    await mount();
    await waitFor(() =>
      expect(backend.relays.some((r) => r.sid === A)).toBe(true),
    );
    const relay = backend.relays.find((r) => r.sid === A)!.stream;
    await act(async () => {
      relay.frame("permission", {
        sessionId: A,
        toolCall: {
          toolCallId: "call_1",
          title: "run_command: make test",
          kind: "execute",
        },
        options: [
          { optionId: "allow", name: "Allow", kind: "allow_once" },
          { optionId: "deny", name: "Deny", kind: "reject_once" },
        ],
      });
    });
    await waitFor(() => expect(Fake.instances).toHaveLength(1));
    expect(Fake.instances[0]!.options.body).toBe(
      "Permission needed: run_command: make test",
    );
    expect(Fake.instances[0]!.options.tag).toContain(`:${A}:permission:call_1`);
  });
});
