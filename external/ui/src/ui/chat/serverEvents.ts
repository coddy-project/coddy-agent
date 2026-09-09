import { parseSSEBlocks } from "./sse";
import type { ProviderUsage } from "./providerUsage";

export type ServerEventsHandlers = {
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
  /** Called whenever the subscription goes up or down, so callers can fall back to polling. */
  onConnectedChange?: (connected: boolean) => void;
  signal: AbortSignal;
  /** Injectable for tests; defaults to the global fetch (which carries remote-env auth). */
  fetchImpl?: typeof fetch;
  /** Injectable for tests; defaults to window.setTimeout semantics. */
  sleep?: (ms: number) => Promise<void>;
};

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

/**
 * Subscribe to `GET /coddy/events` until the signal aborts.
 *
 * Built on `fetch` rather than `EventSource` on purpose: `EventSource` cannot carry the
 * `Authorization` header the remote-environment shim injects, and the SPA already reads
 * every other stream this way. Reconnects with capped exponential backoff, because the
 * events stream is an optimisation - callers keep a poll as the fallback - and a server
 * that never comes back must not spin.
 */
export async function subscribeServerEvents(
  p: ServerEventsHandlers,
): Promise<void> {
  const doFetch = p.fetchImpl ?? fetch;
  const sleep = p.sleep ?? defaultSleep;
  let backoff = BACKOFF_START_MS;

  while (!p.signal.aborted) {
    let connected = false;
    try {
      const res = await doFetch("/coddy/events", { signal: p.signal });
      if (!res.ok || !res.body) {
        throw new Error(`events stream unavailable (${res.status})`);
      }
      connected = true;
      backoff = BACKOFF_START_MS;
      p.onConnectedChange?.(true);

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
          if (ev.event === "provider_usage") {
            const parsed = providerUsageOf(ev.data);
            if (parsed) p.onProviderUsage?.(parsed.sessionId, parsed.usage);
            continue;
          }
          if (ev.event === "config_reloaded") {
            // Nothing to parse: the payload is the announcement itself.
            p.onConfigReloaded?.();
            continue;
          }
          if (ev.event !== "turn_started" && ev.event !== "turn_ended")
            continue;
          const sid = sessionIdOf(ev.data);
          if (!sid) continue;
          if (ev.event === "turn_started") p.onTurnStarted(sid);
          else p.onTurnEnded(sid);
        }
      }
    } catch {
      // Aborted, refused, or dropped: both cases fall through to the backoff below.
    } finally {
      if (connected) p.onConnectedChange?.(false);
    }

    if (p.signal.aborted) return;
    await sleep(backoff);
    backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
  }
}
