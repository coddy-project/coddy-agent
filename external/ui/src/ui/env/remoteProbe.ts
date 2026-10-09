// What a remote environment answers, asked from the browser (issue #401).
//
// One question used to be asked of every remote: GET /v1/models. A relay serves
// no /v1 at all - it answers 404 whatever the token - so a healthy relay was
// always red, and a relay was called up by a second question that needs no
// token at all (/swarm/info), which made one without a token look healthy. A
// browser that the remote's CORS does not admit got no answer whatsoever, and
// that was reported the same way as a machine that is switched off.
//
// probeRemote asks as many questions as it takes to tell those apart, and the
// menu, the chip and the banner all read the one answer.

import { localFetch } from "./remoteEnv";

/**
 * How far a remote got:
 *
 * - `up` - it answered and accepted the token;
 * - `unauthorized` - it answered and refused the token, or needs one;
 * - `cors` - it answered, but the browser kept the answer from the page because
 *   the remote's CORS settings do not name this page's origin;
 * - `down` - nothing answered (offline, a wrong address, DNS, TLS, a timeout),
 *   or what answered is neither a coddy serve nor a relay.
 */
export type RemoteReach = "up" | "unauthorized" | "cors" | "down";

export type RemoteProbe = {
  reach: RemoteReach;
  /** The remote said it is a swarm relay (GET /swarm/info). */
  relay: boolean;
};

/** One agent a relay reaches, with the route the relay knows it by. */
export type RelayAgent = {
  name: string;
  /** Node path from the relay: one name per hop. */
  path: string[];
  online: boolean;
};

export const PROBE_TIMEOUT_MS = 6_000;

function trimBase(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

function authHeaders(token: string): Record<string, string> {
  return token ? { Authorization: "Bearer " + token } : {};
}

function isRefusal(status: number): boolean {
  return status === 401 || status === 403;
}

/**
 * isRelay asks the public swarm route. A bare relay answers it without a
 * token, so when the caller holds none the request stays a simple one and
 * costs no preflight. A relay reached through its parent's mount sits behind
 * the parent's client token - there the same route refuses a request that
 * carries no credential at all - so a held token goes along; the public route
 * ignores it either way.
 */
async function isRelay(
  base: string,
  token: string,
  timeoutMs: number,
): Promise<boolean> {
  try {
    const res = await localFetch(base + "/swarm/info", {
      headers: { ...authHeaders(token), Accept: "application/json" },
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) {
      return false;
    }
    const info = (await res.json()) as { swarm?: unknown } | null;
    return !!info && info.swarm === true;
  } catch {
    return false;
  }
}

/**
 * answersOpaquely reports whether anything answers at all, asked in a way that
 * needs no CORS: a `no-cors` request resolves with an opaque response for any
 * HTTP answer and fails only when nothing answered. A remote that answers here
 * and failed the ordinary request is one whose CORS settings leave this page
 * out - which the browser otherwise reports exactly like a machine that is off.
 */
async function answersOpaquely(
  base: string,
  timeoutMs: number,
): Promise<boolean> {
  try {
    await localFetch(base + "/swarm/info", {
      mode: "no-cors",
      signal: AbortSignal.timeout(timeoutMs),
    });
    return true;
  } catch {
    return false;
  }
}

/** probeRemote tells what a remote environment answers with this token. */
export async function probeRemote(
  baseUrl: string,
  token: string,
  timeoutMs = PROBE_TIMEOUT_MS,
): Promise<RemoteProbe> {
  const base = trimBase(baseUrl);
  let models: Response;
  try {
    models = await localFetch(base + "/v1/models", {
      headers: authHeaders(token),
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch (e) {
    // A request given up on (the timeout) is not one the browser kept from
    // the page: only a failed fetch - what CORS and a dead machine both look
    // like - is told apart by asking again without CORS.
    const name = (e as { name?: unknown } | null)?.name;
    if (name === "TimeoutError" || name === "AbortError") {
      return { reach: "down", relay: false };
    }
    return {
      reach: (await answersOpaquely(base, timeoutMs)) ? "cors" : "down",
      relay: false,
    };
  }
  if (models.ok) {
    return { reach: "up", relay: false };
  }
  // A 401 is an agent refusing the token; a 404 is what a relay says to any
  // /v1 request. The public swarm route tells which of the two this is.
  const relay = await isRelay(base, token, timeoutMs);
  if (!relay) {
    return { reach: isRefusal(models.status) ? "unauthorized" : "down", relay };
  }
  // /swarm/info is public, so it says nothing about the token. The node list
  // takes the client token, and a relay that lists its nodes has accepted it.
  try {
    const nodes = await localFetch(base + "/swarm/nodes", {
      headers: { ...authHeaders(token), Accept: "application/json" },
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (nodes.ok) {
      return { reach: "up", relay };
    }
    return { reach: isRefusal(nodes.status) ? "unauthorized" : "down", relay };
  } catch {
    return { reach: "down", relay };
  }
}

/**
 * reportedName asks a remote what it calls itself, for an entry of
 * httpserver.remotes that gives no name: a relay answers with its name on the
 * public GET /swarm/info (its host name unless swarm.name says otherwise), an
 * agent with the host name it runs on (GET /coddy/info, behind its token).
 * Empty when it does not say, and for a remote the probe could not reach.
 */
export async function reportedName(
  baseUrl: string,
  token: string,
  probe: RemoteProbe,
  timeoutMs = PROBE_TIMEOUT_MS,
): Promise<string> {
  const base = trimBase(baseUrl);
  const read = async (path: string, field: string, auth: boolean) => {
    try {
      const res = await localFetch(base + path, {
        headers: {
          ...(auth ? authHeaders(token) : {}),
          Accept: "application/json",
        },
        signal: AbortSignal.timeout(timeoutMs),
      });
      if (!res.ok) {
        return "";
      }
      const body = (await res.json()) as Record<string, unknown> | null;
      const value = body?.[field];
      return typeof value === "string" ? value.trim() : "";
    } catch {
      return "";
    }
  };
  if (probe.relay) {
    return read("/swarm/info", "name", false);
  }
  return probe.reach === "up" ? read("/coddy/info", "hostname", true) : "";
}

type TopologyAnswer = {
  nodes?: { uuid?: string; name?: string; kind?: string; online?: boolean }[];
  routes?: Record<string, { path?: string[] } | undefined>;
};

type NodeListAnswer = {
  nodes?: { name?: string; kind?: string; online?: boolean }[];
};

/**
 * relayAgents lists the agents a relay reaches, behind every hop, for the
 * environment menu: one click there points the app at the node instead of
 * opening the relay's map first. A relay is not entered this way (the map is
 * how a relay is used), so only agents are listed. Empty when the relay does
 * not answer; the menu then shows the relay alone, as it did before.
 */
export async function relayAgents(
  relayUrl: string,
  token: string,
  timeoutMs = PROBE_TIMEOUT_MS,
): Promise<RelayAgent[]> {
  const base = trimBase(relayUrl);
  const init = {
    headers: { ...authHeaders(token), Accept: "application/json" },
    signal: AbortSignal.timeout(timeoutMs),
  };
  try {
    const res = await localFetch(base + "/swarm/topology", init);
    if (res.ok) {
      const topo = (await res.json()) as TopologyAnswer;
      const out: RelayAgent[] = [];
      for (const n of topo.nodes ?? []) {
        const path = (n.uuid ? topo.routes?.[n.uuid]?.path : undefined) ?? [];
        if (n.kind !== "agent" || !n.name || path.length === 0) {
          continue;
        }
        out.push({ name: n.name, path: [...path], online: !!n.online });
      }
      return sortAgents(out);
    }
  } catch {
    /* fall back to the direct children below */
  }
  try {
    const res = await localFetch(base + "/swarm/nodes", {
      ...init,
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) {
      return [];
    }
    const list = (await res.json()) as NodeListAnswer;
    return sortAgents(
      (list.nodes ?? [])
        .filter((n) => n.kind === "agent" && !!n.name)
        .map((n) => ({
          name: n.name as string,
          path: [n.name as string],
          online: !!n.online,
        })),
    );
  } catch {
    return [];
  }
}

/** Nearest first, then by route, so a chain reads from the top down. */
function sortAgents(list: RelayAgent[]): RelayAgent[] {
  return list.sort(
    (a, b) =>
      a.path.length - b.path.length ||
      a.path.join("/").localeCompare(b.path.join("/"), undefined, {
        numeric: true,
        sensitivity: "base",
      }),
  );
}
