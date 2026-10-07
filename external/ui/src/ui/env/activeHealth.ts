// Reachability of the *active* environment (issue #60). A single shared monitor probes the
// selected remote on load, on an interval, and on window focus, so a down / misauthenticated
// remote is visible at a glance (composer chip dot + a banner) instead of the app silently
// rendering empty against a dead backend. Local is always "up".

import { useEffect } from "react";
import { useSyncExternalStore } from "react";
import {
  environmentKey,
  getEnv,
  onEnvironmentSwitch,
  subscribeEnv,
  type CoddyEnv,
} from "./remoteEnv";
import { probeRemote, type RemoteProbe } from "./remoteProbe";

export type EnvHealth = "checking" | "up" | "down";

/** The active environment's health and, for a remote, what its probe answered. */
export type ActiveEnvProbe = {
  health: EnvHealth;
  /** Null for Local and before the first answer. */
  probe: RemoteProbe | null;
};

const PROBE_INTERVAL_MS = 30_000;

let state: ActiveEnvProbe = { health: "up", probe: null };
let started = false;
/**
 * Asks are numbered, and only the newest one's answer is kept: two can be in
 * flight (the interval, a focus, a switch), and a timeout of an older one
 * landing after a newer "up" put a remote that had just come back down again.
 */
let asked = 0;
/** The environment the verdict in `state` is about. */
let stateKey = "";
/** The token the newest ask presented. */
let askedToken = "";
const listeners = new Set<() => void>();

function set(next: ActiveEnvProbe): void {
  if (
    next.health !== state.health ||
    next.probe?.reach !== state.probe?.reach ||
    next.probe?.relay !== state.probe?.relay
  ) {
    state = next;
    listeners.forEach((cb) => cb());
  }
}

/**
 * probeActiveEnv asks a remote environment what it answers with its token
 * (remoteProbe.ts): an agent its model catalog, a relay its node list, and a
 * remote the browser gets no answer from whether it is there at all. Null for
 * Local, which is always there.
 */
export async function probeActiveEnv(
  env: CoddyEnv,
): Promise<RemoteProbe | null> {
  if (env.mode !== "remote") {
    return null;
  }
  return probeRemote(env.baseUrl, env.token);
}

/** probeEnvHealth resolves "up" when the environment answers and accepts its token, else
 * "down" (offline, DNS/TLS, refused, CORS-blocked, or 401/403). Local is always "up". */
export async function probeEnvHealth(env: CoddyEnv): Promise<EnvHealth> {
  const probe = await probeActiveEnv(env);
  return !probe || probe.reach === "up" ? "up" : "down";
}

async function tick(): Promise<void> {
  const env = getEnv();
  if (env.mode !== "remote") {
    stateKey = environmentKey(env);
    set({ health: "up", probe: null });
    return;
  }
  // Asked again, a remote keeps its last verdict until the answer changes it:
  // a down one going "checking" on every ask took the banner away for the
  // length of each probe and, on the stacked shell, moved the page with it.
  // "checking" is only where a remote starts (startActiveHealthMonitor, a
  // switch in place).
  const mine = ++asked;
  askedToken = env.token;
  const probe = await probeActiveEnv(env);
  // A newer ask is on its way, or the app moved to another remote while this
  // one was asked: this answer is not the one to keep.
  if (mine !== asked || environmentKey(getEnv()) !== environmentKey(env)) {
    return;
  }
  stateKey = environmentKey(env);
  set({ health: !probe || probe.reach === "up" ? "up" : "down", probe });
}

// A switch in place: another remote starts from "checking", its health is not
// the last one's; the same remote chosen again keeps its verdict on screen
// until the new ask answers, as on any other ask.
onEnvironmentSwitch(() => {
  if (!started) {
    return;
  }
  if (environmentKey(getEnv()) !== stateKey) {
    set({ health: "checking", probe: null });
  }
  void tick();
});

// A token rotated under the same remote (configuredRemotes.syncActiveToken)
// is asked with at once: the verdict is about the token presented, and an ask
// still on its way with the old one is superseded.
subscribeEnv(() => {
  if (!started) {
    return;
  }
  const env = getEnv();
  if (
    env.mode === "remote" &&
    environmentKey(env) === stateKey &&
    env.token !== askedToken
  ) {
    void tick();
  }
});

/** startActiveHealthMonitor begins probing the active environment (idempotent). */
export function startActiveHealthMonitor(): void {
  if (started || typeof window === "undefined") return;
  started = true;
  state = {
    health: getEnv().mode === "remote" ? "checking" : "up",
    probe: null,
  };
  stateKey = environmentKey(getEnv());
  void tick();
  window.setInterval(() => void tick(), PROBE_INTERVAL_MS);
  window.addEventListener("focus", () => void tick());
}

export function subscribeHealth(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}
export function snapshotHealth(): EnvHealth {
  return state.health;
}
function snapshotProbe(): ActiveEnvProbe {
  return state;
}

/** useActiveEnvHealth returns the live health of the active environment for React components. */
export function useActiveEnvHealth(): EnvHealth {
  useEffect(() => startActiveHealthMonitor(), []);
  return useSyncExternalStore(subscribeHealth, snapshotHealth, snapshotHealth);
}

/** useActiveEnvProbe is useActiveEnvHealth with the answer that decided it. */
export function useActiveEnvProbe(): ActiveEnvProbe {
  useEffect(() => startActiveHealthMonitor(), []);
  return useSyncExternalStore(subscribeHealth, snapshotProbe, snapshotProbe);
}
