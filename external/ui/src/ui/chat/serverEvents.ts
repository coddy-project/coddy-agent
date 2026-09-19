import { parseSSEBlocks } from "./sse";
import type { ProviderUsage } from "./providerUsage";
import {
  sessionSettingsEventOf,
  type SessionSettingsEvent,
} from "./sessionSettings";

/** What a caller does with the events of `GET /coddy/events`. */
export type ServerEventHandlers = {
  onTurnStarted: (sessionId: string) => void;
  onTurnEnded: (sessionId: string) => void;
  /** A fresh account-usage snapshot the server built outside a request
   *  (a finished turn, a deferred refresh); sessionId names the turn, the
   *  snapshot is account-wide. */
  onProviderUsage?: (sessionId: string, usage: ProviderUsage) => void;
  /** The live configuration was swapped (settings save, the agent's `config_commit`,
   *  a skill install). The event carries nothing but the fact, so a caller re-reads
   *  whatever config-derived list it renders - the model picker, the slash commands. */
  onConfigReloaded?: () => void;
  /** The message queue of a session changed - anywhere, by anyone. A session is
   *  shared, so this is how a second browser learns that someone else queued a
   *  follow-up onto the turn it is watching. Carries the whole queue and its
   *  version; the caller keeps the highest version it has seen. */
  onMessageQueue?: (sessionId: string, queue: QueuedMessageEvent) => void;
  /** A session's settings changed - model, reasoning, mode, permission mode,
   *  the overrides for the next turns - from any surface. Carries the whole
   *  versioned snapshot and a notice of what changed. */
  onSessionSettings?: (event: SessionSettingsEvent) => void;
  /** A background subagent of this parent session started waiting for a
   *  permission answer, or stopped waiting (answered anywhere, withdrawn, its
   *  run ended). The prompt itself waits on the subagent's task row, so the
   *  chat of that session re-reads its tasks. */
  onSubagentPermission?: (parentSessionId: string) => void;
  /** The connect/reconnect replay is complete; reconcile activity and queues over REST. */
  onReady?: () => void;
  /** Called whenever the subscription goes up or down, so callers can fall back to polling. */
  onConnectedChange?: (connected: boolean) => void;
};

export type ServerEventsHandlers = ServerEventHandlers & {
  signal: AbortSignal;
  /** Injectable for tests; defaults to the global fetch (which carries remote-env auth). */
  fetchImpl?: typeof fetch;
  /** Injectable for tests; defaults to window.setTimeout semantics. */
  sleep?: (ms: number) => Promise<void>;
};

/**
 * One event of the stream, parsed. Plain data on purpose: a tab that does not own
 * the connection receives these through a MessagePort or a BroadcastChannel, which
 * carry structured clones and nothing else.
 */
export type ServerEvent =
  | { type: "turn_started"; sessionId: string }
  | { type: "turn_ended"; sessionId: string }
  | { type: "provider_usage"; sessionId: string; usage: ProviderUsage }
  | { type: "message_queue"; sessionId: string; queue: QueuedMessageEvent }
  | { type: "session_settings"; event: SessionSettingsEvent }
  | { type: "config_reloaded" }
  | { type: "subagent_permission"; parentSessionId: string }
  | { type: "ready" };

/** One session's message queue as the server event carries it. */
export type QueuedMessageEvent = {
  messages: { id: string; text: string; createdAt?: string }[];
  version: number;
};

function messageQueueOf(
  data: string,
): { sessionId: string; queue: QueuedMessageEvent } | null {
  try {
    const parsed = JSON.parse(data) as {
      sessionId?: unknown;
      messages?: unknown;
      version?: unknown;
    };
    const sid =
      typeof parsed.sessionId === "string" ? parsed.sessionId.trim() : "";
    if (!sid) return null;
    const rows = Array.isArray(parsed.messages) ? parsed.messages : [];
    return {
      sessionId: sid,
      queue: {
        messages: rows
          .map((r) => r as { id: string; text: string; createdAt?: string })
          .filter(
            (r) => r && typeof r.id === "string" && typeof r.text === "string",
          ),
        version: typeof parsed.version === "number" ? parsed.version : 0,
      },
    };
  } catch {
    return null;
  }
}

const BACKOFF_START_MS = 1000;
const BACKOFF_MAX_MS = 10000;

function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function providerUsageOf(
  data: string,
): { sessionId: string; usage: ProviderUsage } | null {
  try {
    const parsed = JSON.parse(data) as {
      sessionId?: unknown;
      usage?: ProviderUsage | null;
    };
    if (!parsed.usage || typeof parsed.usage.provider !== "string") return null;
    return {
      sessionId: typeof parsed.sessionId === "string" ? parsed.sessionId : "",
      usage: parsed.usage,
    };
  } catch {
    return null;
  }
}

function sessionIdOf(data: string): string {
  try {
    const parsed = JSON.parse(data) as { sessionId?: unknown };
    return typeof parsed.sessionId === "string" ? parsed.sessionId.trim() : "";
  } catch {
    return "";
  }
}

function parentSessionIdOf(data: string): string {
  try {
    const parsed = JSON.parse(data) as { parentSessionId?: unknown };
    return typeof parsed.parentSessionId === "string"
      ? parsed.parentSessionId.trim()
      : "";
  } catch {
    return "";
  }
}

/** parseServerEvent reads one SSE block of the stream; null for anything a client ignores. */
export function parseServerEvent(ev: {
  event: string;
  data: string;
}): ServerEvent | null {
  switch (ev.event) {
    case "ready":
      return { type: "ready" };
    case "provider_usage": {
      const parsed = providerUsageOf(ev.data);
      return parsed ? { type: "provider_usage", ...parsed } : null;
    }
    case "message_queue": {
      const parsed = messageQueueOf(ev.data);
      return parsed ? { type: "message_queue", ...parsed } : null;
    }
    case "session_settings": {
      const parsed = sessionSettingsEventOf(ev.data);
      return parsed ? { type: "session_settings", event: parsed } : null;
    }
    case "config_reloaded":
      // Nothing to parse: the payload is the announcement itself.
      return { type: "config_reloaded" };
    case "subagent_permission": {
      // A frame naming no parent belongs to no chat.
      const parent = parentSessionIdOf(ev.data);
      return parent
        ? { type: "subagent_permission", parentSessionId: parent }
        : null;
    }
    case "turn_started":
    case "turn_ended": {
      const sid = sessionIdOf(ev.data);
      return sid ? { type: ev.event, sessionId: sid } : null;
    }
    default:
      return null;
  }
}

/** dispatchServerEvent hands one parsed event to the handler that takes it. */
export function dispatchServerEvent(
  h: ServerEventHandlers,
  event: ServerEvent,
): void {
  switch (event.type) {
    case "ready":
      h.onReady?.();
      return;
    case "provider_usage":
      h.onProviderUsage?.(event.sessionId, event.usage);
      return;
    case "message_queue":
      h.onMessageQueue?.(event.sessionId, event.queue);
      return;
    case "session_settings":
      h.onSessionSettings?.(event.event);
      return;
    case "config_reloaded":
      h.onConfigReloaded?.();
      return;
    case "subagent_permission":
      h.onSubagentPermission?.(event.parentSessionId);
      return;
    case "turn_started":
      h.onTurnStarted(event.sessionId);
      return;
    case "turn_ended":
      h.onTurnEnded(event.sessionId);
      return;
  }
}

export type ServerEventsStreamOptions = {
  onEvent: (event: ServerEvent) => void;
  onConnectedChange?: (connected: boolean) => void;
  /** A connection attempt the server answered with an error status. */
  onRefused?: (status: number) => void;
  signal: AbortSignal;
  /** Where the stream is; `/coddy/events` of the page (or of the remote the fetch shim picks) by default. */
  url?: string;
  headers?: Record<string, string>;
  fetchImpl?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
};

/**
 * streamServerEvents holds `GET /coddy/events` open until the signal aborts and reports
 * every parsed event. It is the connection itself, whoever owns it: a tab on its own, the
 * shared worker, or the tab elected to hold the stream for the others.
 *
 * Built on `fetch` rather than `EventSource` on purpose: `EventSource` cannot carry the
 * `Authorization` header a remote environment needs, and the SPA already reads every
 * other stream this way. Reconnects with capped exponential backoff, because the events
 * stream is an optimisation - callers keep a poll as the fallback - and a server that
 * never comes back must not spin.
 */
export async function streamServerEvents(
  o: ServerEventsStreamOptions,
): Promise<void> {
  const doFetch = o.fetchImpl ?? fetch;
  const sleep = o.sleep ?? defaultSleep;
  const url = o.url ?? "/coddy/events";
  let backoff = BACKOFF_START_MS;

  while (!o.signal.aborted) {
    let connected = false;
    try {
      const res = await doFetch(url, {
        signal: o.signal,
        ...(o.headers ? { headers: o.headers } : {}),
      });
      if (!res.ok || !res.body) {
        if (!res.ok) o.onRefused?.(res.status);
        throw new Error(`events stream unavailable (${res.status})`);
      }
      connected = true;
      backoff = BACKOFF_START_MS;
      o.onConnectedChange?.(true);

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const carry = { buf: "" };
      for (;;) {
        const step = await reader.read();
        if (step.done) break;
        const events = parseSSEBlocks(
          dec.decode(step.value, { stream: true }),
          carry,
        );
        for (const ev of events) {
          const event = parseServerEvent(ev);
          if (event) o.onEvent(event);
        }
      }
    } catch {
      // Aborted, refused, or dropped: both cases fall through to the backoff below.
    } finally {
      if (connected) o.onConnectedChange?.(false);
    }

    if (o.signal.aborted) return;
    await sleep(backoff);
    backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
  }
}

/** Subscribe to `GET /coddy/events` on a connection of this tab's own until the signal aborts. */
export function subscribeServerEvents(p: ServerEventsHandlers): Promise<void> {
  return streamServerEvents({
    onEvent: (event) => dispatchServerEvent(p, event),
    ...(p.onConnectedChange ? { onConnectedChange: p.onConnectedChange } : {}),
    signal: p.signal,
    ...(p.fetchImpl ? { fetchImpl: p.fetchImpl } : {}),
    ...(p.sleep ? { sleep: p.sleep } : {}),
  });
}
