import {
  dispatchServerEvent,
  streamServerEvents,
  type ServerEvent,
  type ServerEventHandlers,
  type ServerEventsStreamOptions,
} from "./serverEvents";

// One GET /coddy/events connection serving several tabs. The owner of the
// connection - a SharedWorker, or the tab elected through Web Locks - runs a
// ServerEventsHub; every tab, the owner's own included, reads what the hub says
// through a receiver. This module is imported by the worker, so it must not
// touch the DOM.

/** The part of a MessagePort a tab and a worker use to talk. */
export type PortLike = {
  postMessage(data: unknown): void;
  addEventListener(
    type: "message",
    fn: (event: { data: unknown }) => void,
  ): void;
  removeEventListener(
    type: "message",
    fn: (event: { data: unknown }) => void,
  ): void;
  start(): void;
  close(): void;
};

/**
 * What the owner of the shared stream tells tabs. `to` addresses one tab, a message
 * without it is for all of them. `term` orders owners: a tab elected later speaks
 * with a higher term, and a tab drops anything an earlier owner says after that.
 */
export type HubMessage =
  | { kind: "event"; event: ServerEvent; term: number; to?: string }
  | { kind: "connected"; connected: boolean; term: number; to?: string }
  | { kind: "refused"; status: number; term: number };

/** What a tab tells the owner. A worker also needs where the stream is and how to authenticate. */
export type TabMessage =
  | {
      kind: "hello";
      tab: string;
      url?: string;
      headers?: Record<string, string>;
    }
  | { kind: "bye"; tab: string };

export function isTabMessage(data: unknown): data is TabMessage {
  const m = data as { kind?: unknown; tab?: unknown } | null;
  return (
    !!m && (m.kind === "hello" || m.kind === "bye") && typeof m.tab === "string"
  );
}

function isHubMessage(data: unknown): data is HubMessage {
  const m = data as { kind?: unknown; term?: unknown } | null;
  return (
    !!m &&
    (m.kind === "event" || m.kind === "connected" || m.kind === "refused") &&
    typeof m.term === "number"
  );
}

export class ServerEventsHub {
  private connected = false;
  private snapshotComplete = false;
  private readonly active = new Set<string>();
  private run = 0;

  constructor(
    private readonly send: (message: HubMessage) => void,
    readonly term = 0,
  ) {}

  /** A new owner starts with nothing: until its connection is up, nobody holds one. */
  announceDisconnected(): void {
    this.send({ kind: "connected", connected: false, term: this.term });
  }

  /** Holds the connection until the signal aborts, telling every tab what it hears. */
  stream(
    o: Omit<
      ServerEventsStreamOptions,
      "onEvent" | "onConnectedChange" | "onRefused"
    >,
  ): Promise<void> {
    const run = ++this.run;
    const current = () => run === this.run;
    this.reset(false);
    return streamServerEvents({
      ...o,
      onConnectedChange: (connected) => {
        if (current()) {
          this.reset(connected);
          this.send({ kind: "connected", connected, term: this.term });
        }
      },
      onEvent: (event) => {
        if (!current()) return;
        this.track(event);
        this.send({ kind: "event", event, term: this.term });
      },
      onRefused: (status) => {
        if (current()) this.send({ kind: "refused", status, term: this.term });
      },
    });
  }

  /**
   * Brings a joining tab to where the others are. A tab of its own would read the
   * server's snapshot of active turns and then ready on connecting; this one gets the
   * turns the hub has seen so far, and ready once the server's snapshot is complete -
   * before that, the rest of the snapshot and its ready reach it with everybody else.
   * Addressed to that tab alone: ready makes a tab give up its pending Stops.
   */
  hello(tab: string): void {
    const term = this.term;
    this.send({ kind: "connected", connected: this.connected, term, to: tab });
    if (!this.connected) return;
    for (const sessionId of this.active)
      this.send({
        kind: "event",
        event: { type: "turn_started", sessionId },
        term,
        to: tab,
      });
    if (this.snapshotComplete)
      this.send({ kind: "event", event: { type: "ready" }, term, to: tab });
  }

  // Every connection starts with a snapshot of its own; what an earlier one said
  // about active turns is stale.
  private reset(connected: boolean) {
    this.connected = connected;
    this.snapshotComplete = false;
    this.active.clear();
  }

  private track(event: ServerEvent) {
    if (event.type === "turn_started") this.active.add(event.sessionId);
    else if (event.type === "turn_ended") this.active.delete(event.sessionId);
    else if (event.type === "ready") this.snapshotComplete = true;
  }
}

export type HubReceiver = {
  receive(data: unknown): void;
  /** The tab stops listening; reports the stream down, as a closing connection of its own would. */
  close(): void;
  /** The highest owner term heard so far. */
  readonly term: number;
};

/** createHubReceiver turns what an owner says into the handler calls a tab of its own would make. */
export function createHubReceiver(
  tab: string,
  h: ServerEventHandlers & { onRefused?: (status: number) => void },
): HubReceiver {
  let connected = false;
  let term = Number.NEGATIVE_INFINITY;
  return {
    get term() {
      return term;
    },
    receive(data) {
      if (!isHubMessage(data)) return;
      if (data.kind !== "refused" && data.to !== undefined && data.to !== tab)
        return;
      if (data.term < term) return;
      term = data.term;
      switch (data.kind) {
        case "connected":
          if (data.connected === connected) return;
          connected = data.connected;
          h.onConnectedChange?.(connected);
          return;
        case "refused":
          h.onRefused?.(data.status);
          return;
        case "event":
          dispatchServerEvent(h, data.event);
          return;
      }
    },
    close() {
      if (!connected) return;
      connected = false;
      h.onConnectedChange?.(false);
    },
  };
}
