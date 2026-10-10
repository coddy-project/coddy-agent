import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  dismissStorageLow,
  getStorageSnapshot,
  noteStorageWriteFailing,
  refreshStorageStatus,
  resetStorageMonitorForTest,
  startStorageMonitor,
  storageStatusOfInfo,
} from "./storageStatus";
import { connectRemote, setEnv } from "./remoteEnv";

type Held = { resolve: (r: Response) => void };

let answers: Array<() => Response | Promise<Response>> = [];
let held: Held[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  expect(String(input)).toBe("/coddy/info");
  const next = answers.shift();
  if (!next) throw new Error("no answer prepared");
  return next();
});

function info(storage: unknown): Response {
  return new Response(
    JSON.stringify({ object: "coddy.info", version: "1.2.3", storage }),
    { status: 200 },
  );
}

const MB = 1024 * 1024;
const low = {
  state: "low",
  volume: "sessions",
  freeBytes: 300 * MB,
  totalBytes: 100 * 1024 * MB,
  minFreeBytes: 512 * MB,
};
const ok = { ...low, state: "ok", freeBytes: 4096 * MB };

function snapshot() {
  return getStorageSnapshot();
}

beforeEach(() => {
  answers = [];
  held = [];
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
  window.sessionStorage.clear();
  resetStorageMonitorForTest();
});

afterEach(() => {
  resetStorageMonitorForTest();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test("storageStatusOfInfo reads the storage object and nothing it does not understand", () => {
  expect(storageStatusOfInfo({ storage: low })).toEqual({
    state: "low",
    volume: "sessions",
    freeBytes: 300 * MB,
    totalBytes: 100 * 1024 * MB,
    minFreeBytes: 512 * MB,
  });
  expect(
    storageStatusOfInfo({ storage: { state: "full", minFreeBytes: 5 } }),
  ).toEqual({
    state: "full",
    volume: "",
    freeBytes: null,
    totalBytes: null,
    minFreeBytes: 5,
  });
  expect(
    storageStatusOfInfo({ storage: { ...low, volume: "home" } })?.volume,
  ).toBe("home");
  // Figures that are not counts are dropped, a threshold that is none is 0.
  const odd = storageStatusOfInfo({
    storage: { ...low, freeBytes: -1, totalBytes: "x", minFreeBytes: null },
  });
  expect(odd).toMatchObject({
    freeBytes: null,
    totalBytes: null,
    minFreeBytes: 0,
  });

  expect(storageStatusOfInfo({})).toBeNull();
  expect(storageStatusOfInfo(null)).toBeNull();
  expect(storageStatusOfInfo({ storage: "full" })).toBeNull();
  expect(storageStatusOfInfo({ storage: { state: "nearly" } })).toBeNull();
});

test("a read keeps the server's answer", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  expect(snapshot().status).toMatchObject({ state: "low", volume: "sessions" });

  answers.push(() => info(ok));
  await refreshStorageStatus();
  expect(snapshot().status?.state).toBe("ok");
});

test("a server that reports no storage leaves nothing to show", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  answers.push(
    () =>
      new Response(JSON.stringify({ object: "coddy.info" }), { status: 200 }),
  );
  await refreshStorageStatus();
  expect(snapshot().status).toBeNull();
});

test("a read that fails keeps what is held", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  answers.push(() => {
    throw new TypeError("Failed to fetch");
  });
  await refreshStorageStatus();
  expect(snapshot().status?.state).toBe("low");
  answers.push(() => new Response("{}", { status: 502 }));
  await refreshStorageStatus();
  expect(snapshot().status?.state).toBe("low");
});

// Two reads can be in flight (the timer, a focus, an event); the one asked
// last decides, not the one that answers last.
test("the newest read decides, not the one that answers last", async () => {
  const gates: Array<(r: Response) => void> = [];
  const gated = () => new Promise<Response>((resolve) => gates.push(resolve));
  answers.push(gated, gated);
  const first = refreshStorageStatus();
  const second = refreshStorageStatus();
  gates[1]!(info(ok));
  await second;
  gates[0]!(info(low));
  await first;
  expect(snapshot().status?.state).toBe("ok");
});

test("a failed save puts the full-disk state up at once and the server's answer corrects it", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();

  const second = new Promise<Response>((resolve) => held.push({ resolve }));
  answers.push(() => second);
  noteStorageWriteFailing(true);
  // Before the server has answered: full, with the figures already held.
  expect(snapshot().status).toMatchObject({
    state: "full",
    volume: "sessions",
    freeBytes: 300 * MB,
  });

  held[0]!.resolve(info({ ...ok }));
  await vi.waitFor(() => expect(snapshot().status?.state).toBe("ok"));
});

test("a failed save with nothing held yet is full without figures", async () => {
  answers.push(() => new Promise<Response>(() => {}));
  noteStorageWriteFailing(true);
  expect(snapshot().status).toEqual({
    state: "full",
    volume: "",
    freeBytes: null,
    totalBytes: null,
    minFreeBytes: 0,
  });
});

test("the end of a failure only reads the server again", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  answers.push(() => info(ok));
  noteStorageWriteFailing(false);
  await vi.waitFor(() => expect(snapshot().status?.state).toBe("ok"));
});

test("a low-space warning is dismissed for the tab, a full disk never is", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(false);

  dismissStorageLow();
  expect(snapshot().dismissedLow).toBe(true);
  expect(window.sessionStorage.getItem("coddy_storage_banner_dismissed")).toBe(
    "sessions",
  );

  // Still low on the next read: stays dismissed.
  answers.push(() => info({ ...low, freeBytes: 280 * MB }));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(true);

  // Full is not low: the dismissal does not apply, and is forgotten.
  answers.push(() => info({ ...low, state: "full", freeBytes: 0 }));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(false);
  dismissStorageLow();
  expect(snapshot().dismissedLow).toBe(false);

  // Low again later: it speaks again.
  answers.push(() => info(low));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(false);
});

test("a dismissal is remembered by the tab and forgotten when the disk recovers", async () => {
  window.sessionStorage.setItem("coddy_storage_banner_dismissed", "sessions");
  // A page that loads with the mark already there (a reload of the tab): a
  // fresh copy of the module reads it at start, with the React it renders with.
  vi.resetModules();
  const fresh = await import("./storageStatus");
  const rtl = await import("@testing-library/react");
  answers.push(() => info(low));
  await fresh.refreshStorageStatus();
  const { result } = rtl.renderHook(() => fresh.useStorageStatus());
  expect(result.current.dismissedLow).toBe(true);

  answers.push(() => info(ok));
  await fresh.refreshStorageStatus();
  expect(window.sessionStorage.getItem("coddy_storage_banner_dismissed")).toBe(
    null,
  );
  rtl.cleanup();
  fresh.resetStorageMonitorForTest();
});

test("the monitor reads at once, on a timer while visible, and on focus", async () => {
  vi.useFakeTimers();
  answers.push(
    () => info(ok),
    () => info(ok),
    () => info(ok),
  );
  startStorageMonitor();
  startStorageMonitor(); // idempotent
  expect(fetchMock).toHaveBeenCalledTimes(1);

  await vi.advanceTimersByTimeAsync(60_000);
  expect(fetchMock).toHaveBeenCalledTimes(2);

  window.dispatchEvent(new Event("focus"));
  expect(fetchMock).toHaveBeenCalledTimes(3);

  // A hidden tab does not poll.
  const hidden = vi.spyOn(document, "visibilityState", "get");
  hidden.mockReturnValue("hidden");
  await vi.advanceTimersByTimeAsync(60_000);
  expect(fetchMock).toHaveBeenCalledTimes(3);
  hidden.mockRestore();
});

// While the disk is full the person is freeing space, and the banner should go
// away soon after they have: the page asks every 15 seconds instead of every minute.
test("a full disk is asked about more often", async () => {
  vi.useFakeTimers();
  answers.push(
    () => info({ ...low, state: "full", freeBytes: 0 }),
    () => info({ ...low, state: "full", freeBytes: 0 }),
    () => info({ ...low, state: "full", freeBytes: 0 }),
    () => info(ok),
    () => info(ok),
  );
  startStorageMonitor();
  await vi.advanceTimersByTimeAsync(0);
  expect(getStorageSnapshot().status?.state).toBe("full");
  expect(fetchMock).toHaveBeenCalledTimes(1);

  // The first wait was set before the state was known: a minute.
  await vi.advanceTimersByTimeAsync(60_000);
  expect(fetchMock).toHaveBeenCalledTimes(2);
  // From then on, fifteen seconds.
  await vi.advanceTimersByTimeAsync(15_000);
  expect(fetchMock).toHaveBeenCalledTimes(3);
  await vi.advanceTimersByTimeAsync(15_000);
  expect(fetchMock).toHaveBeenCalledTimes(4);
  expect(getStorageSnapshot().status?.state).toBe("ok");

  // The disk has room again: back to a minute.
  await vi.advanceTimersByTimeAsync(15_000);
  expect(fetchMock).toHaveBeenCalledTimes(4);
  await vi.advanceTimersByTimeAsync(45_000);
  expect(fetchMock).toHaveBeenCalledTimes(5);
});

test("a switch in place to another remote forgets the last one's disk and reads the new one's", async () => {
  setEnv({ mode: "remote", baseUrl: "http://a.lan:1", token: "" });
  answers.push(() => info(low));
  startStorageMonitor();
  await vi.waitFor(() => expect(snapshot().status?.state).toBe("low"));
  dismissStorageLow();
  expect(snapshot().dismissedLow).toBe(true);

  answers.push(() => info(ok));
  connectRemote("http://b.lan:2", "");
  // At once: nothing of the other server's disk is shown for this one.
  expect(snapshot().status).toBeNull();
  expect(snapshot().dismissedLow).toBe(false);
  await vi.waitFor(() => expect(snapshot().status?.state).toBe("ok"));
  setEnv({ mode: "local" });
});
