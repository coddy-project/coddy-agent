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

// A disk whose free space hovers around the threshold flips between ok and low
// as files come and go. A person who dismissed the warning must not be shown it
// again every time it dips: the dismissal is forgotten when the disk went
// through full, or has read ok for DISMISSAL_FORGOTTEN_AFTER_OK_MS (three
// minutes, three reads of the minute timer) without a low read in between.
// Only the clock is faked, so the reads themselves run as they do.
test("a dismissed warning stays dismissed while free space hovers around the threshold", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-10-10T10:00:00Z"));
  const at = async (secondsLater: number, storage: unknown) => {
    vi.setSystemTime(
      new Date(Date.parse("2026-10-10T10:00:00Z") + secondsLater * 1000),
    );
    answers.push(() => info(storage));
    await refreshStorageStatus();
  };

  // dismissedLow only has a meaning while the disk reads low (an ok disk shows no
  // banner anyway), so what is held across an ok read is the mark of the tab.
  const marked = () =>
    window.sessionStorage.getItem("coddy_storage_banner_dismissed");

  await at(0, low);
  dismissStorageLow();
  expect(snapshot().dismissedLow).toBe(true);

  // It dips back above the line and under it again, over and over.
  await at(60, ok);
  expect(snapshot().status?.state).toBe("ok");
  expect(marked()).toBe("sessions");
  await at(120, low);
  expect(snapshot().dismissedLow).toBe(true);
  await at(180, ok);
  await at(240, ok);
  await at(299, ok);
  expect(marked()).toBe("sessions");
  await at(300, low);
  expect(snapshot().dismissedLow).toBe(true);

  // Three minutes of ok without a low read in between: really recovered.
  await at(360, ok);
  await at(450, ok);
  await at(539, ok);
  expect(marked()).toBe("sessions");
  await at(540, ok);
  expect(marked()).toBeNull();
  // Low again afterwards: the warning speaks again.
  await at(600, low);
  expect(snapshot().dismissedLow).toBe(false);
  expect(snapshot().status?.state).toBe("low");
});

test("a burst of reads does not count as a recovery", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-10-10T10:00:00Z"));
  answers.push(() => info(low));
  await refreshStorageStatus();
  dismissStorageLow();
  // A focus, a visibility change and the timer within the same second all read ok.
  for (let i = 0; i < 5; i++) {
    answers.push(() => info(ok));
    await refreshStorageStatus();
  }
  answers.push(() => info(low));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(true);
});

test("a server that stops reporting a disk leaves the dismissal alone", async () => {
  answers.push(() => info(low));
  await refreshStorageStatus();
  dismissStorageLow();
  answers.push(
    () =>
      new Response(JSON.stringify({ object: "coddy.info" }), { status: 200 }),
  );
  await refreshStorageStatus();
  expect(snapshot().status).toBeNull();
  answers.push(() => info(low));
  await refreshStorageStatus();
  expect(snapshot().dismissedLow).toBe(true);
});

test("a dismissal is remembered by the tab and forgotten once the disk has recovered", async () => {
  window.sessionStorage.setItem("coddy_storage_banner_dismissed", "sessions");
  // A page that loads with the mark already there (a reload of the tab): a
  // fresh copy of the module reads it at start, with the React it renders with.
  vi.resetModules();
  const fresh = await import("./storageStatus");
  const rtl = await import("@testing-library/react");
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-10-10T10:00:00Z"));
  answers.push(() => info(low));
  await fresh.refreshStorageStatus();
  const { result } = rtl.renderHook(() => fresh.useStorageStatus());
  expect(result.current.dismissedLow).toBe(true);

  answers.push(() => info(ok));
  await fresh.refreshStorageStatus();
  expect(window.sessionStorage.getItem("coddy_storage_banner_dismissed")).toBe(
    "sessions",
  );
  vi.setSystemTime(new Date("2026-10-10T10:03:00Z"));
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
