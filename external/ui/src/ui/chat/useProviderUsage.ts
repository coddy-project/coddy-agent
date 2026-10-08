import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchProviderUsage,
  isRemoteBusyEnd,
  isRemoteBusyUpdate,
  usageBannerKey,
  usageIsNewer,
  usageMatchesSubject,
  usageModelOf,
  usageNextReadMs,
  usagePassedResetKey,
  usageProviderOf,
  usageSubjectKey,
  USAGE_FOLLOW_UP_MS,
  type ProviderUsage,
} from "./providerUsage";

const DISMISS_STORAGE_KEY = "coddy_usage_banner_dismissed";

/** How long a row that answered "unsupported" is left alone (the Go remote client uses the same). */
export const USAGE_UNSUPPORTED_TTL_MS = 5 * 60_000;

/**
 * Grace added to the countdown's own budget before the surface drops it: a
 * countdown that was not ended (an end update that never arrived) is taken down
 * this long after its `retryInSec` ran out, counted from its receipt on the
 * surface's own clock. Never compared with the server's absolute `retryAt`.
 */
export const BUSY_EXPIRY_GRACE_MS = 2000;

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

/** The later of two RFC 3339 UTC stamps, compared as strings; "" is the earliest. */
function laterStamp(a: string, b: string): string {
  return b > a ? b : a;
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
 *
 * A provider of type coddy is read per alias (the part of the selector after
 * the row; the answers say what the row is, and the hook remembers it): its
 * snapshot, its request in flight and its countdown belong to the alias, and a
 * switch of alias reads again, where a switch of model inside any other row
 * reads nothing.
 *
 * The countdown of a call waiting for a free slot of a remote Coddy is a state
 * of its own, `busy`, beside the snapshot `usage` (docs/plans/
 * remote-model-provider-phase2.md, 4.6): an update carrying the blocker
 * remote_busy goes there and nothing else does; no snapshot and no
 * "unsupported" answer touches it. The slot holds the last accepted countdown
 * and a tombstone, the largest stamp of an end or of a countdown dropped:
 *
 *   - a countdown is accepted iff its stamp is greater than the tombstone and
 *     not older than the slot's (a replay of a countdown that ended is ignored);
 *   - an end (the update with blocked and resuming gone) is accepted iff it is
 *     not older than the slot's, wins a tie, empties the slot and raises the
 *     tombstone, also when no countdown was held;
 *   - the slot is dropped (tombstone raised) when the viewed session's turn
 *     ends and when the countdown's `retryInSec` plus two seconds have passed
 *     since it was received, whatever the server's own clock says;
 *   - a change of subject drops the slot and the tombstone.
 */
export function useProviderUsage(params: {
  sessionId: string;
  llmModel: string;
  /** Increments when a turn of the viewed session finished. */
  turnEpoch: number;
  fetchImpl?: typeof fetch;
}) {
  const provider = usageProviderOf(params.llmModel);
  const alias = usageModelOf(params.llmModel);
  const [usage, setUsage] = useState<ProviderUsage | null>(null);
  const [busy, setBusy] = useState<ProviderUsage | null>(null);
  const [dismissedKey, setDismissedKey] = useState<string>(() =>
    readDismissed(),
  );
  // Rows that answered "unsupported", each with the time the mark expires.
  // None is ever recorded for a coddy row: the manager's own memory per alias
  // is the only one, so the two do not add up.
  const unsupportedRef = useRef<Map<string, number>>(new Map());
  // What the answers said each row is. A coddy row is read per alias; every
  // other row, and a row nothing has answered for yet, per row.
  const kindsRef = useRef<Map<string, "coddy" | "other">>(new Map());
  // Sequence of the reads issued per subject: only the latest one issued for a
  // subject applies.
  const seqRef = useRef<Map<string, number>>(new Map());
  const timerRef = useRef<number | null>(null);
  const followUpRef = useRef<string>("");
  const providerRef = useRef(provider);
  providerRef.current = provider;
  const aliasRef = useRef(alias);
  aliasRef.current = alias;
  const fetchImpl = params.fetchImpl;

  // The countdown slot, its tombstone and its expiry timer. The slot lives in
  // a ref so that two updates in one batch see each other; `busy` mirrors it
  // for rendering.
  const busyRef = useRef<ProviderUsage | null>(null);
  const tombstoneRef = useRef("");
  const busyTimerRef = useRef<number | null>(null);

  const learn = useCallback(
    (name: string, providerType: string | undefined) => {
      // Only an answer that names its type says what the row is.
      if (!name || !providerType) return;
      kindsRef.current.set(name, providerType === "coddy" ? "coddy" : "other");
    },
    [],
  );
  const isCoddyRow = useCallback(
    (name: string) => kindsRef.current.get(name) === "coddy",
    [],
  );
  const subjectOf = useCallback(
    (name: string, model: string) =>
      usageSubjectKey(name, isCoddyRow(name) ? model : ""),
    [isCoddyRow],
  );

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

  const clearBusyTimer = useCallback(() => {
    if (busyTimerRef.current !== null) {
      window.clearTimeout(busyTimerRef.current);
      busyTimerRef.current = null;
    }
  }, []);

  /** Empties the slot; the countdown it held can no longer come back. */
  const dropBusy = useCallback(() => {
    clearBusyTimer();
    const held = busyRef.current;
    if (!held) return;
    tombstoneRef.current = laterStamp(
      tombstoneRef.current,
      held.fetchedAt ?? "",
    );
    busyRef.current = null;
    setBusy(null);
  }, [clearBusyTimer]);

  const acceptBusy = useCallback(
    (u: ProviderUsage) => {
      const stamp = u.fetchedAt ?? "";
      const held = busyRef.current;
      const heldStamp = held?.fetchedAt ?? "";
      if (isRemoteBusyEnd(u)) {
        // Not older than the countdown held; it wins a tie.
        if (held && stamp < heldStamp) return;
        clearBusyTimer();
        tombstoneRef.current = laterStamp(tombstoneRef.current, stamp);
        if (held) {
          busyRef.current = null;
          setBusy(null);
        }
        return;
      }
      // Newer than whatever ended or was dropped, and not older than the
      // countdown held: an old countdown replayed by a relay, a mirror or a
      // reconnect stays out.
      if (tombstoneRef.current !== "" && !(stamp > tombstoneRef.current))
        return;
      if (held && stamp < heldStamp) return;
      busyRef.current = u;
      setBusy(u);
      clearBusyTimer();
      // The surface's own net: the countdown's budget after receipt plus the
      // grace, on this clock. An end that never arrives cannot keep it up.
      const budgetMs = Math.max(0, u.retryInSec ?? 0) * 1000;
      busyTimerRef.current = window.setTimeout(
        () => {
          busyTimerRef.current = null;
          dropBusy();
        },
        Math.min(budgetMs + BUSY_EXPIRY_GRACE_MS, 2 ** 31 - 1),
      );
    },
    [clearBusyTimer, dropBusy],
  );

  // One update, from a read or from a push: a countdown or its end to the
  // slot, every other to the snapshot.
  const apply = useCallback(
    (next: ProviderUsage) => {
      if (isRemoteBusyUpdate(next)) {
        acceptBusy(next);
        return;
      }
      accept(next);
    },
    [accept, acceptBusy],
  );

  const read = useCallback(
    async (name: string, model: string, refresh: boolean) => {
      if (!name) return;
      const until = unsupportedRef.current.get(name);
      if (until !== undefined) {
        if (until > Date.now()) return;
        unsupportedRef.current.delete(name);
      }
      const key = subjectOf(name, model);
      const seq = (seqRef.current.get(key) ?? 0) + 1;
      seqRef.current.set(key, seq);
      // The answer is about what was asked: the row, and for a coddy row the
      // alias, must still be the ones on screen when it lands.
      const viewing = () =>
        providerRef.current === name &&
        (!isCoddyRow(name) || aliasRef.current === model);
      try {
        const answer = await fetchProviderUsage(
          name,
          refresh,
          fetchImpl,
          model,
        );
        if (!viewing()) return;
        if (!answer.ok && "unsupported" in answer) {
          learn(name, answer.providerType);
          if (!isCoddyRow(name)) {
            unsupportedRef.current.set(
              name,
              Date.now() + USAGE_UNSUPPORTED_TTL_MS,
            );
          }
          // The subject has no usage now (no source, or its usage limits
          // panel switched off in config): a snapshot shown for it must go
          // too. The countdown is not a usage answer's to end.
          setUsage((current) =>
            usageMatchesSubject(current, name, model) ? null : current,
          );
          return;
        }
        if (seqRef.current.get(key) !== seq) return;
        const next = answer.usage;
        if (next) {
          learn(name, next.providerType);
          apply({ ...next, provider: next.provider || name });
        }
      } catch {
        // A failed read keeps the last snapshot; the next trigger tries again.
      }
    },
    [fetchImpl, subjectOf, isCoddyRow, learn, apply],
  );

  // A change of subject (the row, or the alias of a coddy row) drops the
  // countdown and its tombstone: they were about the old one.
  const subjectSeenRef = useRef<string | null>(null);
  useEffect(() => {
    const now = `${provider}\u0000${alias}`;
    if (subjectSeenRef.current !== null && subjectSeenRef.current !== now) {
      clearBusyTimer();
      busyRef.current = null;
      tombstoneRef.current = "";
      setBusy(null);
    }
    subjectSeenRef.current = now;
  }, [provider, alias, clearBusyTimer]);

  // Session open and model change: a cache read for the active subject. A
  // switch of model inside a row nothing has said is coddy reads nothing; a
  // row not yet answered for, or a coddy one, reads the new alias.
  const triggerRef = useRef<{
    provider: string;
    alias: string;
    sessionId: string;
  } | null>(null);
  useEffect(() => {
    if (!provider) {
      triggerRef.current = null;
      setUsage(null);
      return;
    }
    const prev = triggerRef.current;
    triggerRef.current = { provider, alias, sessionId: params.sessionId };
    if (
      prev &&
      prev.provider === provider &&
      prev.sessionId === params.sessionId
    ) {
      if (prev.alias === alias) return;
      if (kindsRef.current.get(provider) === "other") return;
    }
    void read(provider, alias, false);
  }, [provider, alias, params.sessionId, read]);

  // A finished turn spent quota: a refresh, which the server may defer. Only
  // a new epoch triggers it; a model change alone is the cache read above.
  // It also ends a countdown the call did not end itself.
  const lastEpochRef = useRef(params.turnEpoch);
  useEffect(() => {
    if (params.turnEpoch === lastEpochRef.current) return;
    lastEpochRef.current = params.turnEpoch;
    dropBusy();
    if (!provider) return;
    void read(provider, alias, true);
  }, [params.turnEpoch, provider, alias, read, dropBusy]);

  // The schedule the snapshot implies.
  useEffect(() => {
    clearTimer();
    if (!usageMatchesSubject(usage, provider, alias)) return;
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
      void read(provider, alias, forced);
    }, delayMs);
    return clearTimer;
  }, [usage, provider, alias, read, clearTimer]);

  useEffect(() => clearTimer, [clearTimer]);
  useEffect(() => clearBusyTimer, [clearBusyTimer]);

  const applyPushed = useCallback(
    (pushed: ProviderUsage) => {
      if (!pushed || pushed.provider !== providerRef.current) return;
      learn(pushed.provider, pushed.providerType);
      // Another alias of the row is another subject: its snapshot, its
      // countdown and its end say nothing about this one.
      if (!usageMatchesSubject(pushed, providerRef.current, aliasRef.current)) {
        return;
      }
      if (isRemoteBusyUpdate(pushed)) {
        acceptBusy(pushed);
        return;
      }
      if (pushed.unsupported) {
        // The subject has no usage now, as a REST answer of unsupported says,
        // so a snapshot shown for it must go. A countdown is not this
        // update's to end: it has an end update of its own.
        setUsage((current) =>
          usageMatchesSubject(current, pushed.provider, aliasRef.current)
            ? null
            : current,
        );
        return;
      }
      accept(pushed);
    },
    [accept, acceptBusy, learn],
  );

  const dismissBanner = useCallback((key: string) => {
    setDismissedKey(key);
    writeDismissed(key);
  }, []);

  const bannerKey = usageBannerKey(usage, params.llmModel);
  return {
    usage,
    /** The countdown of a call waiting for a free slot of the remote, or null. */
    busy,
    applyPushed,
    dismissedKey: bannerKey && dismissedKey === bannerKey ? bannerKey : "",
    dismissBanner,
    refresh: () => (provider ? read(provider, alias, true) : Promise.resolve()),
  };
}
