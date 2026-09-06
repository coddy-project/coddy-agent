/**
 * Provider account usage for the composer: the quota behind the selected
 * model's provider, as the server reports it (`provider_usage` update, REST
 * `GET /coddy/providers/{name}/usage`). Today only `neuraldeep` rows have a
 * source. The snapshot is account-wide; the client compares the model
 * selector's suffix with `unlimitedModels` itself. Design record:
 * docs/plans/neuraldeep-usage.md (section 4.6).
 */

export type UsageWindow = {
  id: string;
  label: string;
  used?: number;
  limit?: number;
  remaining?: number;
  usedPercent: number;
  exhausted?: boolean;
  resetsAt?: string;
  resetInSec?: number;
};

export type ProviderUsage = {
  sessionUpdate?: string;
  provider: string;
  providerType?: string;
  observedAt?: string;
  fetchedAt?: string;
  plan?: string;
  keyName?: string;
  windows?: UsageWindow[];
  rate?: { used: number; limit: number; remaining: number; resetInSec: number };
  cooldownSec?: number;
  wallet?: { balanceRub: number; spentRub30d: number } | null;
  blocked?: boolean;
  blockers?: string[];
  retryAt?: string;
  retryInSec?: number;
  unlimited?: boolean;
  unlimitedModels?: string[];
  stale?: boolean;
  error?: string;
  unsupported?: boolean;
  refreshPending?: boolean;
  refreshInSec?: number;
};

/** Where a window's segment turns to the warning tone and the banner appears. */
export const USAGE_WARN_PERCENT = 80;
/** A timed block shorter than this reads as a rate limit with a countdown. */
export const USAGE_SHORT_BLOCK_SEC = 60;
/** Grace added to a deadline before the read, so the server has rolled the window. */
export const USAGE_RESET_GRACE_MS = 2000;
/** The single retry when a read after a passed reset still shows the old window. */
export const USAGE_FOLLOW_UP_MS = 30_000;
/** Largest delay a browser timer honours; longer ones fire at once. */
export const USAGE_TIMER_MAX_MS = 2 ** 31 - 1;

/** Provider row name of a model selector (`provider/model`). */
export function usageProviderOf(modelId: string | undefined | null): string {
  const s = (modelId ?? "").trim();
  const i = s.indexOf("/");
  return i > 0 ? s.slice(0, i) : "";
}

/** Upstream model id of a selector, the part the provider's own lists name. */
export function usageModelOf(modelId: string | undefined | null): string {
  const s = (modelId ?? "").trim();
  const i = s.indexOf("/");
  return i >= 0 ? s.slice(i + 1) : s;
}

/** True when the active model bypasses the volume windows. */
export function modelUnlimited(
  u: ProviderUsage | null | undefined,
  modelId: string,
): boolean {
  if (!u) return false;
  if (u.unlimited) return true;
  const want = usageModelOf(modelId).toLowerCase();
  if (!want) return false;
  return (u.unlimitedModels ?? []).some(
    (m) => (m ?? "").trim().toLowerCase() === want,
  );
}

/** Rounded percent for display, clamped to 0..100. */
export function usagePercent(pct: number | undefined | null): number {
  const v = typeof pct === "number" && Number.isFinite(pct) ? pct : 0;
  return Math.round(Math.min(100, Math.max(0, v)));
}

export function usageWindow(
  u: ProviderUsage | null | undefined,
  id: string,
): UsageWindow | null {
  return (u?.windows ?? []).find((w) => w.id === id) ?? null;
}

/** Reset time in the reader's clock: time of day within 24 h, weekday and
 *  time within a week, the date beyond. */
export function formatResetTime(
  resetsAt: string | undefined,
  now: Date = new Date(),
  locale?: string,
): string {
  if (!resetsAt) return "";
  const at = new Date(resetsAt);
  if (Number.isNaN(at.getTime())) return "";
  const diff = at.getTime() - now.getTime();
  const day = 24 * 3600 * 1000;
  if (diff < day) {
    return at.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  }
  if (diff < 7 * day) {
    return at.toLocaleString(locale, {
      weekday: "short",
      hour: "2-digit",
      minute: "2-digit",
    });
  }
  return at.toLocaleDateString(locale, { month: "short", day: "numeric" });
}

/** Rubles with a plain space between thousands and the ruble sign. */
export function formatRub(v: number): string {
  const rounded = Math.round(v);
  const sign = rounded < 0 ? "-" : "";
  const digits = String(Math.abs(rounded));
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, " ");
  return `${sign}${grouped} ₽`;
}

export function formatDurationSec(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

export type UsageBlockKind =
  | "window"
  | "rate"
  | "key"
  | "wallet"
  | "account"
  | "other";

const BLOCKER_KIND: Record<string, UsageBlockKind> = {
  session_exhausted: "window",
  week_exhausted: "window",
  daily_capacity_exhausted: "window",
  session_cooldown: "window",
  abuse_cooldown: "window",
  rpm_exhausted: "rate",
  key_blocked: "key",
  key_cap_blocked: "key",
  wallet_empty: "wallet",
  user_blocked: "account",
};

/** Classifies a Blocked snapshot by its first known blocker. */
export function usageBlockKind(u: ProviderUsage): UsageBlockKind {
  for (const id of u.blockers ?? []) {
    const kind = BLOCKER_KIND[id];
    if (kind) {
      if (
        kind === "window" &&
        typeof u.retryInSec === "number" &&
        u.retryInSec > 0 &&
        u.retryInSec < USAGE_SHORT_BLOCK_SEC
      ) {
        return "rate";
      }
      return kind;
    }
  }
  return "other";
}

export type UsageSummary =
  | { kind: "none" }
  | { kind: "unauthorized"; provider: string }
  | { kind: "unlimited"; wallet: ProviderUsage["wallet"] }
  | {
      kind: "blocked";
      block: UsageBlockKind;
      retryAt?: string;
      retryInSec?: number;
      blocker?: string;
    }
  | {
      kind: "metered";
      session: UsageWindow | null;
      week: UsageWindow | null;
      day: UsageWindow | null;
      warn: boolean;
      stale: boolean;
      wallet: ProviderUsage["wallet"];
    };

/** What the composer shows for the active model, if anything. */
export function summarizeUsage(
  u: ProviderUsage | null | undefined,
  modelId: string,
): UsageSummary {
  if (!u || u.unsupported) return { kind: "none" };
  if (u.provider !== usageProviderOf(modelId)) return { kind: "none" };
  if (u.error === "unauthorized") {
    return { kind: "unauthorized", provider: u.provider };
  }
  if (u.blocked) {
    const block = usageBlockKind(u);
    return {
      kind: "blocked",
      block,
      ...(u.retryAt ? { retryAt: u.retryAt } : {}),
      ...(typeof u.retryInSec === "number" ? { retryInSec: u.retryInSec } : {}),
      ...(u.blockers && u.blockers[0] ? { blocker: u.blockers[0] } : {}),
    };
  }
  if (modelUnlimited(u, modelId)) {
    return { kind: "unlimited", wallet: u.wallet ?? null };
  }
  const session = usageWindow(u, "session");
  const week = usageWindow(u, "week");
  const day = usageWindow(u, "day");
  if (!session && !week && !day && !u.wallet) {
    return { kind: "none" };
  }
  const warn = [session, week, day].some(
    (w) => !!w && (usagePercent(w.usedPercent) >= USAGE_WARN_PERCENT || !!w.exhausted),
  );
  return {
    kind: "metered",
    session,
    week,
    day,
    warn,
    stale: !!u.stale,
    wallet: u.wallet ?? null,
  };
}

/** The window that first crosses the warning threshold (banner subject). */
export function usageWarnWindow(u: ProviderUsage | null | undefined): UsageWindow | null {
  for (const w of u?.windows ?? []) {
    if (usagePercent(w.usedPercent) >= USAGE_WARN_PERCENT || w.exhausted) return w;
  }
  return null;
}

/** Milliseconds until the next read the client owes, and whether that read
 *  must reach the hub (a reset or a retry) or may come from the cache (a
 *  refresh the server deferred). Zero when nothing is pending. The
 *  per-minute rate never arms a read. */
export function usageNextReadMs(
  u: ProviderUsage | null | undefined,
): { delayMs: number; forced: boolean } {
  if (!u) return { delayMs: 0, forced: false };
  let best = 0;
  let forced = false;
  // On a tie the hub read wins: a reset that lands with a deferred refresh
  // is still a reset.
  const consider = (sec: number | undefined, hub: boolean) => {
    if (typeof sec !== "number" || sec <= 0) return;
    if (best === 0 || sec < best || (sec === best && hub)) {
      best = sec;
      forced = hub || (sec === best && forced);
    }
  };
  for (const w of u.windows ?? []) consider(w.resetInSec, true);
  consider(u.retryInSec, true);
  if (u.refreshPending) consider(u.refreshInSec, false);
  if (best === 0) return { delayMs: 0, forced: false };
  // A browser timer past 2^31-1 ms fires at once; a block the hub measures
  // in weeks waits for the cap instead of re-reading in a loop.
  const delayMs = Math.min(best * 1000 + USAGE_RESET_GRACE_MS, USAGE_TIMER_MAX_MS);
  return { delayMs, forced };
}

/** A window whose reset the snapshot says has passed, keyed for the single follow-up. */
export function usagePassedResetKey(u: ProviderUsage | null | undefined): string {
  for (const w of u?.windows ?? []) {
    // The server omits a zero resetInSec (Go omitempty): absent means the
    // reset already passed, the same as an explicit 0.
    if ((w.resetInSec ?? 0) === 0 && w.resetsAt && w.id !== "day") {
      return `${w.id}@${w.resetsAt}`;
    }
  }
  return "";
}

/**
 * Key under which a banner dismissal is remembered: the provider row, the
 * window and its period, so dismissing one account's notice never hides
 * another row's notice with the same reset time.
 */
export function usageBannerKey(u: ProviderUsage | null | undefined): string {
  if (!u) return "";
  if (u.blocked) {
    return `${u.provider}@blocked@${u.retryAt ?? ""}@${(u.blockers ?? []).join(",")}`;
  }
  const w = usageWarnWindow(u);
  return w ? `${u.provider}@${w.id}@${w.resetsAt ?? ""}` : "";
}

/**
 * The i18n key naming a window when the server's label is a plain English
 * word ("week", "day"); the session window's label is a duration the hub
 * chose ("3h") and reads the same in every language. Empty when the label
 * stands as is.
 */
export function usageWindowLabelKey(w: Pick<UsageWindow, "id">): string {
  switch (w.id) {
    case "week":
      return "usage.window.week";
    case "day":
      return "usage.window.day";
    default:
      return "";
  }
}

/**
 * Order snapshots by the server's read time: a REST answer that was issued
 * before a pushed frame, or a frame that crossed a later read, must not
 * replace the newer numbers. Snapshots without a read time (a synthetic
 * unsupported answer) never outrank one with it.
 */
export function usageIsNewer(
  next: ProviderUsage,
  current: ProviderUsage | null | undefined,
): boolean {
  if (!current || current.provider !== next.provider) return true;
  if (!next.fetchedAt) return !current.fetchedAt;
  if (!current.fetchedAt) return true;
  return next.fetchedAt >= current.fetchedAt;
}

export type ProviderUsageAnswer =
  | { ok: true; usage: ProviderUsage }
  | { ok: false; unsupported: true }
  | { ok: false; error: string; usage: ProviderUsage | null };

/** Reads the usage behind a provider row over REST. */
export async function fetchProviderUsage(
  provider: string,
  refresh: boolean,
  fetchImpl: typeof fetch = fetch,
): Promise<ProviderUsageAnswer> {
  const name = provider.trim();
  if (!name) return { ok: false, unsupported: true };
  const url = `/coddy/providers/${encodeURIComponent(name)}/usage${refresh ? "?refresh=1" : ""}`;
  const res = await fetchImpl(url, { headers: { Accept: "application/json" } });
  if (res.status === 404) return { ok: false, unsupported: true };
  if (!res.ok) {
    return { ok: false, error: "unavailable", usage: null };
  }
  const body = (await res.json()) as {
    ok?: boolean;
    unsupported?: boolean;
    error?: string;
    usage?: ProviderUsage | null;
  };
  if (body.unsupported) return { ok: false, unsupported: true };
  if (body.ok && body.usage) return { ok: true, usage: body.usage };
  return {
    ok: false,
    error: body.error || "unavailable",
    usage: body.usage ?? null,
  };
}
