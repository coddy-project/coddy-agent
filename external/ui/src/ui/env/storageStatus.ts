// The room left on the disk that stores the server's sessions (issue #465). The server says
// it in GET /coddy/info (`storage`): the state against sessions.min_free_mb, the figures of the
// disk that decides it and the threshold. This module keeps the latest answer for the page of
// the active environment, reads it again on a slow timer, on the window's focus and whenever
// the events stream says something moved (a save failed on a full disk, a configuration
// reload, a reconnect), and remembers that the person dismissed a low-space warning. The
// banner (chat/StorageBanner.tsx) only renders what is kept here.

import { useEffect, useSyncExternalStore } from "react";
import { onEnvironmentSwitch } from "./remoteEnv";

export type StorageState = "ok" | "low" | "full";

export type StorageStatus = {
  state: StorageState;
  /** Which disk the figures are about; "" when none could be read. */
  volume: "sessions" | "home" | "";
  /** Bytes the server can still write on that disk; null when no disk could be read. */
  freeBytes: number | null;
  totalBytes: number | null;
  /** The threshold sessions.min_free_mb sets, in bytes; 0 when the warning is off. */
  minFreeBytes: number;
};

/** What the page holds: the server's last word on the disk, and the dismissal of its warning. */
export type StorageSnapshot = {
  /** Null while unknown: before the first answer, from a server that says nothing, after a switch. */
  status: StorageStatus | null;
  /** The person dismissed the low-space warning of this disk; a full disk is never dismissable. */
  dismissedLow: boolean;
};

/** How often the page asks while the disk has room or is only low. */
const POLL_INTERVAL_MS = 60_000;
/**
 * How often it asks while the disk is full: the person is freeing space, and the banner
 * should go away soon after they have, not up to a minute later. The server tries a write
 * at most every ten seconds, so a shorter interval would only ask in vain.
 */
const POLL_FULL_INTERVAL_MS = 15_000;
const DISMISSED_KEY = "coddy_storage_banner_dismissed";

function count(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null;
}

/**
 * storageStatusOfInfo reads the `storage` object of a GET /coddy/info answer. Null for a server
 * that sends none (an older build, nothing to report) and for one whose state this page does not
 * know: a banner must never claim what it cannot read.
 */
export function storageStatusOfInfo(info: unknown): StorageStatus | null {
  const storage = (info as { storage?: unknown } | null)?.storage;
  if (!storage || typeof storage !== "object") return null;
  const s = storage as Record<string, unknown>;
  if (s["state"] !== "ok" && s["state"] !== "low" && s["state"] !== "full") {
    return null;
  }
  const volume =
    s["volume"] === "home"
      ? "home"
      : s["volume"] === "sessions"
        ? "sessions"
        : "";
  return {
    state: s["state"],
    volume,
    freeBytes: volume ? count(s["freeBytes"]) : null,
    totalBytes: volume ? count(s["totalBytes"]) : null,
    minFreeBytes: count(s["minFreeBytes"]) ?? 0,
  };
}

function readDismissed(): string {
  try {
    return window.sessionStorage.getItem(DISMISSED_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeDismissed(volume: string): void {
  try {
    if (volume) window.sessionStorage.setItem(DISMISSED_KEY, volume);
    else window.sessionStorage.removeItem(DISMISSED_KEY);
  } catch {
    // Storage can be blocked or full; the warning then comes back with the page.
  }
}

let status: StorageStatus | null = null;
/** The disk whose low-space warning was dismissed ("" for none), kept per tab. */
let dismissedVolume = typeof window === "undefined" ? "" : readDismissed();
let snapshot: StorageSnapshot = compute();
let started = false;
/** Reads are numbered, and only the newest answer is kept. */
let asked = 0;
const listeners = new Set<() => void>();

function compute(): StorageSnapshot {
  return {
    status,
    dismissedLow:
      status?.state === "low" &&
      dismissedVolume !== "" &&
      dismissedVolume === status.volume,
  };
}

function publish(): void {
  const next = compute();
  if (
    next.status === snapshot.status &&
    next.dismissedLow === snapshot.dismissedLow
  ) {
    return;
  }
  snapshot = next;
  listeners.forEach((cb) => cb());
}

function sameStatus(a: StorageStatus | null, b: StorageStatus | null): boolean {
  return (
    a === b ||
    (!!a &&
      !!b &&
      a.state === b.state &&
      a.volume === b.volume &&
      a.freeBytes === b.freeBytes &&
      a.totalBytes === b.totalBytes &&
      a.minFreeBytes === b.minFreeBytes)
  );
}

function setStatus(next: StorageStatus | null): void {
  if (!sameStatus(status, next)) status = next;
  // A warning that is no longer one is forgotten, so the next time the disk runs low it speaks again.
  if (status?.state !== "low" && dismissedVolume) {
    dismissedVolume = "";
    writeDismissed("");
  }
  publish();
}

/**
 * refreshStorageStatus reads GET /coddy/info of the active environment (the fetch shim sends it
 * to the local server, a remote or a node behind a relay) and keeps its `storage`. A read that
 * fails keeps what is held: a blip of the network is no reason for the banner to flicker, and a
 * remote that is really down is the environment alert's to say.
 */
export async function refreshStorageStatus(): Promise<void> {
  const mine = ++asked;
  let info: unknown;
  try {
    const res = await fetch("/coddy/info", {
      headers: { Accept: "application/json" },
    });
    if (!res.ok) return;
    info = await res.json();
  } catch {
    return;
  }
  // A newer read is on its way, or the environment changed while this one was asked.
  if (mine !== asked) return;
  setStatus(storageStatusOfInfo(info));
}

/**
 * noteStorageWriteFailing is what the events stream (event: storage_status) and a 507 answer
 * tell the page: a save has just failed on a full disk (or the failure has ended). The page
 * shows the full-disk banner at once, with the figures it already has, and reads the server's
 * own answer right after, which corrects it if the failure is already over.
 */
export function noteStorageWriteFailing(failing: boolean): void {
  if (failing) {
    setStatus({
      volume: "",
      freeBytes: null,
      totalBytes: null,
      minFreeBytes: 0,
      ...status,
      state: "full",
    });
  }
  void refreshStorageStatus();
}

/** dismissStorageLow hides the low-space warning for this tab until the disk stops being low. */
export function dismissStorageLow(): void {
  if (status?.state !== "low") return;
  dismissedVolume = status.volume || "sessions";
  writeDismissed(dismissedVolume);
  publish();
}

/** resetStorageStatus forgets everything held, for a switch to another environment. */
export function resetStorageStatus(): void {
  asked += 1;
  status = null;
  dismissedVolume = "";
  writeDismissed("");
  publish();
}

// A switch in place reads the other server's disk, not the last one's.
onEnvironmentSwitch(() => {
  resetStorageStatus();
  if (started) void refreshStorageStatus();
});

/** What the monitor set up, so a test can take it down again. */
let stopMonitor: (() => void) | null = null;

/** startStorageMonitor begins keeping the status fresh (idempotent). */
export function startStorageMonitor(): void {
  if (started || typeof window === "undefined") return;
  started = true;
  void refreshStorageStatus();
  let timer = 0;
  let live = true;
  const schedule = () => {
    timer = window.setTimeout(
      async () => {
        if (document.visibilityState !== "hidden") await refreshStorageStatus();
        if (live) schedule();
      },
      status?.state === "full" ? POLL_FULL_INTERVAL_MS : POLL_INTERVAL_MS,
    );
  };
  schedule();
  const onFocus = () => void refreshStorageStatus();
  const onVisible = () => {
    if (document.visibilityState === "visible") void refreshStorageStatus();
  };
  window.addEventListener("focus", onFocus);
  document.addEventListener("visibilitychange", onVisible);
  stopMonitor = () => {
    live = false;
    window.clearTimeout(timer);
    window.removeEventListener("focus", onFocus);
    document.removeEventListener("visibilitychange", onVisible);
  };
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

/** getStorageSnapshot is what the page holds right now, without starting anything. */
export function getStorageSnapshot(): StorageSnapshot {
  return snapshot;
}

/** useStorageStatus returns what the page holds about the server's disk, and starts keeping it fresh. */
export function useStorageStatus(): StorageSnapshot {
  useEffect(() => startStorageMonitor(), []);
  return useSyncExternalStore(
    subscribe,
    getStorageSnapshot,
    getStorageSnapshot,
  );
}

/** Test seam: take the monitor down and forget everything, so a test starts from a page that never asked. */
export function resetStorageMonitorForTest(): void {
  stopMonitor?.();
  stopMonitor = null;
  started = false;
  resetStorageStatus();
}
