import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchProviderUsage,
  usageBannerKey,
  usageIsFor,
  usageIsNewer,
  usageModelOf,
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
  // The subject of the reads is the model selector, not the row alone: a
  // coddy row's usage is read and shown per model, and every other type
  // ignores the model.
  const selector = provider ? params.llmModel.trim() : "";
  const [usage, setUsage] = useState<ProviderUsage | null>(null);
  const [dismissedKey, setDismissedKey] = useState<string>(() =>
    readDismissed(),
  );
  // Rows that answered "unsupported", each with the time the mark expires.
  const unsupportedRef = useRef<Map<string, number>>(new Map());
  // Sequence of the reads issued: only the latest one issued applies.
  const seqRef = useRef(0);
  const timerRef = useRef<number | null>(null);
  const followUpRef = useRef<string>("");
  const selectorRef = useRef(selector);
  selectorRef.current = selector;
  const fetchImpl = params.fetchImpl;

  // A snapshot replaces the state only when the server read it no earlier
  // than the one shown: a REST answer issued before a pushed frame, or a
  // frame that crossed a later read, never brings the numbers back. A
  // snapshot of another subject (another row, or another alias of a coddy
  // row) is no older or newer than it: it replaces it.
  const accept = useCallback((next: ProviderUsage) => {
    setUsage((current) =>
      !current ||
      current.provider !== next.provider ||
      (current.model ?? "") !== (next.model ?? "") ||
      usageIsNewer(next, current)
        ? next
        : current,
    );
  }, []);

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const read = useCallback(
    async (sel: string, refresh: boolean) => {
      const name = usageProviderOf(sel);
      if (!name) return;
      const until = unsupportedRef.current.get(sel);
      if (until !== undefined) {
        if (until > Date.now()) return;
        unsupportedRef.current.delete(sel);
      }
      const seq = ++seqRef.current;
      try {
        const answer = await fetchProviderUsage(
          name,
          refresh,
          fetchImpl,
          usageModelOf(sel),
        );
        if (selectorRef.current !== sel) return;
        if (!answer.ok && "unsupported" in answer) {
          unsupportedRef.current.set(
            sel,
            Date.now() + USAGE_UNSUPPORTED_TTL_MS,
          );
          // The row has no usage now (no source, or its usage limits panel
          // switched off in config): a snapshot shown for it must go too.
          setUsage((current) => (usageIsFor(current, sel) ? null : current));
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

  // Session open and model change: a cache read for the active model's row.
  useEffect(() => {
    if (!selector) {
      setUsage(null);
      return;
    }
    void read(selector, false);
  }, [selector, params.sessionId, read]);

  // A finished turn spent quota: a refresh, which the server may defer. Only
  // a new epoch triggers it; a model change alone is the cache read above.
  const lastEpochRef = useRef(params.turnEpoch);
  useEffect(() => {
    if (params.turnEpoch === lastEpochRef.current) return;
    lastEpochRef.current = params.turnEpoch;
    if (!selector) return;
    void read(selector, true);
  }, [params.turnEpoch, selector, read]);

  // The schedule the snapshot implies.
  useEffect(() => {
    clearTimer();
    if (!usage || !usageIsFor(usage, selector)) return;
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
      void read(selector, forced);
    }, delayMs);
    return clearTimer;
  }, [usage, selector, read, clearTimer]);

  useEffect(() => clearTimer, [clearTimer]);

  const applyPushed = useCallback(
    (pushed: ProviderUsage) => {
      if (!pushed || pushed.unsupported) return;
      if (!usageIsFor(pushed, selectorRef.current)) return;
      accept(pushed);
    },
    [accept],
  );

  const dismissBanner = useCallback((key: string) => {
    setDismissedKey(key);
    writeDismissed(key);
  }, []);

  const bannerKey = usageBannerKey(usage, params.llmModel);
  return {
    usage,
    applyPushed,
    dismissedKey: bannerKey && dismissedKey === bannerKey ? bannerKey : "",
    dismissBanner,
    refresh: () => (selector ? read(selector, true) : Promise.resolve()),
  };
}
