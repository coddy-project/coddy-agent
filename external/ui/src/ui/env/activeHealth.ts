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

/** probeEnvHealth resolves "up" when the environment answers `GET /v1/models` (authorized), else
 * "down" (offline, DNS/TLS, refused, CORS-blocked, or 401/403). Local is always "up". */
export async function probeEnvHealth(env: CoddyEnv): Promise<EnvHealth> {
  if (env.mode !== "remote") return "up";
  try {
    const res = await localFetch(env.baseUrl + "/v1/models", {
      headers: env.token ? { Authorization: "Bearer " + env.token } : {},
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
