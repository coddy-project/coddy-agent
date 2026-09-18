import { useCallback, useEffect, useRef, useState } from "react";
import type { QueuedMessageEvent } from "./serverEvents";
import { turnProgressFromActivity, type TurnProgress } from "./turnProgress";

const HDR = "X-Coddy-Session-ID";
const RECONCILE_MS = 2000;

type Activity = { active: boolean; revision: number; generation: number };
type Options = {
  sessionId: string;
  connected: boolean;
  postPending: (sid: string) => boolean;
  onQueueRead: (sid: string) => (queue: QueuedMessageEvent) => void;
  onReconcile: (sid: string, active: boolean, wasActive: boolean) => void;
  /**
   * The running turn's clock and tokens as the activity answer carries them. The relay
   * does not replay a turn_progress frame the transcript snapshot covers, so a tab that
   * joined late reads them here. Null says the answer found the session idle: whatever
   * progress the tab holds belongs to a turn that is over. It is said on every such
   * read, not only on one that reconciles - the first read to see the turn gone may be
   * one that notifies nobody, and then no reconcile runs for that turn at all.
   */
  onTurnProgress?: (sid: string, progress: TurnProgress | null) => void;
};

/** Server admission is independent of a browser's POST/relay connection. A lost
 * stream or a successful cancel request cannot prove that admission was released. */
export function useSessionTurnActivity(options: Options) {
  const callbacks = useRef(options);
  callbacks.current = options;
  const activity = useRef(new Map<string, Activity>());
  const requests = useRef(new Map<string, AbortController>());
  const mounted = useRef(true);
  const [epoch, setEpoch] = useState(0);

  const get = useCallback(
    (sid: string) => activity.current.get(sid.trim())?.active,
    [],
  );
  const generation = useCallback(
    (sid: string) => activity.current.get(sid.trim())?.generation ?? 0,
    [],
  );
  const observe = useCallback((sid: string, active: boolean) => {
    const key = sid.trim();
    if (!key || !mounted.current) return;
    const previous = activity.current.get(key);
    const changed = previous?.active !== active;
    activity.current.set(key, {
      active,
      revision: (previous?.revision ?? 0) + 1,
      generation: (previous?.generation ?? 0) + (changed ? 1 : 0),
    });
    if (changed) setEpoch((n) => n + 1);
  }, []);

  const refresh = useCallback(
    async (sid: string, notify = true, replacePending = true) => {
      const key = sid.trim();
      // First-send admission may have written an empty layout without publishing
      // the live session yet. Neither endpoint should hydrate it in that window.
      if (!key || !mounted.current || callbacks.current.postPending(key))
        return;
      const revision = activity.current.get(key)?.revision ?? 0;
      const applyQueueSnapshot = callbacks.current.onQueueRead(key);
      const readSnapshot = async (
        kind: "activity" | "queue",
        apply: (raw: unknown) => void,
      ) => {
        const requestKey = `${key}/${kind}`;
        const previous = requests.current.get(requestKey);
        if (previous && !replacePending) return;
        previous?.abort();
        const ctl = new AbortController();
        requests.current.set(requestKey, ctl);
        try {
          const res = await fetch(
            `/coddy/sessions/${encodeURIComponent(key)}/${kind}`,
            {
              headers: { [HDR]: key },
              signal: ctl.signal,
            },
          );
          if (!res.ok) return;
          const data: unknown = await res.json();
          if (
            mounted.current &&
            !ctl.signal.aborted &&
            requests.current.get(requestKey) === ctl
          )
            apply(data);
        } catch {
          // Unreachable is not idle or an empty queue. Keep the last observation
          // and retry; activity and queue reads must not block each other.
        } finally {
          if (requests.current.get(requestKey) === ctl)
            requests.current.delete(requestKey);
        }
      };
      await Promise.all([
        // A POST not yet admitted may read idle just before the server accepts it.
        callbacks.current.postPending(key)
          ? Promise.resolve()
          : readSnapshot("activity", (raw) => {
              const data = raw as {
                sessionId?: string;
                turnActive?: boolean;
              } | null;
              if (
                !data ||
                (activity.current.get(key)?.revision ?? 0) !== revision ||
                callbacks.current.postPending(key) ||
                data.sessionId !== key ||
                typeof data.turnActive !== "boolean"
              )
                return;
              const wasActive = get(key) === true;
              observe(key, data.turnActive);
              const progress = turnProgressFromActivity(data, Date.now());
              if (progress || !data.turnActive)
                callbacks.current.onTurnProgress?.(key, progress);
              if (notify)
                callbacks.current.onReconcile(key, data.turnActive, wasActive);
            }),
        readSnapshot("queue", (raw) => {
          const data = raw as QueuedMessageEvent | null;
          if (
            !data ||
            callbacks.current.postPending(key) ||
            !Array.isArray(data.messages) ||
            typeof data.version !== "number"
          )
            return;
          applyQueueSnapshot({
            messages: data.messages.filter(
              (row) =>
                row &&
                typeof row.id === "string" &&
                typeof row.text === "string",
            ),
            version: data.version,
          });
        }),
      ]);
    },
    [get, observe],
  );

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      for (const ctl of requests.current.values()) ctl.abort();
      requests.current.clear();
    };
  }, []);

  useEffect(() => {
    const sid = options.sessionId.trim();
    if (!sid) return;
    void refresh(sid);
    const timer = window.setInterval(() => {
      // Even with events connected, reconcile an active turn: a relay can fail
      // independently, and cancellation is only a best-effort acknowledgement.
      if (get(sid) !== false || !callbacks.current.connected)
        void refresh(sid, true, false);
    }, RECONCILE_MS);
    return () => window.clearInterval(timer);
  }, [options.sessionId, get, refresh]);

  const ready = useCallback(() => {
    const ids = new Set([callbacks.current.sessionId.trim()]);
    for (const [sid, state] of activity.current) if (state.active) ids.add(sid);
    for (const sid of ids) if (sid) void refresh(sid);
  }, [refresh]);

  return { get, generation, observe, refresh, ready, epoch };
}
