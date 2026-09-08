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

/** Groups sessions by the node that owns them, nodes ordered by name. */
/** One card on the swarm screen: a node, how to reach it, and its work. */
export type NodeRow = {
  node: string;
  nodePath: string[];
  sessions: SwarmSession[];
  kind?: string;
};

export function groupByNode(sessions: SwarmSession[]): NodeRow[] {
  const groups = new Map<string, NodeRow>();
  for (const s of sessions) {
    const key = s.node_path.join("/") || s.node_name;
    const existing = groups.get(key);
    if (existing) {
      existing.sessions.push(s);
      continue;
    }
    groups.set(key, {
      node: s.node_name,
      nodePath: s.node_path,
      sessions: [s],
    });
  }
  return [...groups.values()].sort((a, b) =>
    a.nodePath.join("/").localeCompare(b.nodePath.join("/")),
  );
}

/** A short, readable label for where a node sits in the swarm. */
export function routeLabel(nodePath: string[]): string {
  return nodePath.join(" › ");
}

/** One entry in the node filter row. */
export type NodeChip = {
  /** Route to the node, joined - also the filter value. */
  path: string;
  /** What to show on the chip. */
  label: string;
  online: boolean;
  transport?: string;
  url?: string;
  /** True when this relay knows the node directly. */
  direct: boolean;
};

/**
 * Builds the node filter row from what the relay lists **and** what the sessions
 * came from.
 *
 * A relay only lists the nodes it holds leases for - its direct children - but a
 * session can arrive from any depth of the chain. Filtering by the registry
 * alone would therefore offer no way to narrow to the agent whose work is right
 * there on the screen.
 */
export function nodeChips(
  nodes: {
    name: string;
    online: boolean;
    transport?: string;
    url?: string;
    kind?: string;
  }[],
  sessions: SwarmSession[],
): NodeChip[] {
  const chips = new Map<string, NodeChip>();
  for (const n of nodes) {
    chips.set(n.name, {
      path: n.name,
      label: n.name,
      online: n.online,
      ...(n.transport !== undefined ? { transport: n.transport } : {}),
      ...(n.url !== undefined ? { url: n.url } : {}),
      direct: true,
    });
  }
  for (const s of sessions) {
    const path = s.node_path.join("/");
    if (!path || chips.has(path)) {
      continue;
    }
    chips.set(path, {
      path,
      label: s.node_name,
      online: true,
      ...(s.node_url ? { url: s.node_url } : {}),
      direct: false,
    });
  }
  return [...chips.values()].sort((a, b) => a.path.localeCompare(b.path));
}

/**
 * Every node worth showing a card for, whether or not it has work on it.
 *
 * A swarm that has just come up has no sessions anywhere, and a node with no
 * sessions used to have no card and therefore no way in - the one moment when
 * a person most needs a way in. Nodes come from the topology when there is one
 * (it reaches past the relay's own children), from the registry otherwise, and
 * from the sessions themselves for anything neither knew about.
 */
export function nodeRows(
  groups: NodeRow[],
  known: { path: string[]; name: string; kind?: string }[],
): NodeRow[] {
  const byKey = new Map<string, NodeRow>(
    groups.map((g) => [g.nodePath.join("/"), g]),
  );
  for (const n of known) {
    const key = n.path.join("/");
    if (!key || byKey.has(key)) {
      continue;
    }
    byKey.set(key, {
      node: n.name,
      nodePath: n.path,
      sessions: [],
      ...(n.kind !== undefined ? { kind: n.kind } : {}),
    });
  }
  return [...byKey.values()].sort((a, b) =>
    a.nodePath.join("/").localeCompare(b.nodePath.join("/")),
  );
}

/** Routes from a topology, as node paths: how to reach each node from here. */
export function topologyNodePaths(
  topology: {
    root: { uuid: string };
    nodes: { uuid: string; name: string; kind: string }[];
    routes: Record<string, { path: string[] }>;
  } | null,
): { path: string[]; name: string; kind?: string }[] {
  if (!topology) {
    return [];
  }
  const out: { path: string[]; name: string; kind?: string }[] = [];
  for (const n of topology.nodes) {
    if (n.uuid === topology.root.uuid) {
      continue;
    }
    const route = topology.routes[n.uuid];
    if (route && route.path.length > 0) {
      out.push({ path: route.path, name: n.name, kind: n.kind });
    }
  }
  return out;
}
