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
  // The stub provider answers unsupported once and is not asked again.
  await waitFor(() => expect(calls.length).toBe(3));
  expect(calls[2]).toBe("/coddy/providers/stub/usage");
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
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  act(() => result.current.applyPushed({ ...snapshot(1), provider: "nd-work" }));
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
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
  act(() => result.current.dismissBanner("neuraldeep@session@2026-09-06T17:59:59Z"));
  expect(result.current.dismissedKey).toBe("neuraldeep@session@2026-09-06T17:59:59Z");
  expect(window.localStorage.getItem("coddy_usage_banner_dismissed")).toBe("neuraldeep@session@2026-09-06T17:59:59Z");
});

test("an older REST answer never replaces a newer pushed snapshot, nor does an older push", async () => {
  let release: () => void = () => {};
  const slow = new Promise<void>((r) => {
    release = r;
  });
  const impl = vi.fn(async (url: string) => {
    if (/refresh=1/.test(url)) {
      await slow;
      return new Response(JSON.stringify({ ok: true, usage: snapshot(500, { fetchedAt: "2026-09-06T17:47:00Z" }) }), { status: 200 });
    }
    return new Response(JSON.stringify({ ok: true, usage: snapshot(407, { fetchedAt: "2026-09-06T17:47:00Z" }) }), { status: 200 });
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: p.turnEpoch, fetchImpl: impl }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.windows?.[0]?.used).toBe(407));
  // The turn-end refresh is slow; the server's push for the same turn lands first.
  rerender({ turnEpoch: 1 });
  act(() => result.current.applyPushed(snapshot(9000, { fetchedAt: "2026-09-06T17:47:30Z" })));
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  await act(async () => {
    release();
    await slow;
    await new Promise((r) => setTimeout(r, 10));
  });
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  act(() => result.current.applyPushed(snapshot(1, { fetchedAt: "2026-09-06T17:47:10Z" })));
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  // A deferred answer carries the read time of the snapshot it repeats: it still applies (with its schedule).
  act(() => result.current.applyPushed(snapshot(9000, { fetchedAt: "2026-09-06T17:47:30Z", refreshPending: true, refreshInSec: 9 })));
  expect(result.current.usage?.refreshPending).toBe(true);
});

test("only the latest read issued applies, whatever order the answers arrive in", async () => {
  const gates: Array<() => void> = [];
  const answers = [
    snapshot(407, { fetchedAt: "2026-09-06T17:47:00Z" }),
    snapshot(555, { fetchedAt: "2026-09-06T17:47:05Z" }),
  ];
  const impl = vi.fn(async () => {
    const usage = answers.shift();
    await new Promise<void>((r) => gates.push(r));
    return new Response(JSON.stringify({ ok: true, usage }), { status: 200 });
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({ sessionId: "s1", llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: p.turnEpoch, fetchImpl: impl }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(gates.length).toBe(1));
  rerender({ turnEpoch: 1 });
  await waitFor(() => expect(gates.length).toBe(2));
  // The later read answers first, then the earlier one: the state keeps the later numbers.
  await act(async () => {
    gates[1]?.();
    await new Promise((r) => setTimeout(r, 10));
  });
  expect(result.current.usage?.windows?.[0]?.used).toBe(555);
  await act(async () => {
    gates[0]?.();
    await new Promise((r) => setTimeout(r, 10));
  });
  expect(result.current.usage?.windows?.[0]?.used).toBe(555);
});

test("an unsupported row is asked again after five minutes", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const { impl, calls } = fetchStub([]);
  const { rerender } = renderHook(
    (p: { sessionId: string }) =>
      useProviderUsage({ sessionId: p.sessionId, llmModel: "stub/model", turnEpoch: 0, fetchImpl: impl }),
    { initialProps: { sessionId: "s1" } },
  );
  await waitFor(() => expect(calls.length).toBe(1));
  rerender({ sessionId: "s2" });
  await new Promise((r) => setTimeout(r, 20));
  expect(calls.length).toBe(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5 * 60_000 + 10);
  });
  rerender({ sessionId: "s3" });
  await waitFor(() => expect(calls.length).toBe(2));
});
