import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchProviderUsage,
  usageBannerKey,
  usageIsNewer,
  usageNextReadMs,
  usagePassedResetKey,
  usageProviderOf,
  USAGE_FOLLOW_UP_MS,
  type ProviderUsage,
} from "./providerUsage";

const DISMISS_STORAGE_KEY = "coddy_usage_banner_dismissed";

/** How long a row that answered "unsupported" is left alone (the Go remote client uses the same). */
export const USAGE_UNSUPPORTED_TTL_MS = 5 * 60_000;

function readDismissed(): string {
  try {
    return window.localStorage.getItem(DISMISS_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeDismissed(key: string) {
  try {
    window.localStorage.setItem(DISMISS_STORAGE_KEY, key);
  } catch {
    // Storage may be unavailable (private mode); the dismissal is then per page load.
  }
}

/**
 * The composer's view of the provider usage behind the selected model.
 *
 * Reads over REST when a session opens or the model changes (cache read on
 * the server), after every turn of the viewed session (a refresh; the server
 * defers it inside its pacing floor and says so), and on the schedule the
 * snapshot implies: one hub read after a window's reset, one cache read when
 * the server deferred a refresh, one follow-up when a passed reset still
 * shows. Snapshots pushed by the server (`provider_usage` on the events
 * stream) are applied through `applyPushed`. Nothing polls otherwise.
 */
export function useProviderUsage(params: {
  sessionId: string;
  llmModel: string;
  /** Increments when a turn of the viewed session finished. */
  turnEpoch: number;
  fetchImpl?: typeof fetch;
}) {
  const provider = usageProviderOf(params.llmModel);
  const [usage, setUsage] = useState<ProviderUsage | null>(null);
  const [dismissedKey, setDismissedKey] = useState<string>(() => readDismissed());
  // Rows that answered "unsupported", each with the time the mark expires.
  const unsupportedRef = useRef<Map<string, number>>(new Map());
  // Sequence of the reads issued: only the latest one issued applies.
  const seqRef = useRef(0);
  const timerRef = useRef<number | null>(null);
  const followUpRef = useRef<string>("");
  const providerRef = useRef(provider);
  providerRef.current = provider;
  const fetchImpl = params.fetchImpl;

  // A snapshot replaces the state only when the server read it no earlier
  // than the one shown: a REST answer issued before a pushed frame, or a
  // frame that crossed a later read, never brings the numbers back.
  const accept = useCallback((next: ProviderUsage) => {
    setUsage((current) => (usageIsNewer(next, current) ? next : current));
  }, []);

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const read = useCallback(
    async (name: string, refresh: boolean) => {
      if (!name) return;
      const until = unsupportedRef.current.get(name);
      if (until !== undefined) {
        if (until > Date.now()) return;
        unsupportedRef.current.delete(name);
      }
      const seq = ++seqRef.current;
      try {
        const answer = await fetchProviderUsage(name, refresh, fetchImpl);
        if (providerRef.current !== name) return;
        if (!answer.ok && "unsupported" in answer) {
          unsupportedRef.current.set(name, Date.now() + USAGE_UNSUPPORTED_TTL_MS);
          return;
        }
        if (seq !== seqRef.current) return;
        const next = answer.usage;
        if (next) accept({ ...next, provider: next.provider || name });
      } catch {
        // A failed read keeps the last snapshot; the next trigger tries again.
      }
    },
    [fetchImpl, accept],
  );

  // Session open and model change: a cache read for the active provider.
  useEffect(() => {
    if (!provider) {
      setUsage(null);
      return;
    }
    void read(provider, false);
  }, [provider, params.sessionId, read]);

  // A finished turn spent quota: a refresh, which the server may defer. Only
  // a new epoch triggers it; a model change alone is the cache read above.
  const lastEpochRef = useRef(params.turnEpoch);
  useEffect(() => {
    if (params.turnEpoch === lastEpochRef.current) return;
    lastEpochRef.current = params.turnEpoch;
    if (!provider) return;
    void read(provider, true);
  }, [params.turnEpoch, provider, read]);

  // The schedule the snapshot implies.
  useEffect(() => {
    clearTimer();
    if (!usage || usage.provider !== provider) return;
    let { delayMs, forced } = usageNextReadMs(usage);
    const passed = usagePassedResetKey(usage);
    // The passed-reset follow-up is remembered only once it is the read
    // armed here; a shorter cache read that returns the same passed window
    // still gets its follow-up afterwards.
    if (
      passed &&
      followUpRef.current !== passed &&
      (delayMs === 0 || USAGE_FOLLOW_UP_MS < delayMs)
    ) {
      followUpRef.current = passed;
      delayMs = USAGE_FOLLOW_UP_MS;
      forced = true;
    }
    if (delayMs === 0) return;
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      void read(provider, forced);
    }, delayMs);
    return clearTimer;
  }, [usage, provider, read, clearTimer]);

  useEffect(() => clearTimer, [clearTimer]);

  const applyPushed = useCallback(
    (pushed: ProviderUsage) => {
      if (!pushed || pushed.unsupported) return;
      if (pushed.provider !== providerRef.current) return;
      accept(pushed);
    },
    [accept],
  );

  const dismissBanner = useCallback((key: string) => {
    setDismissedKey(key);
    writeDismissed(key);
  }, []);

  const bannerKey = usageBannerKey(usage);
  return {
    usage,
    applyPushed,
    dismissedKey: bannerKey && dismissedKey === bannerKey ? bannerKey : "",
    dismissBanner,
    refresh: () => (provider ? read(provider, true) : Promise.resolve()),
  };
}
