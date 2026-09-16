import { createEventsWorkerHost } from "./eventsWorkerHost";
import type {
  ChannelLike,
  LocksLike,
  PortLike,
  SharedWorkerLike,
} from "./sharedServerEvents";

// In-memory stand-ins for the browser machinery that lets tabs share one events
// stream: a MessagePort pair, SharedWorker instances keyed by name and running
// the real worker host, Web Locks and BroadcastChannel. Messages are structured
// clones delivered on a microtask, as they are between real browsing contexts.

const realSleep = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));

export class FakePort implements PortLike {
  other!: FakePort;
  closed = false;
  private listeners = new Set<(event: { data: unknown }) => void>();
  postMessage(data: unknown) {
    if (this.closed) return;
    const target = this.other;
    const copy = structuredClone(data);
    queueMicrotask(() => {
      if (!target.closed) target.listeners.forEach((fn) => fn({ data: copy }));
    });
  }
  addEventListener(_type: "message", fn: (event: { data: unknown }) => void) {
    this.listeners.add(fn);
  }
  removeEventListener(
    _type: "message",
    fn: (event: { data: unknown }) => void,
  ) {
    this.listeners.delete(fn);
  }
  start() {}
  close() {
    this.closed = true;
  }
}

/** SharedWorker as the browser keys it: one instance per name, running the real host. */
export function fakeWorkers(
  fetchImpl: typeof fetch,
  mode: "working" | "broken" | "silent" = "working",
) {
  const hosts = new Map<string, ReturnType<typeof createEventsWorkerHost>>();
  const names: string[] = [];
  const factory = (name: string): SharedWorkerLike => {
    names.push(name);
    const tabSide = new FakePort();
    const workerSide = new FakePort();
    tabSide.other = workerSide;
    workerSide.other = tabSide;
    const errors = new Set<() => void>();
    if (mode === "broken") queueMicrotask(() => errors.forEach((fn) => fn()));
    if (mode === "working") {
      let host = hosts.get(name);
      if (!host) {
        host = createEventsWorkerHost({ fetchImpl, sleep: realSleep });
        hosts.set(name, host);
      }
      host.connect(workerSide);
    }
    return {
      port: tabSide,
      addEventListener: (_type: "error", fn: () => void) => errors.add(fn),
      removeEventListener: (_type: "error", fn: () => void) =>
        errors.delete(fn),
    };
  };
  return { factory, hosts, names };
}

/** Web Locks: one holder per name, waiters granted in order, abortable while waiting. */
export class FakeLocks implements LocksLike {
  private held = new Set<string>();
  private waiting = new Map<string, (() => void)[]>();
  async request(
    name: string,
    options: { signal?: AbortSignal },
    callback: () => Promise<void>,
  ) {
    if (options.signal?.aborted)
      throw new DOMException("Aborted", "AbortError");
    if (this.held.has(name)) {
      const queue = this.waiting.get(name) ?? [];
      this.waiting.set(name, queue);
      await new Promise<void>((resolve, reject) => {
        const grant = () => {
          options.signal?.removeEventListener("abort", drop);
          resolve();
        };
        const drop = () => {
          queue.splice(queue.indexOf(grant), 1);
          reject(new DOMException("Aborted", "AbortError"));
        };
        options.signal?.addEventListener("abort", drop, { once: true });
        queue.push(grant);
      });
    }
    this.held.add(name);
    try {
      return await callback();
    } finally {
      const next = this.waiting.get(name)?.shift();
      if (next) next();
      else this.held.delete(name);
    }
  }
}

/** BroadcastChannel: a message reaches every other open channel of the same name. */
export function fakeChannels() {
  const open = new Set<FakeChannel>();
  class FakeChannel implements ChannelLike {
    private listeners = new Set<(event: { data: unknown }) => void>();
    constructor(readonly name: string) {
      open.add(this);
    }
    postMessage(data: unknown) {
      const copy = structuredClone(data);
      for (const channel of open) {
        if (channel === this || channel.name !== this.name) continue;
        queueMicrotask(() => {
          if (open.has(channel))
            channel.listeners.forEach((fn) => fn({ data: copy }));
        });
      }
    }
    addEventListener(_type: "message", fn: (event: { data: unknown }) => void) {
      this.listeners.add(fn);
    }
    removeEventListener(
      _type: "message",
      fn: (event: { data: unknown }) => void,
    ) {
      this.listeners.delete(fn);
    }
    close() {
      open.delete(this);
    }
  }
  return { create: (name: string) => new FakeChannel(name), open };
}
