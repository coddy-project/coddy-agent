import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchProviderUsage,
  usageBannerKey,
  usageNextReadMs,
  usagePassedResetKey,
  usageProviderOf,
  USAGE_FOLLOW_UP_MS,
  type ProviderUsage,
} from "./providerUsage";

const DISMISS_STORAGE_KEY = "coddy_usage_banner_dismissed";

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
  const unsupportedRef = useRef<Set<string>>(new Set());
  const timerRef = useRef<number | null>(null);
  const followUpRef = useRef<string>("");
  const providerRef = useRef(provider);
  providerRef.current = provider;
  const fetchImpl = params.fetchImpl;

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const read = useCallback(
    async (name: string, refresh: boolean) => {
      if (!name || unsupportedRef.current.has(name)) return;
      try {
        const answer = await fetchProviderUsage(name, refresh, fetchImpl);
        if (providerRef.current !== name) return;
        if (!answer.ok && "unsupported" in answer) {
          unsupportedRef.current.add(name);
          return;
        }
        const next = answer.ok ? answer.usage : answer.usage;
        if (next) setUsage({ ...next, provider: next.provider || name });
      } catch {
        // A failed read keeps the last snapshot; the next trigger tries again.
      }
    },
    [fetchImpl],
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
    if (passed && followUpRef.current !== passed) {
      followUpRef.current = passed;
      if (delayMs === 0 || USAGE_FOLLOW_UP_MS < delayMs) {
        delayMs = USAGE_FOLLOW_UP_MS;
        forced = true;
      }
    }
    if (delayMs === 0) return;
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      void read(provider, forced);
    }, delayMs);
    return clearTimer;
  }, [usage, provider, read, clearTimer]);

  useEffect(() => clearTimer, [clearTimer]);

  const applyPushed = useCallback((pushed: ProviderUsage) => {
    if (!pushed || pushed.unsupported) return;
    if (pushed.provider !== providerRef.current) return;
    setUsage(pushed);
  }, []);

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
