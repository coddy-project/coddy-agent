import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { useProviderUsage } from "./useProviderUsage";
import type { ProviderUsage } from "./providerUsage";

function snapshot(
  used: number,
  extra: Partial<ProviderUsage> = {},
): ProviderUsage {
  return {
    provider: "neuraldeep",
    providerType: "neuraldeep",
    plan: "pro",
    windows: [
      {
        id: "session",
        label: "3h",
        used,
        limit: 15000,
        usedPercent: (used / 15000) * 100,
        resetsAt: "2026-09-06T17:59:59Z",
        resetInSec: 777,
      },
    ],
    ...extra,
  };
}

function fetchStub(script: Array<{ url: RegExp; body: unknown }>) {
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    const hit = script.find((s) => s.url.test(url));
    return new Response(
      JSON.stringify(hit ? hit.body : { ok: false, unsupported: true }),
      { status: 200 },
    );
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
  const { impl, calls } = fetchStub([
    { url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } },
  ]);
  const { result, rerender } = renderHook(
    (p: { sessionId: string; llmModel: string; turnEpoch: number }) =>
      useProviderUsage({ ...p, fetchImpl: impl }),
    {
      initialProps: {
        sessionId: "s1",
        llmModel: "neuraldeep/qwen3.8-27b",
        turnEpoch: 0,
      },
    },
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  expect(calls).toEqual([
    "/coddy/providers/neuraldeep/usage?model=qwen3.8-27b",
  ]);
  rerender({
    sessionId: "s1",
    llmModel: "neuraldeep/qwen3.8-27b",
    turnEpoch: 1,
  });
  await waitFor(() => expect(calls.length).toBe(2));
  expect(calls[1]).toBe(
    "/coddy/providers/neuraldeep/usage?model=qwen3.8-27b&refresh=1",
  );
  rerender({ sessionId: "s1", llmModel: "stub/model", turnEpoch: 1 });
  // The stub provider answers unsupported once and is not asked again.
  await waitFor(() => expect(calls.length).toBe(3));
  expect(calls[2]).toBe("/coddy/providers/stub/usage?model=model");
  rerender({ sessionId: "s2", llmModel: "stub/model", turnEpoch: 1 });
  await new Promise((r) => setTimeout(r, 30));
  expect(calls.length).toBe(3);
});

test("a pushed snapshot for the active provider replaces the state, a foreign one is ignored", async () => {
  const { impl } = fetchStub([
    { url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } },
  ]);
  const { result } = renderHook(() =>
    useProviderUsage({
      sessionId: "s1",
      llmModel: "neuraldeep/qwen3.8-27b",
      turnEpoch: 0,
      fetchImpl: impl,
    }),
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  act(() => result.current.applyPushed(snapshot(9000)));
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  act(() =>
    result.current.applyPushed({ ...snapshot(1), provider: "nd-work" }),
  );
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
      useProviderUsage({
        sessionId: "s1",
        llmModel: "neuraldeep/qwen3.8-27b",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
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
  expect(calls[2]).toBe("/coddy/providers/neuraldeep/usage?model=qwen3.8-27b");
  // The fresh snapshot carries a reset in 777 s: a hub read is armed for it.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(777_000 + 2_100);
  });
  await waitFor(() => expect(calls.length).toBe(4));
  expect(calls[3]).toBe(
    "/coddy/providers/neuraldeep/usage?model=qwen3.8-27b&refresh=1",
  );
});

test("the banner dismissal is remembered per period", async () => {
  const { impl } = fetchStub([
    { url: /neuraldeep/, body: { ok: true, usage: snapshot(13000) } },
  ]);
  const { result } = renderHook(() =>
    useProviderUsage({
      sessionId: "s1",
      llmModel: "neuraldeep/qwen3.8-27b",
      turnEpoch: 0,
      fetchImpl: impl,
    }),
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  expect(result.current.dismissedKey).toBe("");
  act(() =>
    result.current.dismissBanner("neuraldeep@session@2026-09-06T17:59:59Z"),
  );
  expect(result.current.dismissedKey).toBe(
    "neuraldeep@session@2026-09-06T17:59:59Z",
  );
  expect(window.localStorage.getItem("coddy_usage_banner_dismissed")).toBe(
    "neuraldeep@session@2026-09-06T17:59:59Z",
  );
});

test("an older REST answer never replaces a newer pushed snapshot, nor does an older push", async () => {
  let release: () => void = () => {};
  const slow = new Promise<void>((r) => {
    release = r;
  });
  const impl = vi.fn(async (url: string) => {
    if (/refresh=1/.test(url)) {
      await slow;
      return new Response(
        JSON.stringify({
          ok: true,
          usage: snapshot(500, { fetchedAt: "2026-09-06T17:47:00Z" }),
        }),
        { status: 200 },
      );
    }
    return new Response(
      JSON.stringify({
        ok: true,
        usage: snapshot(407, { fetchedAt: "2026-09-06T17:47:00Z" }),
      }),
      { status: 200 },
    );
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: "neuraldeep/qwen3.8-27b",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() =>
    expect(result.current.usage?.windows?.[0]?.used).toBe(407),
  );
  // The turn-end refresh is slow; the server's push for the same turn lands first.
  rerender({ turnEpoch: 1 });
  act(() =>
    result.current.applyPushed(
      snapshot(9000, { fetchedAt: "2026-09-06T17:47:30Z" }),
    ),
  );
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  await act(async () => {
    release();
    await slow;
    await new Promise((r) => setTimeout(r, 10));
  });
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  act(() =>
    result.current.applyPushed(
      snapshot(1, { fetchedAt: "2026-09-06T17:47:10Z" }),
    ),
  );
  expect(result.current.usage?.windows?.[0]?.used).toBe(9000);
  // A deferred answer carries the read time of the snapshot it repeats: it still applies (with its schedule).
  act(() =>
    result.current.applyPushed(
      snapshot(9000, {
        fetchedAt: "2026-09-06T17:47:30Z",
        refreshPending: true,
        refreshInSec: 9,
      }),
    ),
  );
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
      useProviderUsage({
        sessionId: "s1",
        llmModel: "neuraldeep/qwen3.8-27b",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
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
      useProviderUsage({
        sessionId: p.sessionId,
        llmModel: "stub/model",
        turnEpoch: 0,
        fetchImpl: impl,
      }),
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

test("an unsupported answer for the shown provider clears the snapshot", async () => {
  let panelOff = false;
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    const body = panelOff
      ? {
          ok: false,
          unsupported: true,
          disabled: true,
          provider: "neuraldeep",
          providerType: "neuraldeep",
        }
      : { ok: true, usage: snapshot(407) };
    return new Response(JSON.stringify(body), { status: 200 });
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: "neuraldeep/qwen3.8-27b",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  // The operator switched the row's usage limits panel off; the refresh
  // after the next turn says so and the section must not keep stale numbers.
  panelOff = true;
  rerender({ turnEpoch: 1 });
  await waitFor(() => expect(result.current.usage).toBeNull());
  expect(calls.length).toBe(2);
});

// --- a coddy row: the remote account's usage, read per alias ----------------

function remoteSnapshot(
  model: string,
  percent: number,
  extra: Partial<ProviderUsage> = {},
): ProviderUsage {
  return {
    provider: "lab",
    providerType: "coddy",
    model,
    fetchedAt: "2026-09-06T17:47:10Z",
    windows: [
      {
        id: "session",
        label: "5h",
        usedPercent: percent,
        resetsAt: "2026-09-06T19:00:00Z",
        resetInSec: 4400,
      },
    ],
    ...extra,
  };
}

/** The alias a request asked about. */
function modelOf(url: string): string {
  return new URL(url, "http://stand").searchParams.get("model") ?? "";
}

test("a coddy row is read per alias: a late answer of the old alias never lands on the new one", async () => {
  const gates = new Map<string, () => void>();
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    const model = modelOf(url);
    if (model === "terra") {
      await new Promise<void>((r) => gates.set(model, r));
    }
    return new Response(
      JSON.stringify({
        ok: true,
        usage: remoteSnapshot(model, model === "terra" ? 10 : 70),
      }),
      { status: 200 },
    );
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { llmModel: string }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: p.llmModel,
        turnEpoch: 0,
        fetchImpl: impl,
      }),
    { initialProps: { llmModel: "lab/terra" } },
  );
  await waitFor(() => expect(gates.has("terra")).toBe(true));
  expect(calls).toEqual(["/coddy/providers/lab/usage?model=terra"]);
  // Nothing has said the row is coddy yet, so the switch is a switch of alias
  // of a row not yet known: the answer below teaches it.
  rerender({ llmModel: "lab/coder" });
  await waitFor(() =>
    expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(70),
  );
  expect(result.current.usage?.model).toBe("coder");
  await act(async () => {
    gates.get("terra")?.();
    await new Promise((r) => setTimeout(r, 10));
  });
  // The row was told by the first answer to be coddy: terra's late numbers
  // are about another subject and stay out.
  expect(result.current.usage?.model).toBe("coder");
  expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(70);
  expect(calls).toEqual([
    "/coddy/providers/lab/usage?model=terra",
    "/coddy/providers/lab/usage?model=coder",
  ]);
});

test("once the row is known as coddy, a switch of alias reads again and the old alias's numbers go", async () => {
  const { impl, calls } = fetchStub([
    {
      url: /model=terra/,
      body: { ok: true, usage: remoteSnapshot("terra", 10) },
    },
    {
      url: /model=coder/,
      body: { ok: true, usage: remoteSnapshot("coder", 70) },
    },
  ]);
  const { result, rerender } = renderHook(
    (p: { llmModel: string }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: p.llmModel,
        turnEpoch: 0,
        fetchImpl: impl,
      }),
    { initialProps: { llmModel: "lab/terra" } },
  );
  await waitFor(() =>
    expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(10),
  );
  rerender({ llmModel: "lab/coder" });
  await waitFor(() => expect(calls.length).toBe(2));
  expect(calls[1]).toBe("/coddy/providers/lab/usage?model=coder");
  await waitFor(() =>
    expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(70),
  );
  // And back: terra's numbers are asked for again, coder's are not shown for it.
  rerender({ llmModel: "lab/terra" });
  await waitFor(() => expect(calls.length).toBe(3));
  expect(calls[2]).toBe("/coddy/providers/lab/usage?model=terra");
});

test("a pushed snapshot of another alias of the row is ignored, the viewed alias's replaces the state", async () => {
  const { impl } = fetchStub([
    {
      url: /model=terra/,
      body: { ok: true, usage: remoteSnapshot("terra", 10) },
    },
  ]);
  const { result } = renderHook(() =>
    useProviderUsage({
      sessionId: "s1",
      llmModel: "lab/terra",
      turnEpoch: 0,
      fetchImpl: impl,
    }),
  );
  await waitFor(() =>
    expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(10),
  );
  act(() =>
    result.current.applyPushed(
      remoteSnapshot("coder", 99, { fetchedAt: "2026-09-06T17:48:00Z" }),
    ),
  );
  expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(10);
  act(() =>
    result.current.applyPushed(
      remoteSnapshot("terra", 33, { fetchedAt: "2026-09-06T17:48:00Z" }),
    ),
  );
  expect(result.current.usage?.windows?.[0]?.usedPercent).toBe(33);
});

test("another provider type keeps its request counts: a switch of model inside the row reads nothing", async () => {
  const { impl, calls } = fetchStub([
    { url: /neuraldeep/, body: { ok: true, usage: snapshot(407) } },
  ]);
  const { result, rerender } = renderHook(
    (p: { llmModel: string; turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: p.llmModel,
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    {
      initialProps: { llmModel: "neuraldeep/qwen3.8-27b", turnEpoch: 0 },
    },
  );
  await waitFor(() => expect(result.current.usage?.plan).toBe("pro"));
  rerender({ llmModel: "neuraldeep/kimi-k2.6", turnEpoch: 0 });
  await new Promise((r) => setTimeout(r, 30));
  expect(calls).toEqual([
    "/coddy/providers/neuraldeep/usage?model=qwen3.8-27b",
  ]);
  // The turn-end refresh asks about the model now selected.
  rerender({ llmModel: "neuraldeep/kimi-k2.6", turnEpoch: 1 });
  await waitFor(() => expect(calls.length).toBe(2));
  expect(calls[1]).toBe(
    "/coddy/providers/neuraldeep/usage?model=kimi-k2.6&refresh=1",
  );
});

test("no unsupported mark is kept for a coddy row: its own memory on the server is the only one", async () => {
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    return new Response(
      JSON.stringify({
        ok: false,
        unsupported: true,
        provider: "lab",
        providerType: "coddy",
        model: "terra",
      }),
      { status: 200 },
    );
  }) as unknown as typeof fetch;
  const { rerender } = renderHook(
    (p: { sessionId: string }) =>
      useProviderUsage({
        sessionId: p.sessionId,
        llmModel: "lab/terra",
        turnEpoch: 0,
        fetchImpl: impl,
      }),
    { initialProps: { sessionId: "s1" } },
  );
  await waitFor(() => expect(calls.length).toBe(1));
  rerender({ sessionId: "s2" });
  await waitFor(() => expect(calls.length).toBe(2));
  rerender({ sessionId: "s3" });
  await waitFor(() => expect(calls.length).toBe(3));
});

test("an unsupported answer for a coddy alias clears that alias's snapshot", async () => {
  let supported = true;
  const impl = vi.fn(async () => {
    const body = supported
      ? { ok: true, usage: remoteSnapshot("terra", 10) }
      : {
          ok: false,
          unsupported: true,
          provider: "lab",
          providerType: "coddy",
          model: "terra",
        };
    return new Response(JSON.stringify(body), { status: 200 });
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: "lab/terra",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.model).toBe("terra"));
  supported = false;
  rerender({ turnEpoch: 1 });
  await waitFor(() => expect(result.current.usage).toBeNull());
});

// --- the countdown of a wait for a free slot of the remote ------------------

const T0 = "2026-09-06T17:47:10Z";
const T1 = "2026-09-06T17:47:20Z";
const T2 = "2026-09-06T17:47:30Z";

function countdown(
  stamp: string,
  extra: Partial<ProviderUsage> = {},
): ProviderUsage {
  return {
    provider: "lab",
    providerType: "coddy",
    model: "terra",
    fetchedAt: stamp,
    blocked: true,
    blockers: ["remote_busy"],
    retryAt: "2026-09-06T17:47:42Z",
    retryInSec: 30,
    resuming: true,
    ...extra,
  };
}

function busyEnd(stamp: string): ProviderUsage {
  return {
    provider: "lab",
    providerType: "coddy",
    model: "terra",
    fetchedAt: stamp,
    blockers: ["remote_busy"],
  };
}

/** A row that has a real snapshot of its alias, the countdown beside it. */
function busyHook(llmModel = "lab/terra") {
  const { impl, calls } = fetchStub([
    {
      url: /model=terra/,
      body: { ok: true, usage: remoteSnapshot("terra", 62) },
    },
    {
      url: /model=coder/,
      body: { ok: true, usage: remoteSnapshot("coder", 11) },
    },
  ]);
  const hook = renderHook(
    (p: { llmModel: string; turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: p.llmModel,
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    { initialProps: { llmModel, turnEpoch: 0 } },
  );
  return { ...hook, calls };
}

async function withSnapshot(hook: ReturnType<typeof busyHook>) {
  await waitFor(() =>
    expect(hook.result.current.usage?.windows?.[0]?.usedPercent).toBe(62),
  );
}

test("the countdown is a state of its own beside the snapshot: a snapshot during the wait leaves it, the end leaves the snapshot", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0)));
  expect(hook.result.current.busy?.blockers).toEqual(["remote_busy"]);
  // The snapshot is what it was: the wait did not replace it.
  expect(hook.result.current.usage?.windows?.[0]?.usedPercent).toBe(62);
  // A fresher snapshot lands during the wait: the countdown stays.
  act(() =>
    hook.result.current.applyPushed(
      remoteSnapshot("terra", 64, { fetchedAt: T1 }),
    ),
  );
  expect(hook.result.current.usage?.windows?.[0]?.usedPercent).toBe(64);
  expect(hook.result.current.busy).not.toBeNull();
  // The end takes the countdown down and leaves the snapshot alone.
  act(() => hook.result.current.applyPushed(busyEnd(T2)));
  expect(hook.result.current.busy).toBeNull();
  expect(hook.result.current.usage?.windows?.[0]?.usedPercent).toBe(64);
  expect(hook.calls.length).toBe(1);
});

test("a countdown with no snapshot at all shows by itself, and nothing is read when its budget runs out", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls: string[] = [];
  const impl = vi.fn(async (url: string) => {
    calls.push(url);
    return new Response(
      JSON.stringify({
        ok: false,
        unsupported: true,
        provider: "lab",
        providerType: "coddy",
        model: "terra",
      }),
      { status: 200 },
    );
  }) as unknown as typeof fetch;
  const { result } = renderHook(() =>
    useProviderUsage({
      sessionId: "s1",
      llmModel: "lab/terra",
      turnEpoch: 0,
      fetchImpl: impl,
    }),
  );
  await waitFor(() => expect(calls.length).toBe(1));
  act(() => result.current.applyPushed(countdown(T0)));
  expect(result.current.usage).toBeNull();
  expect(result.current.busy?.retryInSec).toBe(30);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20_000);
  });
  expect(calls.length).toBe(1);
});

test("an end older than the countdown held is ignored, a tied end wins", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T1)));
  act(() => hook.result.current.applyPushed(busyEnd(T0)));
  expect(hook.result.current.busy).not.toBeNull();
  act(() => hook.result.current.applyPushed(busyEnd(T1)));
  expect(hook.result.current.busy).toBeNull();
});

test("a countdown replayed after its end stays ignored, a later one is a new wait", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0)));
  act(() => hook.result.current.applyPushed(busyEnd(T1)));
  expect(hook.result.current.busy).toBeNull();
  // A relay replay, a mirror or a reconnect re-delivers the old countdown and
  // even one stamped at the end's own second.
  act(() => hook.result.current.applyPushed(countdown(T0)));
  expect(hook.result.current.busy).toBeNull();
  act(() => hook.result.current.applyPushed(countdown(T1)));
  expect(hook.result.current.busy).toBeNull();
  act(() => hook.result.current.applyPushed(countdown(T2)));
  expect(hook.result.current.busy?.fetchedAt).toBe(T2);
});

test("an end that comes before any countdown still stamps the tombstone", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(busyEnd(T1)));
  act(() => hook.result.current.applyPushed(countdown(T0)));
  expect(hook.result.current.busy).toBeNull();
});

test("a countdown older than the one held does not replace it, a renewal does", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T1, { retryInSec: 20 })));
  act(() => hook.result.current.applyPushed(countdown(T0, { retryInSec: 99 })));
  expect(hook.result.current.busy?.retryInSec).toBe(20);
  act(() => hook.result.current.applyPushed(countdown(T2, { retryInSec: 10 })));
  expect(hook.result.current.busy?.retryInSec).toBe(10);
});

test("an Unsupported update clears the snapshot of the alias and leaves the countdown alone", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0)));
  act(() =>
    hook.result.current.applyPushed({
      provider: "lab",
      providerType: "coddy",
      model: "terra",
      unsupported: true,
    }),
  );
  expect(hook.result.current.usage).toBeNull();
  expect(hook.result.current.busy).not.toBeNull();
});

test("an Unsupported read answer leaves the countdown alone", async () => {
  let supported = true;
  const impl = vi.fn(async () => {
    const body = supported
      ? { ok: true, usage: remoteSnapshot("terra", 62) }
      : {
          ok: false,
          unsupported: true,
          provider: "lab",
          providerType: "coddy",
          model: "terra",
        };
    return new Response(JSON.stringify(body), { status: 200 });
  }) as unknown as typeof fetch;
  const { result, rerender } = renderHook(
    (p: { turnEpoch: number }) =>
      useProviderUsage({
        sessionId: "s1",
        llmModel: "lab/terra",
        turnEpoch: p.turnEpoch,
        fetchImpl: impl,
      }),
    { initialProps: { turnEpoch: 0 } },
  );
  await waitFor(() => expect(result.current.usage?.model).toBe("terra"));
  act(() => result.current.applyPushed(countdown(T0)));
  supported = false;
  // Not a turn end (that drops the countdown): a read of its own.
  await act(async () => {
    await result.current.refresh();
  });
  expect(result.current.usage).toBeNull();
  expect(result.current.busy).not.toBeNull();
  void rerender;
});

test("another alias's countdown never shows and its end never ends this one's", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0, { model: "coder" })));
  expect(hook.result.current.busy).toBeNull();
  act(() => hook.result.current.applyPushed(countdown(T0)));
  act(() =>
    hook.result.current.applyPushed({ ...busyEnd(T2), model: "coder" }),
  );
  expect(hook.result.current.busy).not.toBeNull();
  // Nor does a countdown of another row.
  act(() =>
    hook.result.current.applyPushed(countdown(T2, { provider: "elsewhere" })),
  );
  expect(hook.result.current.busy?.fetchedAt).toBe(T0);
});

test("a countdown of a remote that predates the alias shows by the row alone", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  const phase1 = countdown(T0);
  delete phase1.model;
  act(() => hook.result.current.applyPushed(phase1));
  expect(hook.result.current.busy).not.toBeNull();
});

test("the end of the viewed session's turn drops the countdown and raises the tombstone", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T1)));
  hook.rerender({ llmModel: "lab/terra", turnEpoch: 1 });
  await waitFor(() => expect(hook.result.current.busy).toBeNull());
  // The dropped countdown cannot come back by a replay.
  act(() => hook.result.current.applyPushed(countdown(T1)));
  expect(hook.result.current.busy).toBeNull();
  act(() => hook.result.current.applyPushed(countdown(T2)));
  expect(hook.result.current.busy).not.toBeNull();
});

test("the countdown expires RetryInSec plus two seconds after it was received, on the surface's own clock", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const hook = busyHook();
  await withSnapshot(hook);
  // The server's own deadline is long past: nothing here compares with it.
  act(() =>
    hook.result.current.applyPushed(
      countdown(T0, { retryAt: "2001-01-01T00:00:00Z", retryInSec: 30 }),
    ),
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(31_900);
  });
  expect(hook.result.current.busy).not.toBeNull();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(300);
  });
  expect(hook.result.current.busy).toBeNull();
  // The expiry raised the tombstone: the same countdown does not come back.
  act(() => hook.result.current.applyPushed(countdown(T0)));
  expect(hook.result.current.busy).toBeNull();
  // The snapshot beside it was never touched.
  expect(hook.result.current.usage?.windows?.[0]?.usedPercent).toBe(62);
});

test("a renewed countdown moves the expiry", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0, { retryInSec: 10 })));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(9_000);
  });
  act(() => hook.result.current.applyPushed(countdown(T1, { retryInSec: 10 })));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(9_000);
  });
  // 18 s after the first, 9 s after the renewal: both budgets of 12 s are not spent.
  expect(hook.result.current.busy).not.toBeNull();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(4_000);
  });
  expect(hook.result.current.busy).toBeNull();
});

test("a change of subject drops the countdown and the tombstone with it", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T1)));
  act(() => hook.result.current.applyPushed(busyEnd(T2)));
  act(() => hook.result.current.applyPushed(countdown(T0, { model: "terra" })));
  expect(hook.result.current.busy).toBeNull();
  hook.rerender({ llmModel: "lab/coder", turnEpoch: 0 });
  await waitFor(() => expect(hook.result.current.usage?.model).toBe("coder"));
  expect(hook.result.current.busy).toBeNull();
  // Back on the first alias a countdown of any stamp is a new wait: the
  // tombstone of the old subject is gone.
  hook.rerender({ llmModel: "lab/terra", turnEpoch: 0 });
  await waitFor(() => expect(hook.result.current.usage?.model).toBe("terra"));
  act(() => hook.result.current.applyPushed(countdown(T0)));
  expect(hook.result.current.busy).not.toBeNull();
});

test("a change of subject mid-wait takes the countdown down", async () => {
  const hook = busyHook();
  await withSnapshot(hook);
  act(() => hook.result.current.applyPushed(countdown(T0)));
  hook.rerender({ llmModel: "lab/coder", turnEpoch: 0 });
  await waitFor(() => expect(hook.result.current.busy).toBeNull());
});
