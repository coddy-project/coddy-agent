import type { SwarmTopology, TopologyEdge, TopologyNode } from "./types";

export type PlacedNode = TopologyNode & {
  x: number;
  y: number;
  depth: number;
  /** The route a client would use to reach this node, empty for the root. */
  path: string[];
};

export type PlacedEdge = {
  from: PlacedNode;
  to: PlacedNode;
  name: string;
  /** True when this edge is not on the node's shortest route: a way round. */
  alternate: boolean;
};

export type TopologyLayout = {
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  width: number;
  height: number;
};

const TIER_HEIGHT = 120;
const NODE_SPACING = 170;
const MARGIN_X = 90;
const MARGIN_Y = 60;

/**
 * layoutTopology places the swarm on a tier per hop.
 *
 * Depth comes from the routes the relay computed, so a node sits at the number
 * of hops actually needed to reach it. In a ring that matters: the same relay
 * can be one hop away and also three, and drawing it at three would suggest the
 * long way is the only way.
 *
 * A pure function of the topology, so the arrangement can be checked without
 * rendering anything.
 */
export function layoutTopology(topology: SwarmTopology): TopologyLayout {
  const byUUID = new Map<string, TopologyNode>();
  for (const n of topology.nodes) {
    byUUID.set(n.uuid, n);
  }
  byUUID.set(topology.root.uuid, topology.root);

  const depths = new Map<string, number>([[topology.root.uuid, 0]]);
  const paths = new Map<string, string[]>([[topology.root.uuid, []]]);
  for (const [uuid, route] of Object.entries(topology.routes || {})) {
    depths.set(uuid, route.path.length);
    paths.set(uuid, route.path);
  }

  // A node with no route is unreachable from here; it still belongs on the
  // picture, parked past the deepest tier so its isolation is visible.
  let maxDepth = 0;
  for (const d of depths.values()) {
    maxDepth = Math.max(maxDepth, d);
  }
  for (const n of byUUID.values()) {
    if (!depths.has(n.uuid)) {
      depths.set(n.uuid, maxDepth + 1);
      paths.set(n.uuid, []);
    }
  }

  const tiers = new Map<number, TopologyNode[]>();
  for (const n of byUUID.values()) {
    const d = depths.get(n.uuid) ?? 0;
    const tier = tiers.get(d) || [];
    tier.push(n);
    tiers.set(d, tier);
  }

  const placed = new Map<string, PlacedNode>();
  let widest = 1;
  for (const [, members] of [...tiers.entries()].sort((a, b) => a[0] - b[0])) {
    widest = Math.max(widest, members.length);
  }
  const width = MARGIN_X * 2 + (widest - 1) * NODE_SPACING;

  for (const [depth, members] of [...tiers.entries()].sort(
    (a, b) => a[0] - b[0],
  )) {
    members.sort((a, b) => a.name.localeCompare(b.name));
    const rowWidth = (members.length - 1) * NODE_SPACING;
    const startX = (width - rowWidth) / 2;
    members.forEach((n, i) => {
      placed.set(n.uuid, {
        ...n,
        depth,
        path: paths.get(n.uuid) || [],
        x: startX + i * NODE_SPACING,
        y: MARGIN_Y + depth * TIER_HEIGHT,
      });
    });
  }

  const edges: PlacedEdge[] = [];
  for (const e of topology.edges || []) {
    const from = placed.get(e.from_uuid);
    const to = placed.get(e.to_uuid);
    if (!from || !to) {
      continue;
    }
    edges.push({ from, to, name: e.name, alternate: isAlternate(topology, e) });
  }

  const height = MARGIN_Y * 2 + (maxDepth + 1) * TIER_HEIGHT;
  return {
    nodes: [...placed.values()].sort((a, b) => a.depth - b.depth || a.x - b.x),
    edges,
    width,
    height,
  };
}

/**
 * An edge is an alternate when the node's chosen route does not end with it.
 * Those are the extra ways round a ring, worth drawing differently so the
 * picture says which path a request would actually take.
 */
function isAlternate(topology: SwarmTopology, edge: TopologyEdge): boolean {
  const route = (topology.routes || {})[edge.to_uuid];
  if (!route || route.path.length === 0) {
    return false;
  }
  return route.path[route.path.length - 1] !== edge.name;
}

/** Counts what the swarm holds, for a one-line summary above the graph. */
export function topologySummary(topology: SwarmTopology): {
  relays: number;
  agents: number;
  offline: number;
} {
  let relays = 1; // the relay we are attached to
  let agents = 0;
  let offline = 0;
  for (const n of topology.nodes) {
    if (n.uuid === topology.root.uuid) {
      continue;
    }
    if (n.kind === "relay") {
      relays += 1;
    } else {
      agents += 1;
    }
    if (!n.online) {
      offline += 1;
    }
  }
  return { relays, agents, offline };
}
