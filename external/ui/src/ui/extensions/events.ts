import type { IncomingFrame } from "./incomingStream";

export type ExtensionEventMap = {
  tabChanged: { from: string; to: string; sessionId: string | null };
  stepEnded: {
    sessionId: string;
    sequence: string;
    transport: "post" | "relay";
    turnStartedAt: string | null;
    usage: Record<string, unknown>;
  };
  turnEnded: { sessionId: string; at: string };
};
/** An event an extension published through `host.fireEvent`. */
export type ExtensionCustomEvent = Readonly<{
  extension: string;
  type: string;
  data: unknown;
}>;
export type ExtensionEventType = keyof ExtensionEventMap | `ext.${string}`;
export type EventHandle = { remove: () => void };
export type ExtensionEventAPI = {
  on: <K extends ExtensionEventType>(
    type: K,
    listener: (
      event: Readonly<
        K extends keyof ExtensionEventMap
          ? ExtensionEventMap[K]
          : ExtensionCustomEvent
      >,
    ) => unknown,
  ) => EventHandle;
};

/** Custom event names: `ext.<extension id>.<type>`, one per published type. */
export function isCustomEventType(name: string) {
  return /^ext\.[a-z][a-z0-9-]{0,63}\.[a-z][a-z0-9-]{0,63}$/.test(name);
}

function freeze<T>(value: T): T {
  if (value && typeof value === "object") {
    Object.values(value).forEach(freeze);
    Object.freeze(value);
  }
  return value;
}

/** Observational callbacks share no mutable payload and never gate host work. */
export class ExtensionEvents {
  private listeners = new Set<{
    owner: string;
    type: string;
    signal: AbortSignal;
    run: (event: never) => unknown;
    remove: () => void;
  }>();
  private seen = new Set<string>();
  private starts = new Map<string, string>();
  constructor(private report: (owner: string, message: string) => void) {}
  on<K extends ExtensionEventType>(
    owner: string,
    signal: AbortSignal,
    type: K,
    listener: (event: never) => unknown,
  ): EventHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (
      !["tabChanged", "stepEnded", "turnEnded"].includes(type) &&
      !isCustomEventType(type)
    )
      throw new Error("Invalid extension event listener");
    if (typeof listener !== "function")
      throw new Error("Invalid extension event listener");
    const row = {
      owner,
      type,
      signal,
      run: listener as (event: never) => unknown,
      remove: () => {
        this.listeners.delete(row);
        signal.removeEventListener("abort", row.remove);
      },
    };
    this.listeners.add(row);
    signal.addEventListener("abort", row.remove, { once: true });
    return { remove: row.remove };
  }
  emit<K extends keyof ExtensionEventMap>(
    type: K,
    payload: ExtensionEventMap[K],
  ) {
    this.deliver(type, freeze(structuredClone(payload)));
  }
  /** Delivers one extension-published event to its `ext.<owner>.<type>`
   * listeners, the same isolation rules as the lifecycle events. */
  emitCustom(owner: string, type: string, data: unknown) {
    this.deliver(
      `ext.${owner}.${type}`,
      freeze(
        structuredClone({
          extension: owner,
          type,
          data,
        }),
      ),
    );
  }
  private deliver(type: string, event: unknown) {
    for (const row of [...this.listeners].sort((a, b) =>
      a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0,
    )) {
      if (row.type !== type || row.signal.aborted || !this.listeners.has(row))
        continue;
      const fail = (error: unknown) => {
        row.remove();
        if (!row.signal.aborted) this.report(row.owner, String(error));
      };
      try {
        Promise.resolve(row.run(event as never)).catch(fail);
      } catch (error) {
        fail(error);
      }
    }
  }
  private admit(key: string) {
    if (this.seen.has(key)) return false;
    this.seen.add(key);
    if (this.seen.size > 2048)
      this.seen.delete(this.seen.values().next().value!);
    return true;
  }
  observe = (
    frame: IncomingFrame & { sequence: string; streamId?: string },
  ) => {
    if (frame.event !== "turn_progress" && frame.event !== "token_usage")
      return;
    let data: Record<string, unknown>;
    try {
      const parsed = JSON.parse(frame.data);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
        return;
      data = parsed;
    } catch {
      return;
    }
    const streamKey = `${frame.sessionId}:${frame.streamId ?? "legacy"}`;
    if (frame.event === "turn_progress") {
      if (typeof data.startedAt === "string") {
        this.starts.delete(streamKey);
        this.starts.set(streamKey, data.startedAt);
        if (this.starts.size > 256)
          this.starts.delete(this.starts.keys().next().value!);
      }
      return;
    }
    const turnStartedAt = this.starts.get(streamKey) ?? null;
    if (
      !this.admit(
        `step:${frame.sessionId}:${turnStartedAt ?? frame.streamId ?? "unknown"}:${frame.sequence}`,
      )
    )
      return;
    this.emit("stepEnded", {
      sessionId: frame.sessionId,
      sequence: frame.sequence,
      transport: frame.transport,
      turnStartedAt,
      usage: data,
    });
  };
  turnEnded(sessionId: string, at: string) {
    if (this.admit(`turn:${sessionId}:${at}`))
      this.emit("turnEnded", { sessionId, at });
  }
}
