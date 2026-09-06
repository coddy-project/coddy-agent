import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { useProviderUsage } from "./useProviderUsage";
import type { ProviderUsage } from "./providerUsage";

function snapshot(used: number, extra: Partial<ProviderUsage> = {}): ProviderUsage {
  return {
    provider: "neuraldeep",
    providerType: "neuraldeep",
    plan: "pro",
    windows: [
      { id: "session", label: "3h", used, limit: 15000, usedPercent: (used / 15000) * 100, resetsAt: "2026-09-06T17:59:59Z", resetInSec: 777 },
    ],
    ...extra,
  };
}

function fetchStub(script: Array<{ url: RegExp; body: unknown }>) {
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    const hit = script.find((s) => s.url.test(url));
    return new Response(JSON.stringify(hit ? hit.body : { ok: false, unsupported: true }), { status: 200 });
  }) as unknown as typeof fetch;
  return { impl, calls };
}

beforeEach(() => {
  window.localStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
});

test("reads at session open and model change, refreshes after a turn", async () => {
  const { impl, calls } = fetchStub([{ url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } }]);
  const { result, rerender } = renderHook(
    (p: { sessionId: string; llmModel: string; turnEpoch: number }) =>
      useProviderUsage({ ...p, fetchImpl: impl }),
    { initialProps: { sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  expect(calls).toEqual(["/coddy/providers/neuraldeep/usage"]);
  rerender({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: 1 });
  await waitFor(() => expect(calls.length).toBe(2));
  expect(calls[1]).toBe("/coddy/providers/neuraldeep/usage?refresh=1");
  rerender({ sessionId: "s1", llmModel: "stub/model", turnEpoch: 1 });
  await waitFor(() => expect(result.current.usage?.provider === "neuraldeep" || result.current.usage === null).toBe(true));
  // The stub provider answers unsupported once and is not asked again.
  await waitFor(() => expect(calls.length).toBe(3));
  rerender({ sessionId: "s2", llmModel: "stub/model", turnEpoch: 1 });
  await new Promise((r) => setTimeout(r, 30));
  expect(calls.length).toBe(3);
});

test("a pushed snapshot for the active provider replaces the state, a foreign one is ignored", async () => {
  const { impl } = fetchStub([{ url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } }]);
  const { result } = renderHook(() =>
    useProviderUsage({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: 0, fetchImpl: impl }),
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  act(() => result.current.applyPushed(snapshot(9000)));
  expect(result.current.usage?.windows?.[0].used).toBe(9000);
  act(() => result.current.applyPushed({ ...snapshot(1), provider: "nd-work" }));
  expect(result.current.usage?.windows?.[0].used).toBe(9000);
});

test("a deferred refresh is followed by one cache read, a reset by a hub read", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const deferred = snapshot(407, { refreshPending: true, refreshInSec: 9 });
  for (const w of deferred.windows!) w.resetInSec = 0;
  const { impl, calls } = fetchStub([
    { url: /refresh=1/, body: { ok: true, usage: deferred } },
    { url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } },
  ]);
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: p.turnEpoch, fetchImpl: impl }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  rerender({ turnEpoch: 1 });
  await waitFor(() => expect(result.current.usage?.refreshPending).toBe(true));
  // 9 s + grace: a cache read, not a refresh.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(11_100);
  });
  await waitFor(() => expect(calls.length).toBe(3));
  expect(calls[2]).toBe("/coddy/providers/neuraldeep/usage");
  // The fresh snapshot carries a reset in 777 s: a hub read is armed for it.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(777_000 + 2_100);
  });
  await waitFor(() => expect(calls.length).toBe(4));
  expect(calls[3]).toBe("/coddy/providers/neuraldeep/usage?refresh=1");
});

test("the banner dismissal is remembered per period", async () => {
  const { impl } = fetchStub([{ url: /neuraldeep/, body: { ok: true, usage: snapshot(13000) } }]);
  const { result } = renderHook(() =>
    useProviderUsage({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: 0, fetchImpl: impl }),
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  expect(result.current.dismissedKey).toBe("");
  act(() => result.current.dismissBanner("session@2026-09-06T17:59:59Z"));
  expect(result.current.dismissedKey).toBe("session@2026-09-06T17:59:59Z");
  expect(window.localStorage.getItem("coddy_usage_banner_dismissed")).toBe("session@2026-09-06T17:59:59Z");
});
