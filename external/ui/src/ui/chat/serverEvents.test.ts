import { expect, test, vi } from "vitest";
import { subscribeServerEvents } from "./serverEvents";

function streamOf(text: string): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text));
      controller.close();
    },
  });
}

function responseOf(text: string): Response {
  return { ok: true, status: 200, body: streamOf(text) } as unknown as Response;
}

const turnEvent = (name: string, sessionId: string) =>
  `event: ${name}\ndata: ${JSON.stringify({
    object: "coddy.turn_event",
    sessionId,
    phase: name === "turn_started" ? "started" : "ended",
    at: "2026-08-10T12:00:00Z",
  })}\n\n`;

test("turn events are reported per session", async () => {
  const started: string[] = [];
  const ended: string[] = [];
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () =>
    responseOf(
      "retry: 3000\n\n" +
        `event: ready\ndata: {"object":"coddy.events_ready"}\n\n` +
        turnEvent("turn_started", "sess_a") +
        turnEvent("turn_ended", "sess_a"),
    ),
  );

  await subscribeServerEvents({
    onTurnStarted: (sid) => started.push(sid),
    onTurnEnded: (sid) => {
      ended.push(sid);
      ctl.abort();
    },
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(started).toEqual(["sess_a"]);
  expect(ended).toEqual(["sess_a"]);
});

test("the ready and keepalive frames are ignored", async () => {
  const seen: string[] = [];
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () => {
    ctl.abort();
    return responseOf(
      `event: ready\ndata: {"object":"coddy.events_ready"}\n\n: keepalive\n\n`,
    );
  });

  await subscribeServerEvents({
    onTurnStarted: (sid) => seen.push(sid),
    onTurnEnded: (sid) => seen.push(sid),
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(seen).toEqual([]);
});

// The stream is an optimisation: the caller keeps a poll running while it is down, so it
// has to hear about both edges.
test("connection state is reported up and down", async () => {
  const states: boolean[] = [];
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () => {
    ctl.abort();
    return responseOf(turnEvent("turn_started", "sess_x"));
  });

  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: () => {},
    onConnectedChange: (c) => states.push(c),
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(states).toEqual([true, false]);
});

test("a dropped stream is retried with growing backoff", async () => {
  const delays: number[] = [];
  const ctl = new AbortController();
  let attempts = 0;
  const fetchImpl = vi.fn(async () => {
    attempts += 1;
    if (attempts >= 3) ctl.abort();
    throw new Error("connection refused");
  });

  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: () => {},
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async (ms) => {
      delays.push(ms);
    },
  });

  expect(attempts).toBe(3);
  expect(delays).toEqual([1000, 2000]);
});

test("a malformed event payload is skipped rather than thrown", async () => {
  const seen: string[] = [];
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () => {
    ctl.abort();
    return responseOf(
      `event: turn_started\ndata: {not json\n\n` +
        turnEvent("turn_started", "sess_ok"),
    );
  });

  await subscribeServerEvents({
    onTurnStarted: (sid) => seen.push(sid),
    onTurnEnded: () => {},
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(seen).toEqual(["sess_ok"]);
});
