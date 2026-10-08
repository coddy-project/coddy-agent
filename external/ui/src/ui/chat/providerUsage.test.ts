import { describe, expect, test, vi } from "vitest";
import {
  fetchProviderUsage,
  formatResetTime,
  formatRub,
  isRemoteBusyEnd,
  isRemoteBusyUpdate,
  modelBlocked,
  modelUnlimited,
  remoteBusy,
  summarizeUsage,
  usageBannerKey,
  usageBlockKind,
  usageIsNewer,
  usageMatchesModel,
  usageNextReadMs,
  usagePassedResetKey,
  usagePercent,
  usagePlanLabel,
  usageProviderOf,
  usageProviderTitle,
  usageSubjectKey,
  usageWindowLabelKey,
  USAGE_TIMER_MAX_MS,
  type ProviderUsage,
  type UsageWindow,
} from "./providerUsage";

const now = new Date("2026-09-06T17:47:12Z");

function windowAt(u: ProviderUsage, i: number): UsageWindow {
  const w = u.windows?.[i];
  if (!w) throw new Error(`no window ${i}`);
  return w;
}

function fixture(): ProviderUsage {
  return {
    sessionUpdate: "provider_usage",
    provider: "neuraldeep",
    providerType: "neuraldeep",
    observedAt: "2026-09-06T17:47:02Z",
    fetchedAt: "2026-09-06T17:47:10Z",
    plan: "pro",
    keyName: "coddy",
    windows: [
      {
        id: "session",
        label: "3h",
        used: 407,
        limit: 15000,
        remaining: 14593,
        usedPercent: 2.71,
        resetsAt: "2026-09-06T17:59:59Z",
        resetInSec: 777,
      },
      {
        id: "week",
        label: "week",
        used: 9981,
        limit: 150000,
        remaining: 140019,
        usedPercent: 6.65,
        resetsAt: "2026-09-07T00:00:00Z",
        resetInSec: 22378,
      },
      {
        id: "day",
        label: "day",
        usedPercent: 0,
        resetsAt: "2026-09-07T00:00:00Z",
        resetInSec: 22378,
      },
    ],
    rate: { used: 2, limit: 120, remaining: 118, resetInSec: 58 },
    wallet: { balanceRub: -1229.24, spentRub30d: 2000.74 },
    blocked: false,
    unlimitedModels: ["qwen3.6-35b-a3b"],
  };
}

describe("providerUsage helpers", () => {
  test("selector parts and the unlimited comparison", () => {
    expect(usageProviderOf("neuraldeep/qwen3.8-27b")).toBe("neuraldeep");
    expect(usageProviderOf("plain")).toBe("");
    expect(modelUnlimited(fixture(), "neuraldeep/qwen3.6-35b-a3b")).toBe(true);
    expect(modelUnlimited(fixture(), "neuraldeep/qwen3.8-27b")).toBe(false);
    expect(
      modelUnlimited({ ...fixture(), unlimited: true }, "neuraldeep/x"),
    ).toBe(true);
  });

  test("the plan name gets a capital, nothing else", () => {
    expect(usagePlanLabel("pro")).toBe("Pro");
    expect(usagePlanLabel("coder")).toBe("Coder");
    expect(usagePlanLabel(" starter ")).toBe("Starter");
    expect(usagePlanLabel("")).toBe("");
    expect(usagePlanLabel(undefined)).toBe("");
  });

  test("percent, rubles and reset time", () => {
    expect(usagePercent(2.71)).toBe(3);
    expect(usagePercent(140)).toBe(100);
    expect(usagePercent(Number.NaN)).toBe(0);
    expect(formatRub(-1229.24)).toBe("-1 229 ₽");
    expect(formatRub(1250)).toBe("1 250 ₽");
    expect(formatRub(999)).toBe("999 ₽");
    expect(formatResetTime("2026-09-06T17:59:59Z", now, "en-US")).toMatch(/59/);
    expect(formatResetTime("2026-09-09T03:00:00Z", now, "en-US")).toMatch(
      /Wed|Tue/,
    );
    expect(formatResetTime("2026-09-20T03:00:00Z", now, "en-US")).toMatch(
      /Sep.*\d{1,2}:\d{2}/,
    );
    expect(formatResetTime(undefined, now)).toBe("");
  });

  test("summary for a metered key, the threshold and the wallet", () => {
    const s = summarizeUsage(fixture(), "neuraldeep/qwen3.8-27b");
    expect(s.kind).toBe("metered");
    if (s.kind !== "metered") return;
    expect(s.session?.usedPercent).toBe(2.71);
    expect(s.warn).toBe(false);
    expect(s.wallet?.balanceRub).toBeLessThan(0);
    const warm = fixture();
    windowAt(warm, 0).usedPercent = 85;
    const w = summarizeUsage(warm, "neuraldeep/qwen3.8-27b");
    expect(w.kind === "metered" && w.warn).toBe(true);
    expect(usageBannerKey(warm)).toBe(
      "neuraldeep@session@2026-09-06T17:59:59Z",
    );
  });

  test("summary hides foreign providers, unsupported and empty snapshots", () => {
    expect(summarizeUsage(fixture(), "stub/model").kind).toBe("none");
    expect(
      summarizeUsage(
        { provider: "neuraldeep", unsupported: true },
        "neuraldeep/x",
      ).kind,
    ).toBe("none");
    expect(
      summarizeUsage(
        { provider: "neuraldeep", error: "unavailable" },
        "neuraldeep/x",
      ).kind,
    ).toBe("none");
    expect(summarizeUsage(null, "neuraldeep/x").kind).toBe("none");
  });

  test("unlimited model, rejected key and blocks", () => {
    expect(summarizeUsage(fixture(), "neuraldeep/qwen3.6-35b-a3b").kind).toBe(
      "unlimited",
    );
    const rejected = summarizeUsage(
      { ...fixture(), error: "unauthorized" },
      "neuraldeep/x",
    );
    expect(rejected).toEqual({ kind: "unauthorized", provider: "neuraldeep" });
    const blocked: ProviderUsage = {
      ...fixture(),
      blocked: true,
      blockers: ["session_exhausted"],
      retryAt: "2026-09-06T17:59:59Z",
      retryInSec: 767,
    };
    const b = summarizeUsage(blocked, "neuraldeep/x");
    expect(b.kind === "blocked" && b.block).toBe("window");
    expect(usageBlockKind({ ...blocked, retryInSec: 42 })).toBe("rate");
    expect(usageBlockKind({ ...blocked, blockers: ["wallet_empty"] })).toBe(
      "wallet",
    );
    expect(usageBlockKind({ ...blocked, blockers: ["user_blocked"] })).toBe(
      "account",
    );
    expect(usageBlockKind({ ...blocked, blockers: ["mystery"] })).toBe("other");
    expect(usageBannerKey(blocked)).toBe(
      "neuraldeep@blocked@2026-09-06T17:59:59Z@session_exhausted",
    );
    expect(usageBannerKey({ ...blocked, provider: "nd-work" })).not.toBe(
      usageBannerKey(blocked),
    );
  });

  test("a tie prefers the hub read, long delays are capped, snapshots order by read time", () => {
    const tie = fixture();
    for (const w of tie.windows ?? []) w.resetInSec = 0;
    windowAt(tie, 0).resetInSec = 9;
    tie.refreshPending = true;
    tie.refreshInSec = 9;
    expect(usageNextReadMs(tie)).toEqual({ delayMs: 11_000, forced: true });
    const long: ProviderUsage = {
      ...fixture(),
      windows: [],
      blocked: true,
      retryInSec: 40 * 24 * 3600,
    };
    expect(usageNextReadMs(long)).toEqual({
      delayMs: USAGE_TIMER_MAX_MS,
      forced: true,
    });
    const older: ProviderUsage = {
      ...fixture(),
      fetchedAt: "2026-09-06T17:47:00Z",
    };
    const newer: ProviderUsage = {
      ...fixture(),
      fetchedAt: "2026-09-06T17:47:20Z",
    };
    expect(usageIsNewer(newer, older)).toBe(true);
    expect(usageIsNewer(older, newer)).toBe(false);
    expect(usageIsNewer(older, older)).toBe(true);
    expect(usageIsNewer(newer, null)).toBe(true);
    expect(usageIsNewer({ provider: "neuraldeep" }, newer)).toBe(false);
    expect(usageIsNewer(newer, { provider: "neuraldeep" })).toBe(true);
    expect(
      usageIsNewer(older, {
        provider: "nd-work",
        fetchedAt: "2026-09-06T17:47:40Z",
      }),
    ).toBe(true);
    expect(usageWindowLabelKey({ id: "week" })).toBe("usage.window.week");
    expect(usageWindowLabelKey({ id: "day" })).toBe("usage.window.day");
    expect(usageWindowLabelKey({ id: "session" })).toBe("");
  });

  test("next read: resets and retries reach the hub, a deferred refresh reads the cache", () => {
    expect(usageNextReadMs(fixture())).toEqual({
      delayMs: 777 * 1000 + 2000,
      forced: true,
    });
    const deferred = fixture();
    for (const w of deferred.windows!) w.resetInSec = 0;
    deferred.refreshPending = true;
    deferred.refreshInSec = 9;
    expect(usageNextReadMs(deferred)).toEqual({
      delayMs: 11_000,
      forced: false,
    });
    expect(usagePassedResetKey(deferred)).toBe("session@2026-09-06T17:59:59Z");
    const rateOnly = fixture();
    for (const w of rateOnly.windows!) w.resetInSec = 0;
    expect(usageNextReadMs(rateOnly).delayMs).toBe(0);
    // A passed day reset is a real refresh signal for the quota sources;
    // NeuralDeep's day meter is a wallet budget that resets constantly.
    const devin = {
      provider: "devin",
      providerType: "devin",
      windows: [
        {
          id: "day",
          label: "day",
          usedPercent: 0,
          resetsAt: "2026-09-07T00:00:00Z",
        },
      ],
    };
    expect(usagePassedResetKey(devin)).toBe("day@2026-09-07T00:00:00Z");
    const ndDay = fixture();
    for (const w of ndDay.windows!) w.resetInSec = 0;
    ndDay.windows = ndDay.windows!.filter((w) => w.id === "day");
    expect(usagePassedResetKey(ndDay)).toBe("");
  });

  test("REST envelope", async () => {
    const calls: string[] = [];
    const fetchImpl = vi.fn(async (url: string) => {
      calls.push(url);
      if (url.includes("/stub/")) {
        return new Response(JSON.stringify({ ok: false, unsupported: true }), {
          status: 200,
        });
      }
      if (url.includes("/gone/")) return new Response("", { status: 404 });
      if (url.includes("?refresh=1")) {
        return new Response(
          JSON.stringify({
            ok: false,
            error: "unavailable",
            usage: { ...fixture(), stale: true, error: "unavailable" },
          }),
          { status: 200 },
        );
      }
      return new Response(JSON.stringify({ ok: true, usage: fixture() }), {
        status: 200,
      });
    }) as unknown as typeof fetch;
    const ok = await fetchProviderUsage("neuraldeep", false, fetchImpl);
    expect(ok.ok && ok.usage.plan).toBe("pro");
    expect(await fetchProviderUsage("stub", false, fetchImpl)).toEqual({
      ok: false,
      unsupported: true,
    });
    expect(await fetchProviderUsage("gone", false, fetchImpl)).toEqual({
      ok: false,
      unsupported: true,
    });
    const stale = await fetchProviderUsage("neuraldeep", true, fetchImpl);
    expect(!stale.ok && "error" in stale && stale.error).toBe("unavailable");
    expect(!stale.ok && "usage" in stale && stale.usage?.stale).toBe(true);
    expect(calls[3]).toBe("/coddy/providers/neuraldeep/usage?refresh=1");
  });

  test("the model rides along as ?model=, before refresh", async () => {
    const calls: string[] = [];
    const fetchImpl = vi.fn(async (url: string) => {
      calls.push(url);
      return new Response(JSON.stringify({ ok: true, usage: fixture() }), {
        status: 200,
      });
    }) as unknown as typeof fetch;
    await fetchProviderUsage("lab", false, fetchImpl, "terra");
    await fetchProviderUsage("lab", true, fetchImpl, "terra");
    await fetchProviderUsage("lab", false, fetchImpl, "gpt 5/x");
    await fetchProviderUsage("lab", false, fetchImpl, "");
    expect(calls).toEqual([
      "/coddy/providers/lab/usage?model=terra",
      "/coddy/providers/lab/usage?model=terra&refresh=1",
      "/coddy/providers/lab/usage?model=gpt%205%2Fx",
      "/coddy/providers/lab/usage",
    ]);
  });

  test("an unsupported answer says which type and alias it is about", async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            ok: false,
            unsupported: true,
            provider: "lab",
            providerType: "coddy",
            model: "terra",
          }),
          { status: 200 },
        ),
    ) as unknown as typeof fetch;
    expect(await fetchProviderUsage("lab", false, fetchImpl, "terra")).toEqual({
      ok: false,
      unsupported: true,
      providerType: "coddy",
      model: "terra",
    });
  });

  test("an alias no row names is an error, not an unsupported provider", async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(JSON.stringify({ ok: false, error: "unknown_model" }), {
          status: 404,
        }),
    ) as unknown as typeof fetch;
    expect(await fetchProviderUsage("lab", false, fetchImpl, "ghost")).toEqual({
      ok: false,
      error: "unknown_model",
      usage: null,
    });
  });
});

/* A model gate on a healthy account (10.09.26). The hub refuses one model of
 * the catalogue and leaves the rest working, so `blocked` stays false and only
 * `blockedModels[]` says the selected model is out. Without reading it the
 * composer showed a green account while every request came back 429. */
describe("a model blocked on a green account", () => {
  const blocked = (): ProviderUsage => ({
    ...fixture(),
    blockedModels: [
      {
        model: "kimi-k2.6",
        blocker: "kimi_budget_exhausted",
        retryAt: "2026-10-09T20:15:41Z",
        retryInSec: 2860119,
      },
    ],
  });

  test("the selector suffix is matched case-insensitively", () => {
    expect(modelBlocked(blocked(), "neuraldeep/KIMI-K2.6")?.blocker).toBe(
      "kimi_budget_exhausted",
    );
    expect(modelBlocked(blocked(), "neuraldeep/qwen3.8-27b")).toBeNull();
    expect(modelBlocked(null, "neuraldeep/kimi-k2.6")).toBeNull();
  });

  test("the summary reports the block with its reset time", () => {
    const s = summarizeUsage(blocked(), "neuraldeep/kimi-k2.6");
    expect(s.kind).toBe("blocked");
    if (s.kind !== "blocked") return;
    expect(s.blocker).toBe("kimi_budget_exhausted");
    expect(s.retryAt).toBe("2026-10-09T20:15:41Z");
    expect(s.model).toBe("kimi-k2.6");
  });

  test("another model on the same key stays metered", () => {
    expect(summarizeUsage(blocked(), "neuraldeep/qwen3.8-27b").kind).toBe(
      "metered",
    );
  });

  test("the banner key follows the blocked model, so a new block is announced", () => {
    expect(usageBannerKey(blocked(), "neuraldeep/kimi-k2.6")).not.toBe(
      usageBannerKey(fixture(), "neuraldeep/kimi-k2.6"),
    );
  });
});

// The wait of a call to a model another Coddy shares for a free stream slot of
// the remote: the agent reports it as a provider_usage update with resuming
// and the blocker remote_busy, whatever agent.wait_for_limit_reset says. It is
// no account limit, and nothing in it is the account's to read again.
describe("a wait for a free slot of the remote", () => {
  function busy(extra: Partial<ProviderUsage> = {}): ProviderUsage {
    return {
      sessionUpdate: "provider_usage",
      provider: "lab",
      providerType: "coddy",
      fetchedAt: "2026-09-06T17:47:10Z",
      blocked: true,
      blockers: ["remote_busy"],
      retryAt: "2026-09-06T17:47:42Z",
      retryInSec: 30,
      resuming: true,
      ...extra,
    };
  }

  test("the blocker is told apart from a usage limit", () => {
    expect(usageBlockKind(busy())).toBe("busy");
    expect(remoteBusy(busy())).toBe(true);
    expect(remoteBusy(fixture())).toBe(false);
    expect(remoteBusy(null)).toBe(false);
  });

  test("the summary of the selected row is a block of that kind", () => {
    const s = summarizeUsage(busy(), "lab/terra");
    expect(s.kind).toBe("blocked");
    if (s.kind !== "blocked") return;
    expect(s.block).toBe("busy");
    expect(s.blocker).toBe("remote_busy");
    expect(s.retryAt).toBe("2026-09-06T17:47:42Z");
    expect(summarizeUsage(busy(), "other/terra").kind).toBe("none");
  });

  test("a countdown that is re-sent keeps one dismissal key and schedules no read", () => {
    expect(usageBannerKey(busy(), "lab/terra")).toBe("lab@remote_busy");
    expect(
      usageBannerKey(
        busy({ retryAt: "2026-09-06T17:47:43Z", retryInSec: 29 }),
        "lab/terra",
      ),
    ).toBe("lab@remote_busy");
    // The remote's slot is not the account's window: nothing to read again
    // when the budget ends, the clearing update takes the notice down.
    expect(usageNextReadMs(busy())).toEqual({ delayMs: 0, forced: false });
    // A limit wait still schedules its read at the reset.
    expect(
      usageNextReadMs({ ...fixture(), blocked: true, retryInSec: 30 }).delayMs,
    ).toBeGreaterThan(0);
  });
});

// A coddy row's usage is the remote account's, read per alias (a remote can
// share rows of two accounts through one local provider row): the update
// carries the alias in `model`, and every match below goes through it.
describe("a coddy row's usage is per alias", () => {
  function remote(extra: Partial<ProviderUsage> = {}): ProviderUsage {
    return {
      sessionUpdate: "provider_usage",
      provider: "lab",
      providerType: "coddy",
      model: "terra",
      fetchedAt: "2026-09-06T17:47:10Z",
      windows: [
        {
          id: "session",
          label: "5h",
          usedPercent: 62,
          resetsAt: "2026-09-06T19:00:00Z",
          resetInSec: 4400,
        },
      ],
      ...extra,
    };
  }

  test("the subject is the alias of a coddy row and the row for every other", () => {
    expect(usageSubjectKey("lab", "terra")).toBe("lab/terra");
    expect(usageSubjectKey("neuraldeep", "")).toBe("neuraldeep");
    expect(usageSubjectKey("neuraldeep", undefined)).toBe("neuraldeep");
  });

  test("a snapshot is shown for its own alias only", () => {
    expect(usageMatchesModel(remote(), "lab/terra")).toBe(true);
    expect(usageMatchesModel(remote(), "lab/coder")).toBe(false);
    expect(usageMatchesModel(remote(), "other/terra")).toBe(false);
    expect(summarizeUsage(remote(), "lab/terra").kind).toBe("metered");
    expect(summarizeUsage(remote(), "lab/coder").kind).toBe("none");
    // No `model` (every other type, a countdown of a remote that predates
    // phase 2) matches by the row alone, as before.
    const rowOnly = remote();
    delete rowOnly.model;
    expect(usageMatchesModel(rowOnly, "lab/coder")).toBe(true);
    expect(usageMatchesModel(fixture(), "neuraldeep/qwen3.8-27b")).toBe(true);
  });

  test("an unlimited alias and a blocked alias read from the remote's own flags", () => {
    expect(summarizeUsage(remote({ unlimited: true }), "lab/terra").kind).toBe(
      "unlimited",
    );
    const blocked = remote({
      blocked: true,
      blockers: ["model_blocked"],
      retryAt: "2026-09-06T19:00:00Z",
      retryInSec: 4400,
    });
    expect(usageBlockKind(blocked)).toBe("window");
    const s = summarizeUsage(blocked, "lab/terra");
    expect(s.kind === "blocked" && s.block).toBe("window");
    expect(s.kind === "blocked" && s.blocker).toBe("model_blocked");
    // Under a minute the same block reads as a rate limit, like any window.
    expect(usageBlockKind({ ...blocked, retryInSec: 30 })).toBe("rate");
  });

  test("a read of another alias is newer whatever its read time", () => {
    const shown = remote({ fetchedAt: "2026-09-06T17:47:40Z" });
    const other = remote({
      model: "coder",
      fetchedAt: "2026-09-06T17:47:00Z",
    });
    expect(usageIsNewer(other, shown)).toBe(true);
    // The same alias still orders by read time.
    expect(
      usageIsNewer(remote({ fetchedAt: "2026-09-06T17:47:00Z" }), shown),
    ).toBe(false);
    // A row with no alias and one with an alias are not one subject.
    const rowOnly = remote({ fetchedAt: "2026-09-06T17:47:00Z" });
    delete rowOnly.model;
    expect(usageIsNewer(rowOnly, shown)).toBe(true);
  });

  test("a dismissal of one alias's notice never hides another's", () => {
    const warm = (model: string) =>
      remote({
        model,
        windows: [
          {
            id: "session",
            label: "5h",
            usedPercent: 85,
            resetsAt: "2026-09-06T19:00:00Z",
          },
        ],
      });
    expect(usageBannerKey(warm("terra"))).toBe(
      "lab/terra@session@2026-09-06T19:00:00Z",
    );
    expect(usageBannerKey(warm("coder"))).not.toBe(
      usageBannerKey(warm("terra")),
    );
    // Every other type keeps its key.
    expect(usageBannerKey({ ...fixture(), blocked: true })).toBe(
      "neuraldeep@blocked@@",
    );
  });

  test("the heading names the alias after the row, and only for a coddy row", () => {
    expect(usageProviderTitle(remote())).toBe("lab · terra");
    // A row named like its type still names the alias.
    expect(usageProviderTitle(remote({ provider: "coddy" }))).toBe(
      "coddy · terra",
    );
    expect(usageProviderTitle(fixture())).toBe("NeuralDeep");
    const rowOnly = remote();
    delete rowOnly.model;
    expect(usageProviderTitle(rowOnly)).toBe("lab");
  });
});

// The end of the countdown is an update on the existing fields (4.6): the
// blocker stays, Blocked and Resuming go. It is told apart from the countdown
// it ends by those two fields alone.
describe("the countdown and its end", () => {
  const stamp = "2026-09-06T17:47:10Z";
  const countdown: ProviderUsage = {
    provider: "lab",
    providerType: "coddy",
    model: "terra",
    fetchedAt: stamp,
    blocked: true,
    blockers: ["remote_busy"],
    retryAt: "2026-09-06T17:47:42Z",
    retryInSec: 32,
    resuming: true,
  };
  const end: ProviderUsage = {
    provider: "lab",
    providerType: "coddy",
    model: "terra",
    fetchedAt: stamp,
    blockers: ["remote_busy"],
  };

  test("both belong to the countdown, only one of them ends it", () => {
    expect(isRemoteBusyUpdate(countdown)).toBe(true);
    expect(isRemoteBusyUpdate(end)).toBe(true);
    expect(isRemoteBusyEnd(countdown)).toBe(false);
    expect(isRemoteBusyEnd(end)).toBe(true);
    // Blocked or resuming alone keeps it a countdown.
    expect(isRemoteBusyEnd({ ...end, resuming: true })).toBe(false);
    expect(isRemoteBusyEnd({ ...end, blocked: true })).toBe(false);
  });

  test("a snapshot or a limit wait is neither", () => {
    expect(isRemoteBusyUpdate(fixture())).toBe(false);
    expect(isRemoteBusyEnd(fixture())).toBe(false);
    const limitWait: ProviderUsage = {
      provider: "lab",
      providerType: "coddy",
      blocked: true,
      blockers: ["session_exhausted"],
      resuming: true,
    };
    expect(isRemoteBusyUpdate(limitWait)).toBe(false);
    expect(isRemoteBusyUpdate(null)).toBe(false);
    expect(isRemoteBusyEnd(undefined)).toBe(false);
  });

  test("the end is no wait: it keeps the key of the row and shows nothing", () => {
    expect(remoteBusy(end)).toBe(false);
    expect(usageNextReadMs(end)).toEqual({ delayMs: 0, forced: false });
    expect(usageBannerKey(countdown, "lab/terra")).toBe(
      "lab/terra@remote_busy",
    );
  });
});
