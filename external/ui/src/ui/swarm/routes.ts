import type { SwarmSession } from "./types";

/** The prefix a relay mounts each node under. */
export const MOUNT_PATH = "/swarm/nodes/";

/**
 * apiPathFor builds the path that reaches one node's API through the relay.
 *
 * A route is composed per request rather than by pointing the whole app at a
 * node, because the app can have several turns in flight on different nodes at
 * once. Moving one shared base under them would send a background reattach, a
 * cancel, or a permission answer to whichever node happened to be selected last.
 */
export function apiPathFor(nodePath: string[], path: string): string {
  const suffix = path.startsWith("/") ? path : `/${path}`;
  if (!nodePath || nodePath.length === 0) {
    return suffix;
  }
  return nodePath.map((n) => `${MOUNT_PATH}${n}`).join("") + suffix;
}

/** The path that reaches the node owning this session. */
export function sessionApiPath(session: SwarmSession, path: string): string {
  return apiPathFor(session.node_path, path);
}

/**
 * A session's identity in an aggregated list.
 *
 * Session ids are chosen per node and do collide across nodes, so a bare id is
 * not enough to key a row, a selection, or a request by.
 */
export function sessionKey(session: SwarmSession): string {
  return `${session.node_path.join("/")}/${session.id}`;
}

/** The hash route that opens one swarm session. */
export function swarmSessionHash(session: SwarmSession): string {
  return `#/swarm/s/${sessionKey(session)}`;
}

/**
 * Reads a swarm session reference out of a hash route.
 *
 * Node names cannot contain a separator, so the last segment is always the
 * session id and everything before it is the path to the node that owns it.
 */
export function parseSwarmSessionHash(
  hash: string,
): { nodePath: string[]; id: string } | null {
  const raw = hash.replace(/^#\/?/, "").trim();
  if (!raw.startsWith("swarm/s/")) {
    return null;
  }
  const rest = raw.slice("swarm/s/".length);
  if (!rest) {
    return null;
  }
  const parts = rest.split("/").filter((p) => p.length > 0);
  if (parts.length !== rest.split("/").length || parts.length === 0) {
    return null;
  }
  const id = parts[parts.length - 1];
  if (!id) {
    return null;
  }
  return { nodePath: parts.slice(0, -1), id };
}

/** A short, readable label for where a node sits in the swarm. */
export function routeLabel(nodePath: string[]): string {
  return nodePath.join(" › ");
}

/** What one node is doing right now, counted from the sessions it owns. */
export type NodeActivity = {
  sessions: number;
  running: number;
  waiting: number;
};

/**
 * Folds the aggregated session list into one line of work per node, keyed by
 * the joined route.
 *
 * The route is the key rather than the node name because two nodes reached by
 * different chains can carry the same name, and the map draws them apart.
 */
export function nodeActivity(
  sessions: SwarmSession[],
): Record<string, NodeActivity> {
  const out: Record<string, NodeActivity> = {};
  for (const s of sessions) {
    const key = s.node_path.join("/");
    const row = out[key] ?? { sessions: 0, running: 0, waiting: 0 };
    row.sessions += 1;
    if (s.turnActive) {
      row.running += 1;
    }
    if (s.permissionPending) {
      row.waiting += 1;
    }
    out[key] = row;
  }
  return out;
}

/**
 * The session on a node that a person came to the map for.
 *
 * Spotting on the map that a box is asking a question is only half the job; the
 * click after it has to land on the question, not on an empty chat. One waiting
 * on an answer wins, then one with a turn in flight, and freshest first inside
 * each - and when a node is merely idle there is nothing to open and the node's
 * own home is the right place.
 */
export function sessionToOpen(
  sessions: SwarmSession[],
  nodePath: string[],
): SwarmSession | null {
  const key = nodePath.join("/");
  const here = sessions.filter((s) => s.node_path.join("/") === key);
  const freshest = (rows: SwarmSession[]): SwarmSession | null =>
    rows.length === 0
      ? null
      : [...rows].sort((a, b) =>
          String(b.updatedAt || "").localeCompare(String(a.updatedAt || "")),
        )[0] || null;
  return (
    freshest(here.filter((s) => s.permissionPending)) ??
    freshest(here.filter((s) => s.turnActive))
  );
}
