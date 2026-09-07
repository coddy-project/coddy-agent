import type {
  SwarmInfo,
  SwarmNode,
  SwarmSessionList,
  SwarmTopology,
} from "./types";

/**
 * A relay's API, reached through whatever fetch the app already installed. The
 * environment shim rewrites same-origin API paths to the selected environment,
 * so nothing here needs to know which relay is in play.
 */

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const init: RequestInit = { headers: { Accept: "application/json" } };
  if (signal) {
    init.signal = signal;
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    throw new Error(`${path}: ${res.status}`);
  }
  return (await res.json()) as T;
}

/**
 * Asks whether the current environment is a relay.
 *
 * A relay serves no model catalog, so the usual probe reports it as down. This
 * one is public by design: a client has to be able to tell a relay from a plain
 * agent before it holds any credential.
 */
export async function probeSwarm(
  signal?: AbortSignal,
): Promise<SwarmInfo | null> {
  try {
    const info = await getJSON<SwarmInfo>("/swarm/info", signal);
    return info && info.swarm ? info : null;
  } catch {
    return null;
  }
}

export async function fetchNodes(signal?: AbortSignal): Promise<SwarmNode[]> {
  const out = await getJSON<{ nodes: SwarmNode[] }>("/swarm/nodes", signal);
  return out.nodes || [];
}

export async function fetchSwarmSessions(
  opts: { q?: string; node?: string; limit?: number } = {},
  signal?: AbortSignal,
): Promise<SwarmSessionList> {
  const params = new URLSearchParams();
  params.set("limit", String(opts.limit ?? 100));
  params.set("include_activity", "true");
  if (opts.q) {
    params.set("q", opts.q);
  }
  if (opts.node) {
    params.set("node", opts.node);
  }
  const out = await getJSON<SwarmSessionList>(
    `/swarm/sessions?${params.toString()}`,
    signal,
  );
  return {
    sessions: out.sessions || [],
    warnings: out.warnings || [],
    node_more: out.node_more || {},
    hasMore: !!out.hasMore,
  };
}

export async function fetchTopology(
  signal?: AbortSignal,
): Promise<SwarmTopology> {
  return getJSON<SwarmTopology>("/swarm/topology", signal);
}
