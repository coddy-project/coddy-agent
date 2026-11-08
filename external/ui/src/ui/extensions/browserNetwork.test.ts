import { expect, test, vi } from "vitest";
import { extensionNetwork } from "./network";

type TrackedSignal = {
  controller: AbortController;
  listeners: Set<EventListenerOrEventListenerObject>;
};

function trackedSignal(): TrackedSignal {
  const controller = new AbortController();
  const listeners = new Set<EventListenerOrEventListenerObject>();
  const add = controller.signal.addEventListener.bind(controller.signal);
  const remove = controller.signal.removeEventListener.bind(controller.signal);
  vi.spyOn(controller.signal, "addEventListener").mockImplementation(
    (type, listener, options) => {
      if (type === "abort" && listener) listeners.add(listener);
      add(type, listener, options);
    },
  );
  vi.spyOn(controller.signal, "removeEventListener").mockImplementation(
    (type, listener, options) => {
      if (type === "abort" && listener) listeners.delete(listener);
      remove(type, listener, options);
    },
  );
  return { controller, listeners };
}

function withoutNativeSignalAny<T>(run: () => T): T {
  const descriptor = Object.getOwnPropertyDescriptor(AbortSignal, "any")!;
  Object.defineProperty(AbortSignal, "any", {
    configurable: true,
    writable: true,
    value: undefined,
  });
  try {
    return run();
  } finally {
    Object.defineProperty(AbortSignal, "any", descriptor);
  }
}

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];

  readonly url: string | URL;
  readonly protocols: string | string[] | undefined;
  readonly close = vi.fn(() => {
    this.readyState = 2;
    this.emitClose();
  });
  readyState = 0;
  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(url: string | URL, protocols?: string | string[]) {
    this.url = url;
    this.protocols = protocols;
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListener) {
    this.listeners.get(type)?.delete(listener);
  }

  emitClose() {
    this.readyState = 3;
    const event = new Event("close");
    for (const listener of this.listeners.get("close") ?? []) listener(event);
    this.listeners.get("close")?.clear();
  }

  listenerCount(type: string) {
    return this.listeners.get(type)?.size ?? 0;
  }
}

function setup(options: { fetch?: typeof fetch } = {}) {
  FakeWebSocket.instances = [];
  const lifetime = trackedSignal();
  const fetcher = options.fetch ?? vi.fn(async () => new Response(null));
  const net = extensionNetwork("demo", "approved", lifetime.controller.signal, {
    fetch: fetcher,
    WebSocket: FakeWebSocket as unknown as typeof WebSocket,
  });
  return { lifetime, fetcher, net };
}

test("browserFetch returns the native response and exposes chunks as they arrive", async () => {
  let streamController!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      streamController = controller;
      controller.enqueue(new Uint8Array([1, 2]));
    },
  });
  const native = new Response(body, {
    status: 200,
    headers: { "Content-Type": "application/octet-stream" },
  });
  const fetcher = vi.fn(async () => native);
  const { net } = setup({ fetch: fetcher });
  const input = new Request("https://example.test/events", {
    headers: { Accept: "application/octet-stream" },
  });

  const response = await net.browserFetch(input, { cache: "no-store" });

  expect(response).toBe(native);
  expect(response.body).toBe(native.body);
  expect(fetcher).toHaveBeenCalledWith(
    input,
    expect.objectContaining({ cache: "no-store" }),
  );
  const reader = response.body!.getReader();
  const first = await reader.read();
  expect(first).toEqual({ done: false, value: new Uint8Array([1, 2]) });
  streamController.enqueue(new Uint8Array([3]));
  expect(await reader.read()).toEqual({
    done: false,
    value: new Uint8Array([3]),
  });
  streamController.close();
  expect(await reader.read()).toEqual({ done: true, value: undefined });
});

test.each(["lifetime", "caller"])(
  "browserFetch %s abort reaches the native request and refuses pre-aborted calls",
  async (source) => {
    const lifetime = trackedSignal();
    const caller = trackedSignal();
    let requestSignal: AbortSignal | null | undefined;
    const fetcher = vi.fn(
      (_input: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          requestSignal = init?.signal;
          requestSignal?.addEventListener(
            "abort",
            () => reject(requestSignal!.reason),
            { once: true },
          );
        }),
    ) as unknown as typeof fetch;
    const net = extensionNetwork(
      "demo",
      "approved",
      lifetime.controller.signal,
      {
        fetch: fetcher,
        WebSocket: FakeWebSocket as unknown as typeof WebSocket,
      },
    );

    const pending = net.browserFetch("https://example.test", {
      signal: caller.controller.signal,
    });
    (source === "lifetime" ? lifetime : caller).controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(requestSignal?.aborted).toBe(true);

    const calls = vi.mocked(fetcher).mock.calls.length;
    const lateInit: RequestInit =
      source === "caller" ? { signal: caller.controller.signal } : {};
    await expect(
      net.browserFetch("https://example.test", lateInit),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(fetcher).toHaveBeenCalledTimes(calls);
  },
);

test("browserFetch composes a Request input's caller signal", async () => {
  const caller = new AbortController();
  let requestSignal: AbortSignal | null | undefined;
  const fetcher = vi.fn(
    (_input: RequestInfo | URL, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        requestSignal = init?.signal;
        requestSignal?.addEventListener(
          "abort",
          () => reject(requestSignal!.reason),
          { once: true },
        );
      }),
  ) as unknown as typeof fetch;
  const { net } = setup({ fetch: fetcher });

  const pending = net.browserFetch(
    new Request("https://example.test", { signal: caller.signal }),
  );
  caller.abort();

  await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  expect(requestSignal?.aborted).toBe(true);
});

test("browserFetch keeps the composed signal alive for the streamed body", async () => {
  let requestSignal: AbortSignal | null | undefined;
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL, init?: RequestInit) => {
      requestSignal = init?.signal;
      return new Response(new ReadableStream({ start() {} }));
    },
  ) as unknown as typeof fetch;
  const { lifetime, net } = setup({ fetch: fetcher });

  const response = await net.browserFetch("https://example.test");
  expect(response.body).toBeInstanceOf(ReadableStream);
  expect(requestSignal?.aborted).toBe(false);
  lifetime.controller.abort();
  expect(requestSignal?.aborted).toBe(true);
});

test("legacy browserFetch fallback aborts after headers and cleans listeners", async () => {
  const lifetime = trackedSignal();
  const caller = trackedSignal();
  let requestSignal: AbortSignal | null | undefined;
  const native = new Response(new ReadableStream({ start() {} }));
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL, init?: RequestInit) => {
      requestSignal = init?.signal;
      return native;
    },
  ) as unknown as typeof fetch;
  const net = extensionNetwork("demo", "approved", lifetime.controller.signal, {
    fetch: fetcher,
    WebSocket: FakeWebSocket as unknown as typeof WebSocket,
  });

  const response = await withoutNativeSignalAny(() =>
    net.browserFetch("https://example.test", {
      signal: caller.controller.signal,
    }),
  );

  expect(response).toBe(native);
  expect(lifetime.listeners.size).toBe(1);
  expect(caller.listeners.size).toBe(1);
  caller.controller.abort();
  expect(requestSignal?.aborted).toBe(true);
  expect(lifetime.listeners.size).toBe(0);
  expect(caller.listeners.size).toBe(0);
});

test("legacy browserFetch fallback refuses late calls without fetching", async () => {
  const { lifetime, fetcher, net } = setup();
  const caller = trackedSignal();
  lifetime.controller.abort();

  await expect(
    withoutNativeSignalAny(() =>
      net.browserFetch("https://example.test", {
        signal: caller.controller.signal,
      }),
    ),
  ).rejects.toMatchObject({ name: "AbortError" });

  expect(fetcher).not.toHaveBeenCalled();
  expect(lifetime.listeners.size).toBe(0);
  expect(caller.listeners.size).toBe(0);
});

test("legacy browserFetch fallback removes listeners when fetch fails", async () => {
  const lifetime = trackedSignal();
  const caller = trackedSignal();
  const net = extensionNetwork("demo", "approved", lifetime.controller.signal, {
    fetch: async () => {
      throw new Error("offline");
    },
    WebSocket: FakeWebSocket as unknown as typeof WebSocket,
  });

  await expect(
    withoutNativeSignalAny(() =>
      net.browserFetch("https://example.test", {
        signal: caller.controller.signal,
      }),
    ),
  ).rejects.toThrow("offline");

  expect(lifetime.listeners.size).toBe(0);
  expect(caller.listeners.size).toBe(0);
});

test("browserFetch refuses an already disposed extension before fetching", async () => {
  const { lifetime, fetcher, net } = setup();
  lifetime.controller.abort();

  await expect(net.browserFetch("https://example.test")).rejects.toMatchObject({
    name: "AbortError",
  });
  expect(fetcher).not.toHaveBeenCalled();
});

const protocolCases: Array<[string, string | string[] | undefined]> = [
  ["no protocols", undefined],
  ["one protocol", "coddy.test"],
  ["protocol list", ["coddy.test", "coddy.test.v2"]],
];

test.each(protocolCases)(
  "webSocket passes %s through to the native constructor",
  (_name, protocols) => {
    const { net } = setup();
    const url = new URL("wss://example.test/socket");

    net.webSocket(url, protocols);

    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(FakeWebSocket.instances[0]?.url).toBe(url);
    expect(FakeWebSocket.instances[0]?.protocols).toEqual(protocols);
  },
);

test.each(["lifetime", "caller"])(
  "webSocket closes on %s abort and removes every abort listener",
  (source) => {
    const { lifetime, net } = setup();
    const caller = trackedSignal();
    const socket = net.webSocket("wss://example.test/socket", undefined, {
      signal: caller.controller.signal,
    }) as unknown as FakeWebSocket;

    (source === "lifetime" ? lifetime : caller).controller.abort();

    expect(socket.close).toHaveBeenCalledOnce();
    expect(socket.readyState).toBe(3);
    expect(lifetime.listeners.size).toBe(0);
    expect(caller.listeners.size).toBe(0);
    expect(socket.listenerCount("close")).toBe(0);
  },
);

test("webSocket removes abort listeners when the peer closes normally", () => {
  const { lifetime, net } = setup();
  const caller = trackedSignal();
  const socket = net.webSocket("wss://example.test/socket", undefined, {
    signal: caller.controller.signal,
  }) as unknown as FakeWebSocket;

  socket.emitClose();

  expect(lifetime.listeners.size).toBe(0);
  expect(caller.listeners.size).toBe(0);
  expect(socket.listenerCount("close")).toBe(0);
  expect(socket.close).not.toHaveBeenCalled();
});

test.each(["lifetime", "caller"])(
  "webSocket refuses a pre-aborted %s before construction",
  (source) => {
    const { lifetime, net } = setup();
    const caller = trackedSignal();
    (source === "lifetime" ? lifetime : caller).controller.abort();

    expect(() =>
      net.webSocket("wss://example.test/socket", undefined, {
        signal: caller.controller.signal,
      }),
    ).toThrow(expect.objectContaining({ name: "AbortError" }));
    expect(FakeWebSocket.instances).toHaveLength(0);
    expect(lifetime.listeners.size).toBe(0);
    expect(caller.listeners.size).toBe(0);
  },
);
