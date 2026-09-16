import type { CoddyEnv } from "../env/remoteEnv";
import {
  subscribeServerEvents,
  type ServerEventsHandlers,
} from "./serverEvents";
import {
  createHubReceiver,
  isTabMessage,
  ServerEventsHub,
  type PortLike,
  type TabMessage,
} from "./serverEventsHub";

// Every tab of Coddy used to hold its own GET /coddy/events. Over plain HTTP/1.1
// a browser keeps six connections to a host for all of its tabs, so six open tabs
// took every one of them and nothing else - a prompt, a Stop, a history read -
// could be sent from any tab. Tabs of one environment now share a single
// connection: a SharedWorker holds it where the browser has one; otherwise the tab
// elected through Web Locks holds it and relays over a BroadcastChannel (both need
// a secure context, which plain http on a LAN address is not); otherwise, and
// whenever the chosen mechanism fails to start, each tab keeps a stream of its own.

export type { PortLike } from "./serverEventsHub";

export type SharedWorkerLike = {
  port: PortLike;
  addEventListener(type: "error", fn: () => void): void;
  removeEventListener(type: "error", fn: () => void): void;
};

export type LocksLike = {
  request(
    name: string,
    options: { signal?: AbortSignal },
    callback: () => Promise<void>,
  ): Promise<unknown>;
};

export type ChannelLike = {
  postMessage(data: unknown): void;
  addEventListener(
    type: "message",
    fn: (event: { data: unknown }) => void,
  ): void;
  removeEventListener(
    type: "message",
    fn: (event: { data: unknown }) => void,
  ): void;
  close(): void;
};

/** The browser machinery tabs share a stream through; null where it is missing. */
export type EventsTransports = {
  sharedWorker: ((name: string) => SharedWorkerLike) | null;
  locks: LocksLike | null;
  channel: ((name: string) => ChannelLike) | null;
};

export type SharedServerEventsOptions = ServerEventsHandlers & {
  /** The environment the page talks to; tabs share a stream only within one. */
  env: CoddyEnv;
  /**
   * The server refused a connection attempt made on this tab's behalf by another
   * context - the worker, or the elected tab. A connection of the tab's own goes
   * through the page's fetch, which reports a refusal by itself.
   */
  onRefused?: (status: number) => void;
  /** Injectable for tests; defaults to what the browser offers. */
  transports?: EventsTransports;
  /** How long a worker has to answer before the tab goes on without it. */
  ackTimeoutMs?: number;
};

// Part of every name, so tabs of a build that speaks a different protocol never
// join the same worker, lock or channel.
const PROTOCOL = 1;
const DEFAULT_ACK_TIMEOUT_MS = 5000;

/**
 * serverEventsScope names what tabs share: the worker, the lock and the channel.
 * A remote environment is told apart by its address and by a fingerprint of its
 * token, so a rotated token does not join a stream authenticated with the old one,
 * and the token itself never appears in a name the browser shows in its tools.
 */
export function serverEventsScope(env: CoddyEnv): string {
  if (env.mode !== "remote") return `coddy-events/${PROTOCOL}/local`;
  return `coddy-events/${PROTOCOL}/remote/${env.baseUrl}/${fingerprint(env.token)}`;
}

// FNV-1a: stable and dependency-free. crypto.subtle is not an option, it exists
// only in the secure contexts this code has to work without.
function fingerprint(text: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

// crypto.randomUUID is secure-context only as well.
function newTabId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

function defaultTransports(): EventsTransports {
  const locks =
    typeof navigator !== "undefined"
      ? (navigator as { locks?: LocksLike }).locks
      : undefined;
  return {
    sharedWorker:
      typeof SharedWorker === "function"
        ? (name) =>
            new SharedWorker(new URL("./eventsWorker.ts", import.meta.url), {
              type: "module",
              name,
            }) as unknown as SharedWorkerLike
        : null,
    locks: locks ?? null,
    channel:
      typeof BroadcastChannel === "function"
        ? (name) => new BroadcastChannel(name) as unknown as ChannelLike
        : null,
  };
}

/**
 * subscribeSharedServerEvents delivers `GET /coddy/events` to the handlers until the
 * signal aborts, over a connection this tab shares with the other tabs of its
 * environment when the browser allows it. The handlers see what a connection of
 * the tab's own would give them: connected state, the snapshot of active turns,
 * ready, then live events.
 */
export async function subscribeSharedServerEvents(
  o: SharedServerEventsOptions,
): Promise<void> {
  const transports = o.transports ?? defaultTransports();
  const scope = serverEventsScope(o.env);
  if (transports.sharedWorker) {
    const served = await viaWorker(o, scope, transports.sharedWorker);
    if (served || o.signal.aborted) return;
  }
  if (transports.locks && transports.channel) {
    await viaLocks(o, scope, transports.locks, transports.channel);
    return;
  }
  await subscribeServerEvents(o);
}

/** Where the worker finds the stream, since the page's fetch shim does not reach into it. */
function workerHello(tab: string, env: CoddyEnv): TabMessage {
  if (env.mode !== "remote")
    // Resolved against the worker's own address, which is the page's origin.
    return { kind: "hello", tab, url: "/coddy/events" };
  return {
    kind: "hello",
    tab,
    url: `${env.baseUrl}/coddy/events`,
    ...(env.token ? { headers: { Authorization: `Bearer ${env.token}` } } : {}),
  };
}

/** Resolves true once the worker served the tab until the signal aborted, false when it never answered. */
function viaWorker(
  o: SharedServerEventsOptions,
  scope: string,
  create: (name: string) => SharedWorkerLike,
): Promise<boolean> {
  let worker: SharedWorkerLike;
  try {
    worker = create(scope);
  } catch {
    return Promise.resolve(false);
  }
  const port = worker.port;
  const tab = newTabId();
  const receiver = createHubReceiver(tab, o);
  const hello = workerHello(tab, o.env);
  const win = typeof window !== "undefined" ? window : null;

  return new Promise<boolean>((resolve) => {
    let answered = false;
    const onMessage = (event: { data: unknown }) => {
      if (!answered) {
        answered = true;
        clearTimeout(timer);
      }
      receiver.receive(event.data);
    };
    const leave = () => port.postMessage({ kind: "bye", tab });
    // A page restored from the back/forward cache said bye when it was hidden.
    const rejoin = (event: Event) => {
      if ((event as PageTransitionEvent).persisted) port.postMessage(hello);
    };
    const finish = (served: boolean) => {
      clearTimeout(timer);
      worker.removeEventListener("error", onUnavailable);
      port.removeEventListener("message", onMessage);
      o.signal.removeEventListener("abort", onAbort);
      win?.removeEventListener("pagehide", leave);
      win?.removeEventListener("pageshow", rejoin);
      leave();
      port.close();
      receiver.close();
      resolve(served);
    };
    // A worker that fails to load, or loads and never answers, is no worker: the
    // tab goes on without it. Once it has answered, its errors are its own.
    const onUnavailable = () => {
      if (!answered) finish(false);
    };
    const onAbort = () => finish(true);
    const timer = setTimeout(
      onUnavailable,
      o.ackTimeoutMs ?? DEFAULT_ACK_TIMEOUT_MS,
    );
    worker.addEventListener("error", onUnavailable);
    port.addEventListener("message", onMessage);
    o.signal.addEventListener("abort", onAbort, { once: true });
    win?.addEventListener("pagehide", leave);
    win?.addEventListener("pageshow", rejoin);
    port.start();
    if (o.signal.aborted) onAbort();
    else port.postMessage(hello);
  });
}

async function viaLocks(
  o: SharedServerEventsOptions,
  scope: string,
  locks: LocksLike,
  openChannel: (name: string) => ChannelLike,
): Promise<void> {
  let channel: ChannelLike;
  try {
    channel = openChannel(scope);
  } catch {
    await subscribeServerEvents(o);
    return;
  }
  const tab = newTabId();
  const receiver = createHubReceiver(tab, o);
  let hub: ServerEventsHub | null = null;
  const onMessage = (event: { data: unknown }) => {
    const data = event.data;
    if (isTabMessage(data)) {
      if (data.kind === "hello") hub?.hello(data.tab);
      return;
    }
    // Once this tab holds the stream, what another owner says is from before.
    if (!hub) receiver.receive(data);
  };
  channel.addEventListener("message", onMessage);
  channel.postMessage({ kind: "hello", tab } satisfies TabMessage);
  try {
    await locks.request(scope, { signal: o.signal }, async () => {
      if (o.signal.aborted) return;
      // Later than every owner this tab has heard, and than the wall clock of one
      // it has not.
      const term = Math.max(Date.now(), receiver.term + 1);
      const owner = new ServerEventsHub((message) => {
        // This tab's own connection goes through the page's fetch, which reports
        // a refusal by itself.
        if (message.kind !== "refused") receiver.receive(message);
        if (message.kind === "refused" || message.to !== tab)
          channel.postMessage(message);
      }, term);
      hub = owner;
      // The previous owner may have gone with its tab, without a word.
      owner.announceDisconnected();
      await owner.stream({
        signal: o.signal,
        ...(o.fetchImpl ? { fetchImpl: o.fetchImpl } : {}),
        ...(o.sleep ? { sleep: o.sleep } : {}),
      });
    });
  } catch {
    // Aborted while another tab held the stream.
  } finally {
    channel.removeEventListener("message", onMessage);
    channel.close();
    receiver.close();
  }
}
