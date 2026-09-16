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

const A = "sess_a";
const B = "sess_b";
const HDR = "X-Coddy-Session-ID";
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

class ControlledStream {
  controller!: ReadableStreamDefaultController<Uint8Array>;
  closed = false;
  onClose?: () => void;
  response: Response;
  constructor(
    readonly signal?: AbortSignal | null,
    abortReads = true,
  ) {
    this.response = new Response(
      new ReadableStream<Uint8Array>({
        start: (controller) => {
          this.controller = controller;
        },
        cancel: () => {
          this.closed = true;
          this.onClose?.();
        },
      }),
      { headers: { "Content-Type": "text/event-stream" } },
    );
    if (abortReads)
      signal?.addEventListener("abort", () =>
        this.fail(new DOMException("Aborted", "AbortError")),
      );
  }
  frame(event: string, data: unknown) {
    if (!this.closed)
      this.controller.enqueue(
        new TextEncoder().encode(
          `${event ? `event: ${event}\n` : ""}data: ${JSON.stringify(data)}\n\n`,
        ),
      );
  }
  text(content: string) {
    this.frame("", { choices: [{ delta: { content } }] });
  }
  end() {
    if (this.closed) return;
    this.closed = true;
    this.controller.close();
    this.onClose?.();
  }
  fail(error = new TypeError("Connection lost")) {
    if (this.closed) return;
    this.closed = true;
    this.controller.error(error);
    this.onClose?.();
  }
}

type Queue = { messages: { id: string; text: string }[]; version: number };
type Request = { path: string; method: string; init: RequestInit };
class Backend {
  activity = new Map([
    [A, false],
    [B, false],
  ]);
  history = [A, B];
  queues = new Map<string, Queue>();
  messages = new Map<string, { role: string; content: string }[]>([
    [
      A,
      [
        { role: "user", content: "Earlier A prompt" },
        { role: "assistant", content: "Earlier A answer" },
      ],
    ],
    [
      B,
      [
        { role: "user", content: "Earlier B prompt" },
        { role: "assistant", content: "Earlier B answer" },
      ],
    ],
  ]);
  requests: Request[] = [];
  events: ControlledStream[] = [];
  posts: { sid: string; stream: ControlledStream }[] = [];
  relays: { sid: string; stream: ControlledStream }[] = [];
  abortPostReads = true;
  override?: (request: Request) => Response | Promise<Response> | undefined;
  /** Connections the browser keeps open to this host, shared by every tab of
   *  the profile: six over HTTP/1.1. A stream holds one until it closes or is
   *  aborted, a request beyond the limit waits for one to free up. Unlimited
   *  unless a test models the other tabs taking the rest. */
  connectionLimit = Infinity;
  connections = 0;
  private waiting: (() => void)[] = [];
  private streaming = new WeakSet<Response>();
  private connect(signal?: AbortSignal | null): Promise<void> | undefined {
    if (this.connections < this.connectionLimit) {
      this.connections++;
      return undefined;
    }
    return new Promise((resolve, reject) => {
      const grant = () => {
        signal?.removeEventListener("abort", drop);
        this.connections++;
        resolve();
      };
      const drop = () => {
        this.waiting = this.waiting.filter((w) => w !== grant);
        reject(new DOMException("Aborted", "AbortError"));
      };
      this.waiting.push(grant);
      signal?.addEventListener("abort", drop, { once: true });
    });
  }
  private disconnect() {
    this.connections--;
    if (this.connections < this.connectionLimit) this.waiting.shift()?.();
  }
  private hold(stream: ControlledStream, signal?: AbortSignal | null) {
    let open = true;
    const close = () => {
      if (!open) return;
      open = false;
      this.disconnect();
    };
    stream.onClose = close;
    signal?.addEventListener("abort", close, { once: true });
    this.streaming.add(stream.response);
    return stream;
  }
  fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const request = { path: String(input), method: init.method ?? "GET", init };
    const connecting = this.connect(init.signal);
    if (connecting) await connecting;
    this.requests.push(request);
    let response: Response | Promise<Response>;
    try {
      response = this.respond(request);
    } catch (err) {
      this.disconnect();
      throw err;
    }
    if (response instanceof Response) {
      if (!this.streaming.has(response)) this.disconnect();
      return response;
    }
    return response.finally(() => this.disconnect());
  });
  private respond(request: Request): Response | Promise<Response> {
    const { path, init } = request;
    const overridden = this.override?.(request);
    if (overridden) return overridden;
    if (path === "/coddy/events") {
      const stream = this.hold(new ControlledStream(init.signal), init.signal);
      this.events.push(stream);
      return stream.response;
    }
    if (path === "/v1/responses") {
      const sid = new Headers(init.headers).get(HDR)!;
      this.activity.set(sid, true);
      const body = JSON.parse(String(init.body));
      this.messages.get(sid)?.push({ role: "user", content: body.input });
      const stream = this.hold(
        new ControlledStream(init.signal, this.abortPostReads),
        init.signal,
      );
      this.posts.push({ sid, stream });
      return stream.response;
    }
    if (path.startsWith("/coddy/sessions?"))
      return json({
        sessions: this.history.map((id) => ({
          id,
          title: `Chat ${id}`,
          turnActive: this.activity.get(id),
        })),
      });
    const match = path.match(/^\/coddy\/sessions\/([^/]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2];
      if (!suffix) return json({});
      if (suffix === "/activity")
        return json({
          sessionId: sid,
          turnActive: this.activity.get(sid) ?? false,
        });
      if (suffix === "/messages")
        return json({ messages: this.messages.get(sid) ?? [] });
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/branches") return json({ branchPoints: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/composer-stream") {
        const stream = this.hold(new ControlledStream(init.signal), init.signal);
        this.relays.push({ sid, stream });
        return stream.response;
      }
      if (suffix === "/cancel") return json({});
      if (suffix === "/queue") {
        const queue = this.queues.get(sid) ?? { messages: [], version: 1 };
        if (request.method === "POST") {
          const next = {
            messages: [
              ...queue.messages,
              {
                id: `q_${queue.version}`,
                text: JSON.parse(String(init.body)).text,
              },
            ],
            version: queue.version + 1,
          };
          this.queues.set(sid, next);
          return json(next, 201);
        }
        return json(queue);
      }
    }
    if (path === "/v1/models")
      return json({ data: [{ id: "test-model", owned_by: "test" }] });
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path === "/coddy/workspace/context")
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  }
  count(path: string, method = "GET") {
    return this.requests.filter((r) => r.path === path && r.method === method)
      .length;
  }
  turn(sid: string, active: boolean) {
    this.activity.set(sid, active);
    this.events
      .at(-1)!
      .frame(active ? "turn_started" : "turn_ended", { sessionId: sid });
  }
  queue(sid: string, queue: Queue) {
    this.queues.set(sid, queue);
    this.events.at(-1)!.frame("message_queue", { sessionId: sid, ...queue });
  }
}
let backend: Backend;
beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", `/#/s/${A}`);
  backend = new Backend();
  vi.stubGlobal("fetch", backend.fetch);
});
afterEach(async () => {
  await act(async () => {
    cleanup();
    for (const { stream } of [...backend.posts, ...backend.relays])
      stream.fail(new DOMException("Aborted", "AbortError"));
    for (const stream of backend.events)
      stream.fail(new DOMException("Aborted", "AbortError"));
  });
  vi.unstubAllGlobals();
});
async function mount() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByText("Earlier A prompt");
  await screen.findByRole("textbox", { name: "Message" });
}
const composer = () => screen.getByRole("textbox", { name: "Message" });
const stop = () => screen.getByRole("button", { name: "Stop generation" });
async function send(text: string) {
  fireEvent.change(composer(), { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(backend.posts.length).toBeGreaterThan(0));
}
async function navigate(sid: string) {
  await act(async () => {
    history.replaceState(null, "", `/#/s/${sid}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await screen.findByText(sid === A ? "Earlier A prompt" : "Earlier B prompt");
}

test.each(["post", "relay"])(
  "a late provider window replaces the model-list fallback on the %s stream",
  async (transport) => {
    document.cookie = "coddy_llm_model=test-model; Path=/";
    backend.override = (r) => {
      if (r.path === "/v1/models")
        return json({
          data: [
            { id: "test-model", owned_by: "test", max_context_tokens: 128000 },
            { id: "other-model", owned_by: "test", max_context_tokens: 512000 },
          ],
        });
      if (r.path.endsWith("/stats"))
        return json({
          stats: { contextBreakdown: { estimatedTotal: 120000 } },
        });
      return undefined;
    };
    if (transport === "relay") backend.activity.set(A, true);
    await mount();
    const expectWindow = async (size: number) => {
      await waitFor(() => {
        const ring = document.querySelector(".context-ring-fg")!;
        const offset = Number(ring.getAttribute("stroke-dashoffset"));
        expect(offset).toBeCloseTo(2 * Math.PI * 12 * (1 - 120000 / size), 1);
      });
    };
    await expectWindow(128000);
    if (transport === "post") await send("Continue A");
    else await waitFor(() => expect(backend.relays).toHaveLength(1));
    const stream = (transport === "post" ? backend.posts : backend.relays)[0]!
      .stream;
    // The listing missed the HTTP wait deadline. A later usage update carries
    // the provider's resolved window without another /v1/models request.
    await act(async () =>
      stream.frame("usage_update", { used: 120000, size: 262144 }),
    );
    await expectWindow(262144);
    const statsReads = backend.count(`/coddy/sessions/${A}/stats`);
    fireEvent.click(screen.getByTestId("composer-context-ring-host"));
    await waitFor(() =>
      expect(backend.count(`/coddy/sessions/${A}/stats`)).toBeGreaterThan(
        statsReads,
      ),
    );
    await expectWindow(262144);
    expect(backend.count("/v1/models")).toBe(1);

    await navigate(B);
    await expectWindow(128000);
    await act(async () =>
      stream.frame("usage_update", { used: 120000, size: 524288 }),
    );
    await expectWindow(128000);
    await navigate(A);
    await expectWindow(262144);
    await act(async () => {
      backend.turn(A, false);
      stream.end();
    });
    fireEvent.click(screen.getByRole("button", { name: "Model" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "other-model" }));
    await expectWindow(512000);
    document.cookie = "coddy_llm_model=; Path=/; Max-Age=0";
  },
);

test("an active session outside the History page gets Stop and queues text, not a new POST", async () => {
  backend.history = [B];
  backend.activity.set(A, true);
  await mount();
  await waitFor(() => expect(stop()).toBeEnabled());
  fireEvent.change(composer(), { target: { value: "Follow up A" } });
  fireEvent.click(screen.getByRole("button", { name: "Queue this message" }));
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/queue`, "POST")).toBe(1),
  );
  expect(backend.posts).toHaveLength(0);
  expect(await screen.findByTestId("composer-queue")).toHaveTextContent(
    "Follow up A",
  );
});

test.each(["http", "eof", "network", "no-live-turn"])(
  "relay %s does not make an active server turn idle",
  async (failure) => {
    backend.activity.set(A, true);
    if (failure === "http")
      backend.override = (r) =>
        r.path.endsWith("/composer-stream") ? json({}, 503) : undefined;
    await mount();
    if (failure !== "http") {
      await waitFor(() => expect(backend.relays).toHaveLength(1));
      await act(async () => {
        const relay = backend.relays[0]!.stream;
        if (failure === "eof") relay.end();
        else if (failure === "network") relay.fail();
        else
          relay.frame("error", {
            error: { code: "no_active_turn", message: "No live turn" },
          });
      });
    }
    await waitFor(() =>
      expect(backend.count(`/coddy/sessions/${A}/messages`)).toBeGreaterThan(1),
    );
    expect(stop()).toBeEnabled();
    fireEvent.change(composer(), { target: { value: "Still working" } });
    expect(
      screen.getByRole("button", { name: "Queue this message" }),
    ).toBeEnabled();
  },
);

test("late attach hydrates the queue and ready after reconnect reconciles a missed turn end", async () => {
  backend.activity.set(A, true);
  backend.queues.set(A, {
    messages: [{ id: "q_saved", text: "Already queued" }],
    version: 7,
  });
  await mount();
  expect(await screen.findByTestId("composer-queue")).toHaveTextContent(
    "Already queued",
  );
  await act(async () => {
    backend.events[0]!.end();
  });
  backend.activity.set(A, false);
  backend.queues.set(A, { messages: [], version: 8 });
  await waitFor(() => expect(backend.events).toHaveLength(2), {
    timeout: 2500,
  });
  await act(async () => {
    backend.events[1]!.frame("ready", { object: "coddy.events_ready" });
  });
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Stop generation" }),
    ).not.toBeInTheDocument(),
  );
  expect(screen.queryByTestId("composer-queue")).not.toBeInTheDocument();
});

test("a stale activity/queue snapshot cannot override newer server events", async () => {
  const activity = deferred<Response>();
  const queue = deferred<Response>();
  backend.override = (r) =>
    r.path.endsWith("/activity")
      ? activity.promise
      : r.path.endsWith("/queue")
        ? queue.promise
        : undefined;
  await mount();
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/activity`)).toBeGreaterThan(0),
  );
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/queue`)).toBeGreaterThan(0),
  );
  await act(async () => {
    backend.turn(A, true);
    backend.queue(A, {
      messages: [{ id: "q_new", text: "Newer queue" }],
      version: 9,
    });
  });
  await waitFor(() => expect(stop()).toBeEnabled());
  await act(async () => {
    activity.resolve(json({ sessionId: A, turnActive: false }));
    queue.resolve(
      json({ messages: [{ id: "q_old", text: "Stale queue" }], version: 8 }),
    );
  });
  expect(stop()).toBeEnabled();
  expect(screen.getByTestId("composer-queue")).toHaveTextContent("Newer queue");
  expect(screen.queryByText("Stale queue")).not.toBeInTheDocument();
});

test.each(["post", "relay"])(
  "Stop reaches the server while other tabs hold every other connection to the host, through this tab's %s",
  async (transport) => {
    if (transport === "relay") backend.activity.set(A, true);
    await mount();
    if (transport === "post") await send("Own A turn");
    else await waitFor(() => expect(backend.relays).toHaveLength(1));
    const stream = (transport === "post" ? backend.posts : backend.relays)[0]!
      .stream;
    await act(async () => {
      stream.text("Partial answer A");
    });
    await screen.findByText("Partial answer A");
    // Other tabs of this server hold the rest of the six connections a browser
    // keeps to a host; this tab holds its events stream and the turn stream.
    backend.connectionLimit = backend.connections;
    fireEvent.click(stop());
    await waitFor(() =>
      expect(backend.count(`/coddy/sessions/${A}/cancel`, "POST")).toBe(1),
    );
    expect(stream.signal!.aborted).toBe(true);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("Partial answer A")).toBeInTheDocument();
  },
);

test("a failed cancel rejoins the running turn and offers a visible retryable Stop error", async () => {
  backend.override = (r) =>
    r.path.endsWith("/cancel")
      ? json({ error: { message: "Unavailable" } }, 503)
      : undefined;
  await mount();
  await send("Own A turn");
  await act(async () => {
    backend.posts[0]!.stream.text("Partial answer A");
  });
  fireEvent.click(stop());
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/cancel`, "POST")).toBe(1),
  );
  expect(backend.posts[0]!.stream.signal!.aborted).toBe(true);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /stop.*try again/i,
  );
  // The turn Stop did not take is watched again through the relay.
  await waitFor(() => expect(backend.relays).toHaveLength(1));
  expect(backend.relays[0]!.stream.signal!.aborted).toBe(false);
  expect(stop()).toBeEnabled();
  fireEvent.click(stop());
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/cancel`, "POST")).toBe(2),
  );
  expect(backend.relays[0]!.stream.signal!.aborted).toBe(true);
  expect(screen.getByText("Partial answer A")).toBeInTheDocument();
});

test("Stop releases its own stream at once, targets its original session and preserves partial text", async () => {
  const cancel = deferred<Response>();
  backend.override = (r) =>
    r.path.endsWith("/cancel") ? cancel.promise : undefined;
  await mount();
  await send("Own A turn");
  await act(async () => {
    backend.posts[0]!.stream.text("Partial answer A");
  });
  fireEvent.click(stop());
  expect(backend.posts[0]!.stream.signal!.aborted).toBe(true);
  // No relay takes the released stream's place while the answer is pending.
  await new Promise((resolve) => setTimeout(resolve, 2300));
  expect(backend.relays).toHaveLength(0);
  await navigate(B);
  await send("Own B turn");
  await act(async () => {
    cancel.resolve(json({}));
  });
  expect(backend.posts[0]!.stream.signal!.aborted).toBe(true);
  expect(backend.posts[1]!.stream.signal!.aborted).toBe(false);
  const request = backend.requests.find((r) => r.path.endsWith("/cancel"))!;
  expect(request.path).toBe(`/coddy/sessions/${A}/cancel`);
  expect(new Headers(request.init.headers).get(HDR)).toBe(A);
  expect(screen.queryByText("Partial answer A")).not.toBeInTheDocument();
  await navigate(A);
  expect(await screen.findByText("Partial answer A")).toBeInTheDocument();
  expect(stop()).toBeEnabled(); // An acknowledgement is not a released turn.
});

test("a late Stop acknowledgement and old POST completion cannot reset a newer stream", async () => {
  const cancel = deferred<Response>();
  backend.abortPostReads = false;
  backend.override = (r) =>
    r.path.endsWith("/cancel") ? cancel.promise : undefined;
  await mount();
  await send("First turn");
  fireEvent.click(stop());
  await act(async () => {
    backend.turn(A, false);
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument(),
  );
  await send("Second turn");
  await waitFor(() => expect(backend.posts).toHaveLength(2));
  await act(async () => {
    backend.posts[1]!.stream.text("New stream text");
  });
  await act(async () => {
    cancel.resolve(json({}));
    backend.posts[0]!.stream.fail(new DOMException("Aborted", "AbortError"));
  });
  expect(backend.posts[1]!.stream.signal!.aborted).toBe(false);
  expect(stop()).toBeEnabled();
  expect(screen.getByText("New stream text")).toBeInTheDocument();
  expect(backend.relays).toHaveLength(0); // An own POST must not also be watched.
});

test("a rejected queue request never restores its draft into another session", async () => {
  const queued = deferred<Response>();
  backend.activity.set(A, true);
  backend.override = (r) =>
    r.path.endsWith("/queue") && r.method === "POST"
      ? queued.promise
      : undefined;
  await mount();
  await waitFor(() => expect(stop()).toBeEnabled());
  fireEvent.change(composer(), { target: { value: "A follow-up" } });
  fireEvent.click(screen.getByRole("button", { name: "Queue this message" }));
  await navigate(B);
  fireEvent.change(composer(), { target: { value: "B draft" } });
  await act(async () => {
    queued.resolve(json({ error: { code: "queue_full" } }, 409));
  });
  expect(composer()).toHaveValue("B draft");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

test("relay EOF preserves partial text when persistence still has only the previous answer", async () => {
  backend.activity.set(A, true);
  await mount();
  await waitFor(() => expect(backend.relays).toHaveLength(1));
  await act(async () => {
    backend.relays[0]!.stream.text("Unpersisted relay answer");
  });
  expect(screen.getByText("Unpersisted relay answer")).toBeInTheDocument();
  await act(async () => {
    backend.relays[0]!.stream.end();
  });
  expect(screen.getByText("Unpersisted relay answer")).toBeInTheDocument();
  expect(stop()).toBeEnabled();
});

test("reconciliation does not repeatedly abort a slow activity read", async () => {
  const activity = deferred<Response>();
  backend.override = (r) =>
    r.path.endsWith("/activity") ? activity.promise : undefined;
  await mount();
  const request = backend.requests.find((r) => r.path.endsWith("/activity"))!;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2100));
  });
  expect(request.init.signal!.aborted).toBe(false);
  await act(async () => {
    activity.resolve(json({ sessionId: A, turnActive: true }));
  });
  expect(stop()).toBeEnabled();
});

test("a slow queue read cannot block recovery from a missed turn end", async () => {
  const queue = deferred<Response>();
  backend.activity.set(A, true);
  backend.override = (r) =>
    r.path.endsWith("/queue") ? queue.promise : undefined;
  await mount();
  await waitFor(() => expect(stop()).toBeEnabled());
  backend.activity.set(A, false);
  await waitFor(
    () =>
      expect(
        screen.queryByRole("button", { name: "Stop generation" }),
      ).not.toBeInTheDocument(),
    { timeout: 3000 },
  );
  await act(async () => {
    queue.resolve(json({ messages: [], version: 2 }));
  });
});

test("a cancel network error rewatches the turn through a new relay and a retry can acknowledge it", async () => {
  backend.activity.set(A, true);
  backend.override = (r) => {
    if (r.path.endsWith("/cancel")) throw new TypeError("Offline");
    return undefined;
  };
  await mount();
  await waitFor(() => expect(backend.relays).toHaveLength(1));
  fireEvent.click(stop());
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /stop.*try again/i,
  );
  expect(backend.relays[0]!.stream.signal!.aborted).toBe(true);
  await waitFor(() => expect(backend.relays).toHaveLength(2));
  expect(backend.relays[1]!.stream.signal!.aborted).toBe(false);
  delete backend.override;
  fireEvent.click(stop());
  await waitFor(() =>
    expect(backend.relays[1]!.stream.signal!.aborted).toBe(true),
  );
  expect(stop()).toBeEnabled();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

test("an acknowledged Stop eventually becomes idle if the turn-ended event was missed", async () => {
  await mount();
  await send("Stop this turn");
  fireEvent.click(stop());
  await waitFor(() =>
    expect(backend.posts[0]!.stream.signal!.aborted).toBe(true),
  );
  expect(stop()).toBeEnabled();
  backend.activity.set(A, false); // No turn_ended frame reaches this browser.
  await waitFor(
    () =>
      expect(
        screen.queryByRole("button", { name: "Stop generation" }),
      ).not.toBeInTheDocument(),
    { timeout: 3000 },
  );
  expect(backend.relays).toHaveLength(0);
});

test("turn-start replay and ready cannot attach a watcher beside this page's own POST", async () => {
  await mount();
  await send("Own turn");
  await act(async () => {
    backend.turn(A, true);
    backend.events[0]!.frame("ready", { object: "coddy.events_ready" });
    backend.posts[0]!.stream.text("One live answer");
  });
  expect(backend.relays).toHaveLength(0);
  expect(screen.getAllByText("One live answer")).toHaveLength(1);
});

test("replayed turn-start events do not reset the queue version high-water mark", async () => {
  backend.activity.set(A, true);
  await mount();
  await act(async () => {
    backend.queue(A, {
      messages: [{ id: "q", text: "Read already" }],
      version: 10,
    });
  });
  expect(screen.getByTestId("composer-queue")).toHaveTextContent(
    "Read already",
  );
  await act(async () => {
    backend.queue(A, { messages: [], version: 11 });
    backend.turn(A, true);
    backend.events[0]!.frame("message_queue", {
      sessionId: A,
      messages: [{ id: "q", text: "Read already" }],
      version: 10,
    });
  });
  expect(screen.queryByTestId("composer-queue")).not.toBeInTheDocument();
});

test("unseen off-screen turn events do not fetch remote transcripts", async () => {
  await mount();
  await act(async () => {
    backend.turn(B, true);
    backend.turn(B, false);
  });
  expect(backend.count(`/coddy/sessions/${B}/messages`)).toBe(0);
  expect(backend.relays).toHaveLength(0);
});

test("a stale active snapshot cannot resurrect an ended turn", async () => {
  const activity = deferred<Response>();
  let reads = 0;
  backend.override = (r) => {
    if (r.path.endsWith("/activity") && reads++ === 0) return activity.promise;
    return undefined;
  };
  await mount();
  await act(async () => {
    backend.turn(A, true);
  });
  await waitFor(() => expect(stop()).toBeEnabled());
  await act(async () => {
    backend.turn(A, false);
    activity.resolve(json({ sessionId: A, turnActive: true }));
  });
  expect(
    screen.queryByRole("button", { name: "Stop generation" }),
  ).not.toBeInTheDocument();
});

test("a failed own stream stays queueable until server activity confirms release", async () => {
  await mount();
  await send("Own turn");
  await act(async () => {
    backend.posts[0]!.stream.fail();
  });
  expect(stop()).toBeEnabled();
  fireEvent.change(composer(), {
    target: { value: "Follow up after disconnect" },
  });
  expect(
    screen.getByRole("button", { name: "Queue this message" }),
  ).toBeEnabled();
});

test.each(["post", "relay"])(
  "a restarted queue recovers lower versions and fences the old %s stream",
  async (kind) => {
    backend.activity.set(A, kind === "relay");
    await mount();
    if (kind === "post") await send("Old own turn");
    else await waitFor(() => expect(backend.relays).toHaveLength(1));
    const oldStream = (kind === "post" ? backend.posts : backend.relays)[0]!
      .stream;
    await act(async () => {
      backend.queue(A, {
        messages: [{ id: "old", text: "Before restart" }],
        version: 100,
      });
      backend.events[0]!.end();
    });
    backend.queues.set(A, {
      messages: [{ id: "new", text: "After restart" }],
      version: 1,
    });
    await waitFor(() => expect(backend.events).toHaveLength(2), {
      timeout: 2500,
    });
    await act(async () => {
      backend.events[1]!.frame("ready", {});
    });
    expect(screen.getByTestId("composer-queue")).toHaveTextContent(
      "After restart",
    );
    await act(async () => {
      backend.queue(A, {
        messages: [{ id: "next", text: "New process update" }],
        version: 2,
      });
      oldStream.frame("message_queue", {
        messages: [{ id: "old", text: "Old stream update" }],
        version: 101,
      });
    });
    expect(screen.getByTestId("composer-queue")).toHaveTextContent(
      "New process update",
    );
    expect(screen.queryByText("Old stream update")).not.toBeInTheDocument();
  },
);

test("same-server ready and turn-start replay still reject older queue frames", async () => {
  backend.activity.set(A, true);
  backend.queues.set(A, { messages: [], version: 100 });
  await mount();
  await act(async () => {
    backend.turn(A, true);
    backend.events[0]!.frame("ready", {});
  });
  await act(async () => {
    backend.events[0]!.frame("message_queue", {
      sessionId: A,
      messages: [{ id: "old", text: "Already consumed" }],
      version: 99,
    });
  });
  expect(screen.queryByTestId("composer-queue")).not.toBeInTheDocument();
});

test.each(["POST", "DELETE", "GET"])(
  "a delayed old queue %s reply cannot overwrite restart recovery",
  async (method) => {
    backend.activity.set(A, true);
    backend.queues.set(A, {
      messages: [{ id: "old", text: "Old queued message" }],
      version: 100,
    });
    await mount();
    await screen.findByText("Old queued message");
    const reply = deferred<Response>();
    let held = false;
    backend.override = (r) => {
      if (!held && r.path.includes("/queue") && r.method === method) {
        held = true;
        return reply.promise;
      }
      return undefined;
    };
    if (method === "POST") {
      fireEvent.change(composer(), { target: { value: "Old follow-up" } });
      fireEvent.click(
        screen.getByRole("button", { name: "Queue this message" }),
      );
    } else if (method === "DELETE") {
      fireEvent.click(screen.getByTestId("composer-queue-remove-old"));
    } else {
      await act(async () => {
        backend.events[0]!.frame("ready", {});
      });
    }
    await waitFor(() => expect(held).toBe(true));
    backend.queues.set(A, {
      messages: [{ id: "new", text: "Recovered queue" }],
      version: 1,
    });
    await act(async () => {
      backend.events[0]!.frame("ready", {});
    });
    expect(screen.getByTestId("composer-queue")).toHaveTextContent(
      "Recovered queue",
    );
    await act(async () => {
      reply.resolve(
        json(
          {
            messages: [{ id: "old", text: "Delayed old reply" }],
            version: 101,
          },
          method === "POST" ? 201 : 200,
        ),
      );
    });
    expect(screen.getByTestId("composer-queue")).toHaveTextContent(
      "Recovered queue",
    );
    expect(screen.queryByText("Delayed old reply")).not.toBeInTheDocument();
  },
);

test("a lower snapshot crossed by a newer pushed queue waits for a fresh read", async () => {
  backend.activity.set(A, true);
  backend.queues.set(A, {
    messages: [{ id: "old", text: "Pre-restart queue" }],
    version: 100,
  });
  await mount();
  const reply = deferred<Response>();
  backend.override = (r) =>
    r.path.endsWith("/queue") ? reply.promise : undefined;
  await act(async () => {
    backend.events[0]!.frame("ready", {});
  });
  await act(async () => {
    backend.queue(A, {
      messages: [{ id: "new", text: "Latest restarted queue" }],
      version: 2,
    });
    reply.resolve(
      json({
        messages: [{ id: "stale", text: "Crossed snapshot" }],
        version: 1,
      }),
    );
  });
  expect(screen.queryByText("Crossed snapshot")).not.toBeInTheDocument();
  delete backend.override;
  await act(async () => {
    backend.events[0]!.frame("ready", {});
  });
  expect(screen.getByTestId("composer-queue")).toHaveTextContent(
    "Latest restarted queue",
  );
});

test("an old turn end cannot unlock a pending own POST, but its real end releases a wedged reader", async () => {
  await mount();
  const admitted = deferred<Response>();
  backend.override = (r) => {
    if (r.path !== "/v1/responses") return undefined;
    backend.posts.push({ sid: A, stream: new ControlledStream(r.init.signal) });
    return admitted.promise;
  };
  await send("Pending new turn");
  await act(async () => {
    // The preceding turn ended; the new request has not been admitted yet.
    backend.events[0]!.frame("turn_ended", { sessionId: A });
  });
  expect(stop()).toBeEnabled();
  fireEvent.change(composer(), {
    target: { value: "Follow-up while admitting" },
  });
  expect(
    screen.getByRole("button", { name: "Queue this message" }),
  ).toBeEnabled();
  fireEvent.change(composer(), { target: { value: "" } });
  await navigate(B);
  expect(
    screen.queryByRole("button", { name: "Stop generation" }),
  ).not.toBeInTheDocument();
  const reads = backend.count(`/coddy/sessions/${A}/activity`);
  await act(async () => {
    backend.activity.set(A, true);
    admitted.resolve(backend.posts[0]!.stream.response);
  });
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${A}/activity`)).toBeGreaterThan(
      reads,
    ),
  );
  expect(
    screen.queryByRole("button", { name: "Stop generation" }),
  ).not.toBeInTheDocument();
  await navigate(A);
  expect(stop()).toBeEnabled();
  await act(async () => {
    backend.turn(A, false);
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument(),
  );
  expect(backend.posts[0]!.stream.closed).toBe(false);
});

test("an old turn end waits for authoritative activity before unlocking an admitted own POST", async () => {
  await mount();
  await send("New admitted turn");
  const activity = deferred<Response>();
  backend.override = (r) =>
    r.path.endsWith("/activity") ? activity.promise : undefined;
  await act(async () => {
    // Unlike a real end, this delayed event disagrees with current server activity.
    backend.events[0]!.frame("turn_ended", { sessionId: A });
  });
  expect(stop()).toBeEnabled();
  await act(async () => {
    activity.resolve(json({ sessionId: A, turnActive: true }));
  });
  expect(stop()).toBeEnabled();
  expect(backend.posts[0]!.stream.signal!.aborted).toBe(false);
});

test("a first send does not hydrate the queue before local POST admission", async () => {
  history.replaceState(null, "", "/");
  const admitted = deferred<Response>();
  backend.override = (r) => {
    if (r.path !== "/v1/responses") return undefined;
    const sid = new Headers(r.init.headers).get(HDR)!;
    backend.posts.push({ sid, stream: new ControlledStream(r.init.signal) });
    return admitted.promise;
  };
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByRole("textbox", { name: "Message" });
  await send("First prompt");
  const { sid, stream } = backend.posts[0]!;
  await act(async () => {
    backend.events[0]!.frame("ready", {});
  });
  expect(backend.count(`/coddy/sessions/${sid}/activity`)).toBe(0);
  expect(backend.count(`/coddy/sessions/${sid}/queue`)).toBe(0);
  await act(async () => {
    backend.activity.set(sid, true);
    admitted.resolve(stream.response);
  });
  await waitFor(() =>
    expect(backend.count(`/coddy/sessions/${sid}/queue`)).toBeGreaterThan(0),
  );
});

test("an observer keeps Stop when an unpaired end is followed by a failed activity read", async () => {
  backend.activity.set(A, true);
  await mount();
  await waitFor(() => expect(stop()).toBeEnabled());
  let failNext = true;
  backend.override = (r) => {
    if (r.path === `/coddy/sessions/${A}/activity` && failNext) {
      failNext = false;
      return json({}, 503);
    }
    return undefined;
  };
  const reads = backend.count(`/coddy/sessions/${A}/activity`);
  await act(async () => {
    backend.events[0]!.frame("turn_ended", { sessionId: A });
  });
  await waitFor(() => expect(failNext).toBe(false));
  expect(stop()).toBeEnabled();
  await waitFor(
    () => expect(backend.count(`/coddy/sessions/${A}/activity`)).toBeGreaterThan(reads + 1),
    { timeout: 3000 },
  );
  fireEvent.change(composer(), { target: { value: "Follow the current turn" } });
  expect(screen.getByRole("button", { name: "Queue this message" })).toBeEnabled();
});

test.each(["turn_started", "ready"])(
  "an observer rejoins after Stop and %s without seeing the idle edge",
  async (event) => {
    backend.activity.set(A, true);
    await mount();
    await waitFor(() => expect(backend.relays).toHaveLength(1));
    fireEvent.click(stop());
    await waitFor(() => expect(backend.relays[0]!.stream.signal!.aborted).toBe(true));
    await act(async () => {
      // A successor is already active when the missed-idle stream recovers.
      backend.events[0]!.frame(event, event === "ready" ? {} : { sessionId: A });
    });
    await waitFor(() => expect(backend.relays.length).toBeGreaterThan(1), { timeout: 3000 });
    expect(backend.relays.at(-1)!.stream.signal!.aborted).toBe(false);
    expect(stop()).toBeEnabled();
  },
);

test.each(["turn_started", "ready"])(
  "a late Stop acknowledgement cannot fence an observer recovered by %s",
  async (event) => {
    const cancel = deferred<Response>();
    backend.activity.set(A, true);
    backend.override = (r) =>
      r.path.endsWith("/cancel") ? cancel.promise : undefined;
    await mount();
    await waitFor(() => expect(backend.relays).toHaveLength(1));
    fireEvent.click(stop());
    await act(async () => {
      backend.events[0]!.frame(event, event === "ready" ? {} : { sessionId: A });
    });
    await act(async () => {
      cancel.resolve(json({}));
    });
    expect(backend.relays[0]!.stream.signal!.aborted).toBe(true);
    await waitFor(() => expect(backend.relays).toHaveLength(2), { timeout: 3000 });
    expect(backend.relays[1]!.stream.signal!.aborted).toBe(false);
    expect(stop()).toBeEnabled();
  },
);

test("a hung Stop times out, keeps the turn watched and becomes retryable", async () => {
  await mount();
  await send("Keep this turn readable");
  backend.override = (r) => {
    if (!r.path.endsWith("/cancel")) return undefined;
    return new Promise<Response>((_resolve, reject) => {
      r.init.signal?.addEventListener("abort", () => reject(new DOMException("Timed out", "AbortError")));
    });
  };
  vi.useFakeTimers();
  try {
    fireEvent.click(stop());
    await act(async () => { await vi.advanceTimersByTimeAsync(5100); });
    expect(screen.getByRole("alert")).toHaveTextContent(/stop.*try again/i);
    expect(backend.posts[0]!.stream.signal!.aborted).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(backend.relays).toHaveLength(1);
    expect(backend.relays[0]!.stream.signal!.aborted).toBe(false);
    delete backend.override;
    await act(async () => { fireEvent.click(stop()); });
    expect(backend.count(`/coddy/sessions/${A}/cancel`, "POST")).toBe(2);
    expect(backend.relays[0]!.stream.signal!.aborted).toBe(true);
  } finally {
    vi.useRealTimers();
  }
});
