import type { SwarmTopology, TopologyEdge, TopologyNode } from "./types";

export type PlacedNode = TopologyNode & {
  x: number;
  y: number;
  depth: number;
  /** The route a client would use to reach this node, empty for the root. */
  path: string[];
};

export type PlacedEdge = {
  /** Stable across polls, so a highlighted route survives a refresh. */
  id: string;
  from: PlacedNode;
  to: PlacedNode;
  name: string;
  /** True when this edge is not on the node's shortest route: a way round. */
  alternate: boolean;
  /** x of the lane a row-skipping link goes round in, clear of every node. */
  laneX: number;
};

/**
 * One hop, marked on the spine by a tick and a caption. The caption itself is
 * built by the view: copy is localized and layout stays free of language.
 */
export type TierRow = {
  depth: number;
  /** Centre line of the row: the y every node in it sits on. */
  y: number;
  count: number;
  /** False for the parked row: nothing in it has a route from here. */
  reachable: boolean;
};

export type TopologyLayout = {
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  tiers: TierRow[];
  /** Where the hop spine runs, in the gutter left of the first column. */
  spineX: number;
  width: number;
  height: number;
};

/**
 * Every dimension a node shape is drawn from. The view derives glyph and plate
 * coordinates from these rather than repeating literals, so widening a card
 * moves its router mark with it instead of leaving it off-centre.
 */
export const NODE_METRICS = {
  relayWidth: 168,
  relayHeight: 56,
  relayRadius: 18,
  agentRadius: 26,
  /** The accent-filled tile that carries the router mark on a relay. */
  tile: 36,
  tileRadius: 11,
  /** Gap between the card edge and the tile. */
  tileInset: 12,
  /** Gap between the tile and the text column beside it. */
  textGap: 12,
  statusRadius: 5.5,
  statusInset: 15,
  badgeRadius: 8.5,
  /** Centre of an agent's name chip, below the disc. */
  chipDrop: 45,
  chipHeight: 20,
  /** Baseline of the meta line under a name. */
  agentMetaDrop: 62,
  /** Baseline of the work line, which hangs below each shape's furniture. */
  agentActivityDrop: 76,
  relayActivityDrop: 44,
  /** Offset of a relay's two text rows from the card's centre line. */
  relayNameDrop: -3,
  relayMetaDrop: 13,
} as const;

/* Tall enough that a hop's label lands clear of both the corner it turns and
   the arrowhead it ends in, and that its rail misses the meta line of the row
   above. Everything below that height puts two of the three on top of each
   other. */
const TIER_HEIGHT = 176;
/* Wide enough that two cards side by side leave a peer link real room to
   cross: at 244 the gap was 76px and every bow across it read as a wedge. */
const NODE_SPACING = 300;
/** Room on the left for the spine and its hop captions. */
const GUTTER = 124;
const MARGIN_RIGHT = 28;
/** How far outside the widest card a row-skipping link runs. */
const LANE_GAP = 34;
const MARGIN_TOP = 48;
/** Room under the deepest row for an agent's name chip and meta line. */
const BOTTOM_PAD = 82;

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

  const ordered = [...tiers.entries()].sort((a, b) => a[0] - b[0]);
  let widest = 1;
  let deepest = 0;
  for (const [depth, members] of ordered) {
    widest = Math.max(widest, members.length);
    deepest = Math.max(deepest, depth);
  }
  // A single column still has to hold a whole relay card, so the widest shape
  // is part of the width rather than something that hangs over the edge.
  const contentWidth = (widest - 1) * NODE_SPACING + NODE_METRICS.relayWidth;
  const width = GUTTER + contentWidth + MARGIN_RIGHT;

  // A route names the node it reaches, so a route minus its last hop names the
  // node one hop back. That is how a row finds the parent to line up under.
  const byRoute = new Map<string, string>();
  for (const [uuid, path] of paths) {
    if (path.length > 0) {
      byRoute.set(path.join("/"), uuid);
    }
  }

  const placed = new Map<string, PlacedNode>();
  const rows: TierRow[] = [];
  for (const [depth, members] of ordered) {
    // Rows are placed top down, so the tier above is already positioned and can
    // pull its children into line. Ordering a row by name alone crosses every
    // connector whose parent happens to sort the other way.
    members.sort((a, b) => {
      const ax = parentX(a, paths, byRoute, placed);
      const bx = parentX(b, paths, byRoute, placed);
      return ax - bx || a.name.localeCompare(b.name);
    });
    const rowWidth = (members.length - 1) * NODE_SPACING;
    const startX = GUTTER + (contentWidth - rowWidth) / 2;
    const y = MARGIN_TOP + depth * TIER_HEIGHT;
    members.forEach((n, i) => {
      placed.set(n.uuid, {
        ...n,
        depth,
        path: paths.get(n.uuid) || [],
        x: startX + i * NODE_SPACING,
        y,
      });
    });
    rows.push({
      depth,
      y,
      count: members.length,
      reachable:
        depth === 0 ||
        members.some((n) => (paths.get(n.uuid) || []).length > 0),
    });
  }

  // A route reaches exactly one node, so its joined path identifies that node.
  // isAlternate needs it to tell the link the route arrives on from a link that
  // merely shares its name.
  //
  // Only the root answers to the empty route. A node with no route has an empty
  // path too, and letting it into this map made it the owner of "" - which told
  // isAlternate that no first hop leaves the relay, and painted every one of
  // them as a way round.
  const uuidByRoute = new Map<string, string>();
  for (const n of placed.values()) {
    if (n.depth === 0 || n.path.length > 0) {
      uuidByRoute.set(n.path.join("/"), n.uuid);
    }
  }

  // Outside every card, so a link that skips a row never crosses one.
  const laneX =
    Math.max(...[...placed.values()].map((n) => n.x + halfWidth(n)), GUTTER) +
    LANE_GAP;

  const edges: PlacedEdge[] = [];
  for (const e of topology.edges || []) {
    const from = placed.get(e.from_uuid);
    const to = placed.get(e.to_uuid);
    if (!from || !to || from.uuid === to.uuid) {
      continue;
    }
    edges.push({
      id: `${from.uuid}>${to.uuid}:${e.name}`,
      from,
      to,
      name: e.name,
      alternate: isAlternate(topology, e, uuidByRoute),
      laneX,
    });
  }

  const height = MARGIN_TOP + deepest * TIER_HEIGHT + BOTTOM_PAD;
  // The lane only costs width when something actually runs in it.
  const skipsARow = edges.some(
    (e) => Math.abs(e.to.y - e.from.y) > TIER_HEIGHT * 1.5,
  );
  return {
    nodes: [...placed.values()].sort((a, b) => a.depth - b.depth || a.x - b.x),
    edges,
    tiers: rows,
    spineX: GUTTER - 28,
    width: skipsARow ? Math.max(width, laneX + LANE_GAP) : width,
    height,
  };
}

/** Where the node one hop back sits, or 0 when there is nothing above it. */
function parentX(
  node: TopologyNode,
  paths: Map<string, string[]>,
  byRoute: Map<string, string>,
  placed: Map<string, PlacedNode>,
): number {
  const path = paths.get(node.uuid) || [];
  if (path.length < 2) {
    return 0;
  }
  const parent = byRoute.get(path.slice(0, -1).join("/"));
  if (!parent) {
    return 0;
  }
  return placed.get(parent)?.x ?? 0;
}

export type Connector = {
  d: string;
  labelX: number;
  labelY: number;
  /** A peer link runs inside one tier; a hop crosses between two. */
  peer: boolean;
};

/**
 * Clear space left between a shape and the wire leaving it. Wide enough that
 * the head on a dialled link, which sits at the near end and points back into
 * the node, is not swallowed by that node's shadow plate.
 */
const EXIT_GAP = 10;
/** The arrowhead is 12 user units long, so a wire stops that short of a shape. */
const ARRIVE_GAP = 12;
/** Radius of an elbow corner where a hop turns onto its rail. */
const CORNER = 12;
/**
 * A peer link sags below its row. The floor keeps a short link visible, the
 * ceiling stops a long one swinging into the next tier, and the span cap beats
 * both: a sag deeper than the wire is long is the V every earlier draft drew.
 */
const PEER_SAG_MIN = 34;
const PEER_SAG_MAX = 78;
const PEER_SAG_OF_SPAN = 0.36;
const PEER_SAG_CAP_OF_SPAN = 0.6;
/**
 * How far the control points reach along the span. A fraction with no absolute
 * floor, so the two of them can never swap order and kink the curve.
 */
const PEER_BEND_OF_SPAN = 0.34;
/** Below this the two shapes are all but touching; a flat wire is the honest one. */
const PEER_MIN_SPAN = 8;

/**
 * Where a link is drawn, and where its label sits on it.
 *
 * Two idioms, kept apart so the picture keeps a grammar. A hop leaves the
 * bottom of one node, turns onto a rail shared by every node its parent feeds,
 * and arrives at the top of the next, so a fan-out reads as a bus rather than
 * as scattered wires. A link between peers on one row leaves a side and arrives
 * at the other's side, sagging under the row by an amount the span sets.
 *
 * Pure, so both idioms can be checked at their extremes without rendering.
 */
export function connectorFor(edge: PlacedEdge): Connector {
  const { from, to } = edge;
  if (Math.abs(to.y - from.y) < 1) {
    return peerLink(from, to);
  }
  // A hop between adjacent rows can turn in the gutter between them. A link
  // that skips a row cannot: its rail would land on that row's centre line and
  // run straight through the cards standing there, and since nodes paint over
  // edges the wire would read as two stubs entering a node it never touches.
  // Those are ring back edges, so they go round the outside instead.
  return Math.abs(to.y - from.y) > TIER_HEIGHT * 1.5
    ? bypassLink(from, to, edge.laneX)
    : hopLink(from, to);
}

/**
 * A link that skips a row: out of one side, along a lane clear of every node,
 * and into the side of the target. Drawn as a way round because that is what
 * it is.
 */
function bypassLink(
  from: PlacedNode,
  to: PlacedNode,
  laneX: number,
): Connector {
  const dir = laneX >= from.x ? 1 : -1;
  const x0 = from.x + dir * (halfWidth(from) + EXIT_GAP);
  const x1 = to.x + dir * (halfWidth(to) + ARRIVE_GAP);
  const vdir = to.y > from.y ? 1 : -1;
  const r = Math.min(
    CORNER,
    Math.abs(laneX - x0) / 2,
    Math.abs(laneX - x1) / 2,
    Math.abs(to.y - from.y) / 2,
  );
  return {
    d:
      `M${round(x0)} ${round(from.y)}` +
      ` L${round(laneX - dir * r)} ${round(from.y)}` +
      ` Q${round(laneX)} ${round(from.y)} ${round(laneX)} ${round(from.y + vdir * r)}` +
      ` L${round(laneX)} ${round(to.y - vdir * r)}` +
      ` Q${round(laneX)} ${round(to.y)} ${round(laneX - dir * r)} ${round(to.y)}` +
      ` L${round(x1)} ${round(to.y)}`,
    labelX: round(laneX),
    labelY: round((from.y + to.y) / 2),
    peer: false,
  };
}

function peerLink(from: PlacedNode, to: PlacedNode): Connector {
  const dir = to.x >= from.x ? 1 : -1;
  const x0 = from.x + dir * (halfWidth(from) + EXIT_GAP);
  const x1 = to.x - dir * (halfWidth(to) + ARRIVE_GAP);
  const span = Math.max(dir * (x1 - x0), 0);
  if (span < PEER_MIN_SPAN) {
    // The two shapes all but meet. A bow across nothing is a spike, and anchors
    // this close can cross, which would point the arrow back at its source, so
    // the wire becomes a stub centred in what gap there is.
    const mid = (from.x + to.x) / 2;
    const half = (dir * PEER_MIN_SPAN) / 2;
    return {
      d: `M${round(mid - half)} ${round(from.y)} L${round(mid + half)} ${round(to.y)}`,
      labelX: round(mid),
      labelY: round(from.y),
      peer: true,
    };
  }
  const sag = Math.min(
    PEER_SAG_MAX,
    Math.max(PEER_SAG_MIN, span * PEER_SAG_OF_SPAN),
    span * PEER_SAG_CAP_OF_SPAN,
  );
  const bend = span * PEER_BEND_OF_SPAN;
  const c1x = x0 + dir * bend;
  const c2x = x1 - dir * bend;
  const cy = from.y + sag;
  return {
    d:
      `M${round(x0)} ${round(from.y)}` +
      ` C${round(c1x)} ${round(cy)} ${round(c2x)} ${round(cy)}` +
      ` ${round(x1)} ${round(to.y)}`,
    labelX: round(cubicMid(x0, c1x, c2x, x1)),
    labelY: round(cubicMid(from.y, cy, cy, to.y)),
    peer: true,
  };
}

function hopLink(from: PlacedNode, to: PlacedNode): Connector {
  const vdir = to.y > from.y ? 1 : -1;
  const y0 = from.y + vdir * (halfHeight(from) + EXIT_GAP);
  const y1 = to.y - vdir * (halfHeight(to) + ARRIVE_GAP);
  // Halfway between the two rows, not between the two shapes: siblings of
  // different sizes then still turn on one rail instead of on four of them.
  const railY = (from.y + to.y) / 2;
  if (Math.abs(to.x - from.x) < 1) {
    return {
      d: `M${round(from.x)} ${round(y0)} L${round(from.x)} ${round(y1)}`,
      labelX: round(from.x),
      labelY: round((railY + y1) / 2),
      peer: false,
    };
  }
  const hdir = to.x > from.x ? 1 : -1;
  const r = Math.min(
    CORNER,
    Math.abs(to.x - from.x) / 2,
    Math.abs(railY - y0),
    Math.abs(y1 - railY),
  );
  return {
    d:
      `M${round(from.x)} ${round(y0)}` +
      ` L${round(from.x)} ${round(railY - vdir * r)}` +
      ` Q${round(from.x)} ${round(railY)} ${round(from.x + hdir * r)} ${round(railY)}` +
      ` L${round(to.x - hdir * r)} ${round(railY)}` +
      ` Q${round(to.x)} ${round(railY)} ${round(to.x)} ${round(railY + vdir * r)}` +
      ` L${round(to.x)} ${round(y1)}`,
    labelX: round(to.x),
    labelY: round((railY + y1) / 2),
    peer: false,
  };
}

function halfWidth(n: PlacedNode): number {
  return n.kind === "relay"
    ? NODE_METRICS.relayWidth / 2
    : NODE_METRICS.agentRadius;
}

function halfHeight(n: PlacedNode): number {
  return n.kind === "relay"
    ? NODE_METRICS.relayHeight / 2
    : NODE_METRICS.agentRadius;
}

/** The point halfway along a cubic, which is where a peer label sits. */
function cubicMid(a: number, b: number, c: number, d: number): number {
  return (a + 3 * b + 3 * c + d) / 8;
}

function round(v: number): number {
  return Math.round(v * 10) / 10;
}

/**
 * An edge is an alternate when it is not the last step of the target's chosen
 * route. The name alone does not settle that: an edge is named by whoever
 * registered the target, so two relays that both know a node call the link the
 * same thing, and matching on the name would paint a ring's back edge as the
 * route in use. The edge also has to leave the node the route arrives from.
 */
function isAlternate(
  topology: SwarmTopology,
  edge: TopologyEdge,
  uuidByRoute: Map<string, string>,
): boolean {
  const route = (topology.routes || {})[edge.to_uuid];
  if (!route || route.path.length === 0) {
    return false;
  }
  if (route.path[route.path.length - 1] !== edge.name) {
    return true;
  }
  const parent = uuidByRoute.get(route.path.slice(0, -1).join("/"));
  return parent !== undefined && parent !== edge.from_uuid;
}

/**
 * The links a request would follow from the attached relay down to one node.
 *
 * A route is a list of hop names, and the edge each hop arrives on is the one
 * the relay chose - never a way round, which shares the name but leaves a
 * different node. Pure, so the highlight can be checked without rendering.
 */
export function routeEdgeIds(
  layout: TopologyLayout,
  route: string,
): Set<string> {
  const out = new Set<string>();
  if (!route) {
    return out;
  }
  const target = layout.nodes.find(
    (n) => n.path.length > 0 && n.path.join("/") === route,
  );
  if (!target) {
    return out;
  }
  for (let hop = 1; hop <= target.path.length; hop += 1) {
    const childKey = target.path.slice(0, hop).join("/");
    const parentKey = target.path.slice(0, hop - 1).join("/");
    const edge = layout.edges.find(
      (e) =>
        !e.alternate &&
        e.to.path.length > 0 &&
        e.to.path.join("/") === childKey &&
        (parentKey === ""
          ? e.from.depth === 0
          : e.from.path.length > 0 && e.from.path.join("/") === parentKey),
    );
    if (edge) {
      out.add(edge.id);
    }
  }
  return out;
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
