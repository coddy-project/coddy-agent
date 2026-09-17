import { afterEach, expect, test, vi } from "vitest";
import type { CoddyEnv } from "../env/remoteEnv";
import {
  FakeLocks,
  fakeChannels,
  fakeWorkers,
} from "./sharedServerEvents.fakes";
import {
  serverEventsScope,
  subscribeSharedServerEvents,
  type EventsTransports,
} from "./sharedServerEvents";

const encoder = new TextEncoder();
const realSleep = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));

type Connection = {
  url: string;
  headers: Headers;
  signal: AbortSignal;
  controller: ReadableStreamDefaultController<Uint8Array>;
};

/** GET /coddy/events as the server answers it, one controllable stream per connection. */
class EventsServer {
  connections: Connection[] = [];
  status = 200;
  fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (this.status !== 200)
      return new Response("refused", { status: this.status });
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start: (c) => {
        controller = c;
      },
    });
    const signal = init.signal ?? new AbortController().signal;
    signal.addEventListener("abort", () => {
      try {
        controller.error(new DOMException("Aborted", "AbortError"));
      } catch {
        // Already closed.
      }
    });
    this.connections.push({
      url: String(input),
      headers: new Headers(init.headers),
      signal,
      controller,
    });
    return new Response(body, { status: 200 });
  });
  open() {
    return this.connections.filter((c) => !c.signal.aborted);
  }
  frame(event: string, data: unknown, connection = this.open().at(-1)!) {
    connection.controller.enqueue(
      encoder.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`),
    );
  }
  /** What the server writes first on every connection: the active turns, then ready. */
  snapshot(active: string[], connection = this.open().at(-1)!) {
    for (const sessionId of active)
      this.frame("turn_started", { sessionId }, connection);
    this.frame("ready", { object: "coddy.events_ready" }, connection);
  }
}

const tabs: { close: () => void }[] = [];
afterEach(() => {
  for (const t of tabs.splice(0)) t.close();
});

function openTab(
  server: EventsServer,
  transports: EventsTransports,
  env: CoddyEnv = { mode: "local" },
  ackTimeoutMs?: number,
) {
  const ctl = new AbortController();
  const seen = {
    started: [] as string[],
    ended: [] as string[],
    queues: [] as string[],
    reloads: 0,
    ready: 0,
    states: [] as boolean[],
    refused: [] as number[],
  };
  const done = subscribeSharedServerEvents({
    env,
    onTurnStarted: (sid) => seen.started.push(sid),
    onTurnEnded: (sid) => seen.ended.push(sid),
    onMessageQueue: (sid) => seen.queues.push(sid),
    onConfigReloaded: () => seen.reloads++,
    onReady: () => seen.ready++,
    onConnectedChange: (connected) => seen.states.push(connected),
    onRefused: (status) => seen.refused.push(status),
    signal: ctl.signal,
    fetchImpl: server.fetch as unknown as typeof fetch,
    sleep: realSleep,
    transports,
    ...(ackTimeoutMs !== undefined ? { ackTimeoutMs } : {}),
  });
  const tab = { seen, done, close: () => ctl.abort() };
  tabs.push(tab);
  return tab;
}

const noTransports: EventsTransports = {
  sharedWorker: null,
  locks: null,
  channel: null,
};

test("tabs sharing a worker open one events connection and each hears every event", async () => {
  const server = new EventsServer();
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const first = openTab(server, transports);
  const second = openTab(server, transports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(1));
  expect(server.connections[0]!.url).toBe("/coddy/events");
  server.snapshot([]);
  server.frame("turn_started", { sessionId: "sess_a" });
  server.frame("config_reloaded", {});
  server.frame("message_queue", {
    sessionId: "sess_a",
    messages: [],
    version: 2,
  });
  server.frame("turn_ended", { sessionId: "sess_a" });
  for (const tab of [first, second]) {
    await vi.waitFor(() => expect(tab.seen.ended).toEqual(["sess_a"]));
    expect(tab.seen.started).toEqual(["sess_a"]);
    expect(tab.seen.queues).toEqual(["sess_a"]);
    expect(tab.seen.reloads).toBe(1);
    expect(tab.seen.ready).toBe(1);
    expect(tab.seen.states).toEqual([true]);
  }
  expect(server.connections).toHaveLength(1);
  expect(new Set(workers.names).size).toBe(1);
});

test("a tab joining a connected worker gets the active turns and ready addressed to it alone", async () => {
  const server = new EventsServer();
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const first = openTab(server, transports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(1));
  server.snapshot(["sess_a", "sess_b"]);
  server.frame("turn_ended", { sessionId: "sess_b" });
  await vi.waitFor(() => expect(first.seen.ended).toEqual(["sess_b"]));

  const late = openTab(server, transports);
  await vi.waitFor(() => expect(late.seen.ready).toBe(1));
  expect(late.seen.states).toEqual([true]);
  expect(late.seen.started).toEqual(["sess_a"]);
  await realSleep(10);
  expect(first.seen.ready).toBe(1);
  expect(first.seen.started).toEqual(["sess_a", "sess_b"]);
  expect(server.connections).toHaveLength(1);
});

test("a tab joining before the snapshot is complete waits for the server's own ready", async () => {
  const server = new EventsServer();
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const first = openTab(server, transports);
  await vi.waitFor(() => expect(first.seen.states).toEqual([true]));
  const late = openTab(server, transports);
  await vi.waitFor(() => expect(late.seen.states).toEqual([true]));
  server.snapshot(["sess_a"]);
  for (const tab of [first, late]) {
    await vi.waitFor(() => expect(tab.seen.ready).toBe(1));
    expect(tab.seen.started).toEqual(["sess_a"]);
  }
  await realSleep(10);
  expect(late.seen.ready).toBe(1);
});

test("a remote environment gets its own worker, with the base URL and the bearer token", async () => {
  const server = new EventsServer();
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const remote: CoddyEnv = {
    mode: "remote",
    baseUrl: "http://nas02:19980",
    token: "secret-token",
  };
  openTab(server, transports);
  openTab(server, transports, remote);
  await vi.waitFor(() => expect(server.connections).toHaveLength(2));
  const remoteConnection = server.connections.find((c) =>
    c.url.startsWith("http://nas02"),
  )!;
  expect(remoteConnection.url).toBe("http://nas02:19980/coddy/events");
  expect(remoteConnection.headers.get("Authorization")).toBe(
    "Bearer secret-token",
  );
  expect(new Set(workers.names).size).toBe(2);
  expect(workers.names.join(" ")).not.toContain("secret-token");
});

test("the scope separates environments and tokens without spelling the token", () => {
  const local = serverEventsScope({ mode: "local" });
  const remote = serverEventsScope({
    mode: "remote",
    baseUrl: "http://nas02:19980",
    token: "one",
  });
  const rotated = serverEventsScope({
    mode: "remote",
    baseUrl: "http://nas02:19980",
    token: "two",
  });
  expect(new Set([local, remote, rotated]).size).toBe(3);
  expect(remote).not.toContain("one");
});

test("the worker's stream closes once the last tab has left", async () => {
  const server = new EventsServer();
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const first = openTab(server, transports);
  const second = openTab(server, transports);
  await vi.waitFor(() => expect(server.open()).toHaveLength(1));
  first.close();
  await realSleep(10);
  expect(server.open()).toHaveLength(1);
  second.close();
  await vi.waitFor(() => expect(server.open()).toHaveLength(0));
  await first.done;
  await second.done;
});

test.each(["broken", "silent"] as const)(
  "a %s worker leaves the tab on a stream of its own",
  async (mode) => {
    const server = new EventsServer();
    const workers = fakeWorkers(server.fetch as unknown as typeof fetch, mode);
    const tab = openTab(
      server,
      { ...noTransports, sharedWorker: workers.factory },
      { mode: "local" },
      30,
    );
    await vi.waitFor(() => expect(server.connections).toHaveLength(1));
    server.snapshot(["sess_a"]);
    await vi.waitFor(() => expect(tab.seen.ready).toBe(1));
    expect(tab.seen.started).toEqual(["sess_a"]);
    expect(workers.hosts.size).toBe(0);
  },
);

test("a refused stream is reported to every tab of the worker", async () => {
  const server = new EventsServer();
  server.status = 401;
  const workers = fakeWorkers(server.fetch as unknown as typeof fetch);
  const transports = { ...noTransports, sharedWorker: workers.factory };
  const first = openTab(server, transports);
  const second = openTab(server, transports);
  for (const tab of [first, second])
    await vi.waitFor(() => expect(tab.seen.refused).toEqual([401]));
});

test("tabs without a worker elect one leader through locks, and followers hear its events", async () => {
  const server = new EventsServer();
  const channels = fakeChannels();
  const transports = {
    sharedWorker: null,
    locks: new FakeLocks(),
    channel: channels.create,
  };
  const leader = openTab(server, transports);
  const follower = openTab(server, transports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(1));
  server.snapshot([]);
  server.frame("turn_started", { sessionId: "sess_a" });
  for (const tab of [leader, follower]) {
    await vi.waitFor(() => expect(tab.seen.started).toEqual(["sess_a"]));
    expect(tab.seen.ready).toBe(1);
    expect(tab.seen.states).toEqual([true]);
  }
  expect(server.connections).toHaveLength(1);
});

test("a late follower gets the connection state, the active turns and ready addressed to it alone", async () => {
  const server = new EventsServer();
  const channels = fakeChannels();
  const transports = {
    sharedWorker: null,
    locks: new FakeLocks(),
    channel: channels.create,
  };
  const leader = openTab(server, transports);
  const early = openTab(server, transports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(1));
  server.snapshot(["sess_a"]);
  await vi.waitFor(() => expect(early.seen.ready).toBe(1));

  const late = openTab(server, transports);
  await vi.waitFor(() => expect(late.seen.ready).toBe(1));
  expect(late.seen.states).toEqual([true]);
  expect(late.seen.started).toEqual(["sess_a"]);
  await realSleep(10);
  expect(leader.seen.ready).toBe(1);
  expect(early.seen.ready).toBe(1);
  expect(early.seen.started).toEqual(["sess_a"]);
});

test("a new leader reports the stream down until its own is up, and a follower ignores the old leader's last word", async () => {
  const server = new EventsServer();
  const channels = fakeChannels();
  const transports = {
    sharedWorker: null,
    locks: new FakeLocks(),
    channel: channels.create,
  };
  const leader = openTab(server, transports);
  const successor = openTab(server, transports);
  const watcher = openTab(server, transports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(1));
  server.snapshot([]);
  for (const tab of [successor, watcher])
    await vi.waitFor(() => expect(tab.seen.ready).toBe(1));

  leader.close();
  await vi.waitFor(() => expect(server.connections).toHaveLength(2));
  expect(server.connections[0]!.signal.aborted).toBe(true);
  for (const tab of [successor, watcher]) {
    await vi.waitFor(() =>
      expect(tab.seen.states).toEqual([true, false, true]),
    );
    expect(tab.seen.ready).toBe(1);
  }
  server.snapshot(["sess_a"]);
  for (const tab of [successor, watcher]) {
    await vi.waitFor(() => expect(tab.seen.ready).toBe(2));
    expect(tab.seen.started).toEqual(["sess_a"]);
  }

  // A message the previous leader sent that arrives only now changes nothing.
  channels
    .create(serverEventsScope({ mode: "local" }))
    .postMessage({ kind: "connected", connected: false, term: 1 });
  await realSleep(10);
  expect(watcher.seen.states).toEqual([true, false, true]);
});

test("without a worker or locks every tab keeps a stream of its own", async () => {
  const server = new EventsServer();
  const first = openTab(server, noTransports);
  const second = openTab(server, noTransports);
  await vi.waitFor(() => expect(server.connections).toHaveLength(2));
  for (const connection of server.connections) server.snapshot([], connection);
  for (const tab of [first, second])
    await vi.waitFor(() => expect(tab.seen.ready).toBe(1));
});
