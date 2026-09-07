// Reachability of the *active* environment (issue #60). A single shared monitor probes the
// selected remote on load, on an interval, and on window focus, so a down / misauthenticated
// remote is visible at a glance (composer chip dot + a banner) instead of the app silently
// rendering empty against a dead backend. Local is always "up".

import { useEffect } from "react";
import { useSyncExternalStore } from "react";
import { getEnv, localFetch, type CoddyEnv } from "./remoteEnv";

export type EnvHealth = "checking" | "up" | "down";

const PROBE_INTERVAL_MS = 30_000;
const PROBE_TIMEOUT_MS = 6_000;

let health: EnvHealth = "up";
let started = false;
const listeners = new Set<() => void>();

function set(h: EnvHealth): void {
  if (h !== health) {
    health = h;
    listeners.forEach((cb) => cb());
  }
}

/** probeEnvHealth resolves "up" when the environment answers, else "down"
 * (offline, DNS/TLS, refused, CORS-blocked, or 401/403). Local is always "up".
 *
 * An agent is asked for its model catalog. A relay has none - it serves no
 * models of its own, only the nodes it reaches - so a catalog probe would
 * report a perfectly healthy relay as unreachable. When the catalog is missing
 * the probe asks whether this is a swarm instead. */
export async function probeEnvHealth(env: CoddyEnv): Promise<EnvHealth> {
  if (env.mode !== "remote") return "up";
  const headers = env.token ? { Authorization: "Bearer " + env.token } : {};
  try {
    const res = await localFetch(env.baseUrl + "/v1/models", {
      headers,
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
    if (res.ok) {
      return "up";
    }
    // A 401 means something is there and refusing us, which is a credential
    // problem rather than an unreachable host; only a missing catalog is worth
    // a second question.
    if (res.status !== 404) {
      return "down";
    }
  } catch {
    return "down";
  }
  try {
    const res = await localFetch(env.baseUrl + "/swarm/info", {
      headers,
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
    return res.ok ? "up" : "down";
  } catch {
    return "down";
  }
}

async function tick(): Promise<void> {
  const env = getEnv();
  if (env.mode !== "remote") {
    set("up");
    return;
  }
  if (health !== "up") set("checking");
  set(await probeEnvHealth(env));
}

/** startActiveHealthMonitor begins probing the active environment (idempotent). */
export function startActiveHealthMonitor(): void {
  if (started || typeof window === "undefined") return;
  started = true;
  health = getEnv().mode === "remote" ? "checking" : "up";
  void tick();
  window.setInterval(() => void tick(), PROBE_INTERVAL_MS);
  window.addEventListener("focus", () => void tick());
}

export function subscribeHealth(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}
export function snapshotHealth(): EnvHealth {
  return health;
}

/** useActiveEnvHealth returns the live health of the active environment for React components. */
export function useActiveEnvHealth(): EnvHealth {
  useEffect(() => startActiveHealthMonitor(), []);
  return useSyncExternalStore(subscribeHealth, snapshotHealth, snapshotHealth);
}
