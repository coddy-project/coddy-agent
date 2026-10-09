// The remotes the page's own server offers as environments: httpserver.remotes
// of the local configuration, read off the origin rather than through the
// environment shim, because the list of places to go must not travel with the
// place you are.
//
// The rail's environment menu and the History filter both list them. Each used to read
// the configuration once, when it mounted, so a remote committed while the page
// was open (a settings save, the agent's config_commit) stayed invisible until
// the composer happened to mount again (issue #401). The list lives here now:
// read on first use, again whenever a menu that shows it opens, and again on
// every config_reloaded the page hears (App.tsx).

import { useEffect, useSyncExternalStore } from "react";
import {
  connectRemote,
  getEnv,
  getRemoteToken,
  localFetch,
  setEnv,
  swarmRootRelay,
} from "./remoteEnv";

export type ConfiguredRemote = {
  name: string;
  url: string;
  /**
   * The bearer token the configuration carries for this remote, when it does
   * (httpserver.remotes[].token, which may be a ${ENV} reference on the
   * server). It wins over a token typed into this browser.
   */
  token?: string;
};

let current: ConfiguredRemote[] = [];
let started = false;
/** Reads are numbered: an answer older than the one applied is dropped. */
let issued = 0;
let applied = 0;
const listeners = new Set<() => void>();

function normalize(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

function parse(cfg: unknown): ConfiguredRemote[] | null {
  const http = (cfg as Record<string, unknown> | null)?.httpserver as
    | Record<string, unknown>
    | undefined;
  const raw = http?.remotes;
  if (raw === undefined) {
    return [];
  }
  if (!Array.isArray(raw)) {
    return null;
  }
  const out: ConfiguredRemote[] = [];
  for (const item of raw) {
    const o = (item ?? {}) as Record<string, unknown>;
    const url = String(o.url ?? "").trim();
    if (!url) {
      continue;
    }
    const remote: ConfiguredRemote = { name: String(o.name ?? "").trim(), url };
    const token = typeof o.token === "string" ? o.token.trim() : "";
    if (token) {
      remote.token = token;
    }
    out.push(remote);
  }
  return out;
}

function sameList(a: ConfiguredRemote[], b: ConfiguredRemote[]): boolean {
  return (
    a.length === b.length &&
    a.every(
      (r, i) =>
        r.name === b[i]?.name && r.url === b[i]?.url && r.token === b[i]?.token,
    )
  );
}

function publish(next: ConfiguredRemote[]): void {
  // An unchanged list keeps its identity, so a menu that probes on every new
  // list does not probe twice for one opening.
  if (sameList(current, next)) {
    return;
  }
  current = next;
  listeners.forEach((cb) => cb());
}

/**
 * refreshConfiguredRemotes reads the list again. A failed read keeps the list
 * on hand: the remotes are optional, and "Connect to…" works without them.
 */
export async function refreshConfiguredRemotes(): Promise<void> {
  started = true;
  const n = ++issued;
  let next: ConfiguredRemote[] | null = null;
  try {
    const res = await localFetch("/coddy/config");
    if (res.ok) {
      next = parse(await res.json());
    }
  } catch {
    next = null;
  }
  if (n < applied) {
    return;
  }
  applied = n;
  if (next) {
    publish(next);
    syncActiveToken(next);
  }
}

export function subscribeConfiguredRemotes(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function snapshotConfiguredRemotes(): ConfiguredRemote[] {
  return current;
}

/** useConfiguredRemotes is the list for a component, read on first use. */
export function useConfiguredRemotes(): ConfiguredRemote[] {
  useEffect(() => {
    if (!started) {
      void refreshConfiguredRemotes();
    }
  }, []);
  return useSyncExternalStore(
    subscribeConfiguredRemotes,
    snapshotConfiguredRemotes,
    snapshotConfiguredRemotes,
  );
}

/**
 * configuredRemoteFor finds the entry a base URL belongs to: the remote itself,
 * or a relay whose node mount the URL is (a node is reached with its relay's
 * client token).
 */
export function configuredRemoteFor(
  url: string,
  list: ConfiguredRemote[] = current,
): ConfiguredRemote | undefined {
  const base = normalize(url);
  let best: ConfiguredRemote | undefined;
  for (const r of list) {
    const own = normalize(r.url);
    if (base === own || base.startsWith(own + "/swarm/nodes/")) {
      if (!best || own.length > normalize(best.url).length) {
        best = r;
      }
    }
  }
  return best;
}

/**
 * tokenForRemote is the token to present to a configured remote: the one its
 * entry carries, else the one this browser kept for its address.
 */
export function tokenForRemote(remote: ConfiguredRemote): string {
  return remote.token || getRemoteToken(remote.url);
}

/**
 * connectConfiguredRemote points the app at a configured remote. A token from
 * the configuration is not copied into the browser's per-remote list: it stays
 * the server's, and a rotated one is picked up on the next read.
 */
export function connectConfiguredRemote(remote: ConfiguredRemote): void {
  connectRemote(remote.url, tokenForRemote(remote), remote.name, {
    rememberToken: !remote.token,
  });
}

/**
 * syncActiveToken hands the active environment a token its configured entry
 * changed since the environment was chosen, so a token rotated in the file is
 * used without choosing the remote again.
 */
function syncActiveToken(list: ConfiguredRemote[]): void {
  const env = getEnv();
  if (env.mode !== "remote") {
    return;
  }
  const entry = configuredRemoteFor(swarmRootRelay(env), list);
  if (!entry?.token || entry.token === env.token) {
    return;
  }
  setEnv({ ...env, token: entry.token });
}

/** resetConfiguredRemotesForTests forgets the list and every read on its way. */
export function resetConfiguredRemotesForTests(): void {
  current = [];
  started = false;
  issued = 0;
  applied = 0;
  listeners.forEach((cb) => cb());
}
