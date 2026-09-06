import { describe, expect, test, vi } from "vitest";
import {
  fetchProviderUsage,
  formatResetTime,
  formatRub,
  modelUnlimited,
  summarizeUsage,
  usageBannerKey,
  usageBlockKind,
  usageNextReadMs,
  usagePassedResetKey,
  usagePercent,
  usageProviderOf,
  type ProviderUsage,
} from "./providerUsage";

const now = new Date("2026-09-06T17:47:12Z");

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
      { id: "session", label: "3h", used: 407, limit: 15000, remaining: 14593, usedPercent: 2.71, resetsAt: "2026-09-06T17:59:59Z", resetInSec: 777 },
      { id: "week", label: "week", used: 9981, limit: 150000, remaining: 140019, usedPercent: 6.65, resetsAt: "2026-09-07T00:00:00Z", resetInSec: 22378 },
      { id: "day", label: "day", usedPercent: 0, resetsAt: "2026-09-07T00:00:00Z", resetInSec: 22378 },
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
    expect(modelUnlimited({ ...fixture(), unlimited: true }, "neuraldeep/x")).toBe(true);
  });

  test("percent, rubles and reset time", () => {
    expect(usagePercent(2.71)).toBe(3);
    expect(usagePercent(140)).toBe(100);
    expect(usagePercent(Number.NaN)).toBe(0);
    expect(formatRub(-1229.24)).toBe("-1 229 ₽");
    expect(formatRub(1250)).toBe("1 250 ₽");
    expect(formatRub(999)).toBe("999 ₽");
    expect(formatResetTime("2026-09-06T17:59:59Z", now, "en-US")).toMatch(/59/);
    expect(formatResetTime("2026-09-09T03:00:00Z", now, "en-US")).toMatch(/Wed|Tue/);
    expect(formatResetTime("2026-09-20T03:00:00Z", now, "en-US")).toMatch(/Sep/);
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
    warm.windows![0].usedPercent = 85;
    const w = summarizeUsage(warm, "neuraldeep/qwen3.8-27b");
    expect(w.kind === "metered" && w.warn).toBe(true);
    expect(usageBannerKey(warm)).toBe("session@2026-09-06T17:59:59Z");
  });

  test("summary hides foreign providers, unsupported and empty snapshots", () => {
    expect(summarizeUsage(fixture(), "stub/model").kind).toBe("none");
    expect(summarizeUsage({ provider: "neuraldeep", unsupported: true }, "neuraldeep/x").kind).toBe("none");
    expect(summarizeUsage({ provider: "neuraldeep", error: "unavailable" }, "neuraldeep/x").kind).toBe("none");
    expect(summarizeUsage(null, "neuraldeep/x").kind).toBe("none");
  });

  test("unlimited model, rejected key and blocks", () => {
    expect(summarizeUsage(fixture(), "neuraldeep/qwen3.6-35b-a3b").kind).toBe("unlimited");
    const rejected = summarizeUsage({ ...fixture(), error: "unauthorized" }, "neuraldeep/x");
    expect(rejected).toEqual({ kind: "unauthorized", provider: "neuraldeep" });
    const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
    const b = summarizeUsage(blocked, "neuraldeep/x");
    expect(b.kind === "blocked" && b.block).toBe("window");
    expect(usageBlockKind({ ...blocked, retryInSec: 42 })).toBe("rate");
    expect(usageBlockKind({ ...blocked, blockers: ["wallet_empty"] })).toBe("wallet");
    expect(usageBlockKind({ ...blocked, blockers: ["user_blocked"] })).toBe("account");
    expect(usageBlockKind({ ...blocked, blockers: ["mystery"] })).toBe("other");
    expect(usageBannerKey(blocked)).toBe("blocked@2026-09-06T17:59:59Z@session_exhausted");
  });

  test("next read: resets and retries reach the hub, a deferred refresh reads the cache", () => {
    expect(usageNextReadMs(fixture())).toEqual({ delayMs: 777 * 1000 + 2000, forced: true });
    const deferred = fixture();
    for (const w of deferred.windows!) w.resetInSec = 0;
    deferred.refreshPending = true;
    deferred.refreshInSec = 9;
    expect(usageNextReadMs(deferred)).toEqual({ delayMs: 11_000, forced: false });
    expect(usagePassedResetKey(deferred)).toBe("session@2026-09-06T17:59:59Z");
    const rateOnly = fixture();
    for (const w of rateOnly.windows!) w.resetInSec = 0;
    expect(usageNextReadMs(rateOnly).delayMs).toBe(0);
  });

  test("REST envelope", async () => {
    const calls: string[] = [];
    const fetchImpl = vi.fn(async (url: string) => {
      calls.push(url);
      if (url.includes("/stub/")) {
        return new Response(JSON.stringify({ ok: false, unsupported: true }), { status: 200 });
      }
      if (url.includes("/gone/")) return new Response("", { status: 404 });
      if (url.includes("?refresh=1")) {
        return new Response(JSON.stringify({ ok: false, error: "unavailable", usage: { ...fixture(), stale: true, error: "unavailable" } }), { status: 200 });
      }
      return new Response(JSON.stringify({ ok: true, usage: fixture() }), { status: 200 });
    }) as unknown as typeof fetch;
    const ok = await fetchProviderUsage("neuraldeep", false, fetchImpl);
    expect(ok.ok && ok.usage.plan).toBe("pro");
    expect(await fetchProviderUsage("stub", false, fetchImpl)).toEqual({ ok: false, unsupported: true });
    expect(await fetchProviderUsage("gone", false, fetchImpl)).toEqual({ ok: false, unsupported: true });
    const stale = await fetchProviderUsage("neuraldeep", true, fetchImpl);
    expect(!stale.ok && "error" in stale && stale.error).toBe("unavailable");
    expect(!stale.ok && "usage" in stale && stale.usage?.stale).toBe(true);
    expect(calls[3]).toBe("/coddy/providers/neuraldeep/usage?refresh=1");
  });
});
