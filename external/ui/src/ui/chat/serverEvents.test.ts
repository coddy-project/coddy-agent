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

test("ready requests reconciliation while keepalive frames remain ignored", async () => {
  const seen: string[] = [];
  const ready = vi.fn();
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
    onReady: ready,
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(seen).toEqual([]);
  expect(ready).toHaveBeenCalledTimes(1);
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

test("provider_usage frames reach their handler with the session that caused them", async () => {
  const seen: Array<{ sid: string; used: number | undefined }> = [];
  const ctl = new AbortController();
  const frame =
    `event: provider_usage\ndata: ${JSON.stringify({
      object: "coddy.provider_usage",
      sessionId: "sess_a",
      usage: { provider: "neuraldeep", providerType: "neuraldeep", windows: [{ id: "session", label: "3h", used: 42, limit: 100, usedPercent: 42 }] },
    })}\n\n`;
  const fetchImpl = vi.fn(async () =>
    responseOf(`event: ready\ndata: {"object":"coddy.events_ready"}\n\n` + frame + `event: provider_usage\ndata: {"broken":true}\n\n`),
  );
  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: () => {},
    onProviderUsage: (sid, usage) => {
      seen.push({ sid, used: usage.windows?.[0]?.used });
      ctl.abort();
    },
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });
  expect(seen).toEqual([{ sid: "sess_a", used: 42 }]);
});

// A session is shared: this stream is how a tab that is not reading any turn's
// own stream learns that someone else queued a follow-up onto it.
test("a message_queue frame reaches its handler with the session and the version", async () => {
  const seen: Array<{ sid: string; texts: string[]; version: number }> = [];
  const ctl = new AbortController();
  const frame =
    `event: message_queue\ndata: ${JSON.stringify({
      object: "coddy.message_queue",
      sessionId: "sess_shared",
      messages: [
        { id: "q_1", text: "check the Windows path too", createdAt: "2026-09-14T00:00:00Z" },
        { id: "q_2", text: "and skip the integration suite" },
      ],
      version: 4,
    })}\n\n`;
  const fetchImpl = vi.fn(async () =>
    responseOf(
      `event: ready\ndata: {"object":"coddy.events_ready"}\n\n` +
        frame +
        `event: message_queue\ndata: {"broken":true}\n\n`,
    ),
  );

  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: () => {},
    onMessageQueue: (sid, queue) => {
      seen.push({
        sid,
        texts: queue.messages.map((m) => m.text),
        version: queue.version,
      });
      ctl.abort();
    },
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(seen).toEqual([
    {
      sid: "sess_shared",
      texts: ["check the Windows path too", "and skip the integration suite"],
      version: 4,
    },
  ]);
});

test("a config reload tells the client to re-read what the config decides", async () => {
  let reloads = 0;
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () =>
    responseOf(
      `event: ready\ndata: {"object":"coddy.events_ready"}\n\n` +
        `event: config_reloaded\ndata: ${JSON.stringify({
          object: "coddy.config_reloaded",
          at: "2026-09-09T12:00:00Z",
        })}\n\n`,
    ),
  );

  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: () => {},
    onConfigReloaded: () => {
      reloads += 1;
      ctl.abort();
    },
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(reloads).toBe(1);
});

// The event carries no model list, so a client that has no use for one must not be
// forced to handle it: the callback stays optional.
test("a config reload without a handler is not an error", async () => {
  const ended: string[] = [];
  const ctl = new AbortController();
  const fetchImpl = vi.fn(async () =>
    responseOf(
      `event: ready\ndata: {"object":"coddy.events_ready"}\n\n` +
        `event: config_reloaded\ndata: {"object":"coddy.config_reloaded"}\n\n` +
        turnEvent("turn_ended", "sess_z"),
    ),
  );

  await subscribeServerEvents({
    onTurnStarted: () => {},
    onTurnEnded: (sid) => {
      ended.push(sid);
      ctl.abort();
    },
    signal: ctl.signal,
    fetchImpl: fetchImpl as unknown as typeof fetch,
    sleep: async () => {},
  });

  expect(ended).toEqual(["sess_z"]);
});
