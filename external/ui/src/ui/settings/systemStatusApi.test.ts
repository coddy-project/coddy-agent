import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import {
  SYSTEM_STATUS_REUSE_MS,
  openSystemStatus,
  readSystemStatus,
  resetSystemStatusForTests,
  snapshotSystemStatus,
  useSystemStatus,
  type SystemStatusDocument,
} from "./systemStatusApi";
import { connectRemote, setEnv } from "../env/remoteEnv";
import { fakeStatus } from "./systemStatus.fakes";

// The store behind the badge, the System tab and the read after Save all. Every
// read here is a deferred fetch, so a test decides in which order the answers
// come back.

type Pending = {
  path: string;
  init: RequestInit | undefined;
  answer: (res: Response) => void;
  fail: (error: Error) => void;
};

/** A fetch that answers nothing until a test says so, one entry per request. */
function deferredFetch(): Pending[] {
  const pending: Pending[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (path: string, init?: RequestInit) =>
        new Promise<Response>((answer, fail) => {
          pending.push({ path: String(path), init, answer, fail });
        }),
    ),
  );
  return pending;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

/** A document that says how many things need attention. */
const needing = (count: number): SystemStatusDocument =>
  fakeStatus({
    attention: Array.from({ length: count }, () => ({ code: "disk_low" })),
  });

/** The number the badge shows: the length of the document's own list. */
const badge = () => snapshotSystemStatus().doc?.attention?.length ?? 0;

const settle = () =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-15T10:00:00Z"));
  localStorage.clear();
  setEnv({ mode: "local" });
  resetSystemStatusForTests();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
  localStorage.clear();
});

/** Two remotes to switch between in place; the page itself is not reloaded. */
function withLocationStub() {
  const realLocation = window.location;
  Object.defineProperty(window, "location", {
    value: { hash: "", reload: vi.fn() },
    writable: true,
    configurable: true,
  });
  return () =>
    Object.defineProperty(window, "location", {
      value: realLocation,
      writable: true,
      configurable: true,
    });
}

describe("the status store orders its reads", () => {
  test("asks GET /coddy/status and keeps what the server answers", async () => {
    const pending = deferredFetch();
    const read = readSystemStatus();
    expect(pending.map((p) => p.path)).toEqual(["/coddy/status"]);
    pending[0]!.answer(json(needing(2)));
    await expect(read).resolves.toMatchObject({ object: "coddy.status" });
    expect(badge()).toBe(2);
    expect(snapshotSystemStatus().issue).toBeNull();
    expect(snapshotSystemStatus().at).toBe(Date.now());
  });

  test("two reads that complete in reverse order: the older answer is dropped", async () => {
    const pending = deferredFetch();
    const first = readSystemStatus();
    const second = readSystemStatus({ fresh: true });
    expect(pending).toHaveLength(2);
    pending[1]!.answer(json(needing(3)));
    await second;
    expect(badge()).toBe(3);
    pending[0]!.answer(json(needing(1)));
    await first;
    expect(badge()).toBe(3);
  });

  // The order R8-2 named: an old success must not restore a badge a newer
  // failure cleared, so a failure advances the applied sequence too.
  test("R1 started, a fresh R2 started and failed, then R1 succeeds: the badge stays cleared", async () => {
    const pending = deferredFetch();
    const opening = readSystemStatus();
    pending[0]!.answer(json(needing(2)));
    await opening;
    expect(badge()).toBe(2);

    const r1 = readSystemStatus({ fresh: true });
    const r2 = readSystemStatus({ fresh: true });
    expect(pending).toHaveLength(3);
    pending[2]!.fail(new TypeError("Failed to fetch"));
    await r2;
    expect(badge()).toBe(0);
    expect(snapshotSystemStatus().issue).toEqual({ kind: "failed" });

    pending[1]!.answer(json(needing(2)));
    await r1;
    expect(badge()).toBe(0);
    expect(snapshotSystemStatus().doc).toBeNull();
    expect(snapshotSystemStatus().issue).toEqual({ kind: "failed" });
  });

  test("a switch of environment with a read in flight: its answer never reaches the new one", async () => {
    const restore = withLocationStub();
    try {
      setEnv({ mode: "remote", baseUrl: "http://a:1", token: "t" });
      const pending = deferredFetch();
      const old = readSystemStatus();
      connectRemote("http://b:1", "t", "b");
      // The new environment starts empty, not with the old one's document.
      expect(snapshotSystemStatus().doc).toBeNull();
      pending[0]!.answer(json(needing(2)));
      await expect(old).resolves.toBeNull();
      expect(snapshotSystemStatus().doc).toBeNull();
      expect(badge()).toBe(0);

      // The new environment is read on its own, and not as the old read.
      const next = readSystemStatus();
      expect(pending).toHaveLength(2);
      pending[1]!.answer(json(needing(1)));
      await next;
      expect(badge()).toBe(1);
    } finally {
      restore();
    }
  });

  test("choosing the same remote again starts from nothing too", async () => {
    const restore = withLocationStub();
    try {
      setEnv({ mode: "remote", baseUrl: "http://a:1", token: "t" });
      const pending = deferredFetch();
      const first = readSystemStatus();
      pending[0]!.answer(json(needing(2)));
      await first;
      expect(badge()).toBe(2);
      connectRemote("http://a:1", "t", "a");
      expect(badge()).toBe(0);
    } finally {
      restore();
    }
  });

  test("a 404, a 401 and a failed read clear the document, each with its own reason", async () => {
    const pending = deferredFetch();
    const outcomes: [Response | Error, unknown][] = [
      [json({}, 404), { kind: "notFound" }],
      [json({}, 401), { kind: "unauthorized" }],
      [new Error("down"), { kind: "failed" }],
      [json({}, 503), { kind: "failed" }],
    ];
    for (const [reply, issue] of outcomes) {
      const good = readSystemStatus({ fresh: true });
      pending[pending.length - 1]!.answer(json(needing(2)));
      await good;
      expect(badge()).toBe(2);
      const bad = readSystemStatus({ fresh: true });
      const last = pending[pending.length - 1]!;
      if (reply instanceof Error) {
        last.fail(reply);
      } else {
        last.answer(reply);
      }
      await bad;
      expect(badge()).toBe(0);
      expect(snapshotSystemStatus().issue).toEqual(issue);
    }
  });

  test("a node a relay cannot reach is offline, with when it was last seen", async () => {
    const pending = deferredFetch();
    const read = readSystemStatus();
    pending[0]!.answer(
      json(
        {
          error: {
            message:
              'swarm node "worker-a": node is registered but not reachable',
            node: "worker-a",
            reason: "node is registered but not reachable",
            last_seen: "2026-10-15T09:41:00Z",
          },
        },
        502,
      ),
    );
    await read;
    expect(snapshotSystemStatus().issue).toEqual({
      kind: "offline",
      node: "worker-a",
      lastSeen: "2026-10-15T09:41:00Z",
    });
    expect(badge()).toBe(0);
  });

  test("a failed refresh keeps the last values for the tab to grey, never for the badge", async () => {
    const pending = deferredFetch();
    const first = readSystemStatus();
    pending[0]!.answer(json(needing(2)));
    await first;
    const at = snapshotSystemStatus().at;
    vi.setSystemTime(Date.now() + 20_000);
    const second = readSystemStatus({ fresh: true });
    pending[1]!.fail(new Error("down"));
    await second;
    const state = snapshotSystemStatus();
    expect(state.doc).toBeNull();
    expect(state.stale?.doc.attention).toHaveLength(2);
    expect(state.stale?.at).toBe(at);
    // A 404 says the server does not report: nothing of it is kept.
    const third = readSystemStatus({ fresh: true });
    pending[2]!.answer(json({}, 404));
    await third;
    expect(snapshotSystemStatus().stale).toBeNull();
  });
});

describe("a read in flight is shared", () => {
  test("the opening read and the tab's first read are one request; Save all reads anew", async () => {
    const pending = deferredFetch();
    const opening = readSystemStatus();
    const tab = openSystemStatus();
    expect(pending).toHaveLength(1);
    const again = readSystemStatus();
    expect(pending).toHaveLength(1);
    const saved = readSystemStatus({ fresh: true });
    expect(pending).toHaveLength(2);
    // The newest read is the one a later reader joins.
    void openSystemStatus();
    expect(pending).toHaveLength(2);
    pending[0]!.answer(json(needing(1)));
    pending[1]!.answer(json(needing(2)));
    await Promise.all([opening, tab, again, saved]);
    expect(badge()).toBe(2);
  });

  test("a read that has finished is no longer shared", async () => {
    const pending = deferredFetch();
    const first = readSystemStatus();
    pending[0]!.answer(json(needing(1)));
    await first;
    void readSystemStatus();
    expect(pending).toHaveLength(2);
  });
});

describe("the tab reuses the opening read for a few seconds", () => {
  test("entered 3 s after Settings opened, the tab serves that document", async () => {
    const pending = deferredFetch();
    const opening = readSystemStatus();
    pending[0]!.answer(json(needing(2)));
    await opening;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    const tab = openSystemStatus();
    expect(pending).toHaveLength(1);
    await expect(tab).resolves.toMatchObject({ object: "coddy.status" });
  });

  test("entered 60 s after, the tab reads at once", async () => {
    const pending = deferredFetch();
    const opening = readSystemStatus();
    pending[0]!.answer(json(needing(2)));
    await opening;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    void openSystemStatus();
    expect(pending).toHaveLength(2);
  });

  test("the window is the constant's, to the millisecond", async () => {
    expect(SYSTEM_STATUS_REUSE_MS).toBe(5_000);
    const pending = deferredFetch();
    const opening = readSystemStatus();
    pending[0]!.answer(json(needing(2)));
    await opening;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SYSTEM_STATUS_REUSE_MS - 1);
    });
    void openSystemStatus();
    expect(pending).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    void openSystemStatus();
    expect(pending).toHaveLength(2);
  });

  test("an environment with nothing read yet is read at once", async () => {
    const pending = deferredFetch();
    void openSystemStatus();
    await settle();
    expect(pending).toHaveLength(1);
  });
});

describe("useSystemStatus", () => {
  test("a component selecting a number is drawn again only when the number changes", async () => {
    const pending = deferredFetch();
    let renders = 0;
    const view = renderHook(() => {
      renders++;
      return useSystemStatus((s) => s.doc?.attention?.length ?? 0);
    });
    expect(view.result.current).toBe(0);
    const base = renders;

    const first = readSystemStatus({ fresh: true });
    pending[0]!.answer(json(needing(2)));
    await act(async () => {
      await first;
    });
    expect(view.result.current).toBe(2);
    const afterFirst = renders;
    expect(afterFirst).toBeGreaterThan(base);

    // A poll that brings the same number draws nothing.
    const second = readSystemStatus({ fresh: true });
    pending[1]!.answer(json(needing(2)));
    await act(async () => {
      await second;
    });
    expect(renders).toBe(afterFirst);

    const third = readSystemStatus({ fresh: true });
    pending[2]!.answer(json({}, 404));
    await act(async () => {
      await third;
    });
    expect(view.result.current).toBe(0);
  });
});
