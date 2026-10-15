import { useSyncExternalStore } from "react";
import { environmentKey, getEnv, onEnvironmentSwitch } from "../env/remoteEnv";

/** The document GET /coddy/status answers (serve.StatusDocument). */
export type SystemStatusDocument = {
  object: "coddy.status";
  generatedAt: string;
  build: {
    version: string;
    revision?: string;
    goVersion: string;
    os: string;
    arch: string;
    tags?: string[];
  };
  process: { startedAt: string; startedBy: string; configFile?: string };
  subsystems: SystemSubsystem[];
  restartRequired?: boolean;
  restartRefused?: string;
  turns: { running: number; oldestSeconds?: number };
  /** Prompts waiting for an answer; a count left out is none. */
  prompts?: { permission?: number; question?: number };
  tasks?: Record<string, Record<string, number>>;
  disk?: { availableBytes: number; sizeBytes: number };
  unavailable?: string[];
  attention?: SystemAttention[];
  metrics?: { providers: SystemProvider[]; series: SystemSeries };
};

export type SystemSubsystem = {
  kind: string;
  state: string;
  address?: string;
  pendingAddress?: string;
  credential?: string;
};

export type SystemAttention = {
  code: string;
  subsystem?: string;
  provider?: string;
  /** swarm_join_down: the relay (host[:port]) and since when it is unreachable. */
  relay?: string;
  offlineSince?: string;
};

export type SystemProvider = {
  name: string;
  attempts: number;
  failures: number;
  meanSeconds?: number;
  lastFailureAt?: string;
  lastOkAt?: string;
};

export type SystemSeries = {
  stepSeconds: number;
  end: string;
  attemptsOk: (number | null)[];
  providerErrors: (number | null)[];
  attemptSecondsMean: (number | null)[];
  tokensIn: (number | null)[];
  tokensOut: (number | null)[];
  turnsRunning: (number | null)[];
  tasksUnfinished: (number | null)[];
};

/** Why a read left no document. */
export type SystemStatusIssue =
  | { kind: "notFound" }
  | { kind: "unauthorized" }
  | { kind: "offline"; node: string; lastSeen?: string }
  | { kind: "failed" };

export type SystemStatusState = {
  doc: SystemStatusDocument | null;
  at: number;
  issue: SystemStatusIssue | null;
  stale: { doc: SystemStatusDocument; at: number } | null;
};

/**
 * The addresses that apply after a restart, one for each moved listener and
 * comma-separated, "" when none moved (a saved change can wait for a restart
 * without one). No two listeners share an address.
 */
export function pendingAddresses(doc: SystemStatusDocument): string {
  return doc.subsystems
    .map((s) => s.pendingAddress)
    .filter(Boolean)
    .join(", ");
}

/** How long the read Settings makes when it opens serves the System tab. */
export const SYSTEM_STATUS_REUSE_MS = 5_000;

const EMPTY: SystemStatusState = { doc: null, at: 0, issue: null, stale: null };

// One document per environment, shared by the badge, the tab and the read after
// Save all. Reads are ordered twice over: a generation per environment (raised
// by a switch, so an answer from before it is never applied and a new
// environment starts empty) and a sequence number per request (an answer, a
// success or a failure, applies only when newer than the last one applied).
let state = EMPTY;
let generation = 0;
let scope = "";
let requested = 0;
let applied = 0;
let inflight: Promise<SystemStatusDocument | null> | null = null;
const listeners = new Set<() => void>();

function notify(): void {
  listeners.forEach((cb) => cb());
}

function forget(): void {
  generation++;
  state = EMPTY;
  inflight = null;
  notify();
}

onEnvironmentSwitch(forget);

/** A change of environment that no switch announced (tests, a token) starts over too. */
function sync(): void {
  const key = environmentKey(getEnv());
  if (key !== scope) {
    scope = key;
    forget();
  }
}

type Outcome = { doc: SystemStatusDocument } | { issue: SystemStatusIssue };

/** What a response says: a document, or why there is none. */
async function outcomeOf(res: Response): Promise<Outcome> {
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    /* an empty or foreign body says nothing more */
  }
  if (res.ok) {
    return (body as { object?: unknown } | null)?.object === "coddy.status"
      ? { doc: body as SystemStatusDocument }
      : { issue: { kind: "failed" } };
  }
  if (res.status === 401 || res.status === 403) {
    return { issue: { kind: "unauthorized" } };
  }
  // A relay names the node it could not reach in its own error.
  const hop = (
    body as { error?: { node?: unknown; last_seen?: unknown } } | null
  )?.error;
  if (typeof hop?.node === "string") {
    return {
      issue: {
        kind: "offline",
        node: hop.node,
        ...(typeof hop.last_seen === "string"
          ? { lastSeen: hop.last_seen }
          : {}),
      },
    };
  }
  return { issue: { kind: res.status === 404 ? "notFound" : "failed" } };
}

/**
 * Reads GET /coddy/status of the environment the page is on. A read in flight
 * is shared, except with `fresh` (Save all), which always asks again and is the
 * one later readers then join. Resolves to the document that stands after the
 * answer, null when there is none or the environment changed meanwhile.
 */
export function readSystemStatus(
  opts: { fresh?: boolean } = {},
): Promise<SystemStatusDocument | null> {
  sync();
  if (inflight !== null && opts.fresh !== true) {
    return inflight;
  }
  const mine = ++requested;
  const asked = generation;
  const read: Promise<SystemStatusDocument | null> = fetch("/coddy/status", {
    headers: { Accept: "application/json" },
  })
    .then(outcomeOf, (): Outcome => ({ issue: { kind: "failed" } }))
    .then((outcome) => {
      if (inflight === read) {
        inflight = null;
      }
      if (asked !== generation) {
        return null;
      }
      if (mine > applied) {
        applied = mine;
        const at = Date.now();
        if ("doc" in outcome) {
          state = { doc: outcome.doc, at, issue: null, stale: null };
        } else {
          // A server that is merely out of reach keeps its last values for the
          // tab to grey; one that does not report, or refuses, keeps nothing.
          const kept = ["offline", "failed"].includes(outcome.issue.kind);
          state = {
            doc: null,
            at,
            issue: outcome.issue,
            stale: !kept
              ? null
              : state.doc
                ? { doc: state.doc, at: state.at }
                : state.stale,
          };
        }
        notify();
      }
      return state.doc;
    });
  inflight = read;
  return read;
}

/**
 * The System tab's first read: the one in flight, or the document the read at
 * Settings' opening left while it is younger than SYSTEM_STATUS_REUSE_MS, else
 * a read at once.
 */
export function openSystemStatus(): Promise<SystemStatusDocument | null> {
  sync();
  if (inflight !== null) {
    return inflight;
  }
  if (state.at > 0 && Date.now() - state.at < SYSTEM_STATUS_REUSE_MS) {
    return Promise.resolve(state.doc);
  }
  return readSystemStatus();
}

export function snapshotSystemStatus(): SystemStatusState {
  return state;
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/**
 * Follows the store through a selector. One that returns a number or a string
 * draws its component again only when that changes, so the drawer, which reads
 * the count, is not redrawn by every poll of the tab.
 */
export function useSystemStatus<T>(select: (s: SystemStatusState) => T): T {
  return useSyncExternalStore(
    subscribe,
    () => select(state),
    () => select(state),
  );
}

/** Forgets everything the store holds, for tests. */
export function resetSystemStatusForTests(): void {
  scope = "";
  applied = requested;
  forget();
}
