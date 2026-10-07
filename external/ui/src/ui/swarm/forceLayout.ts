import {
  CLIENT_UUID,
  NODE_METRICS,
  layoutTopology,
  nodeHalfHeight,
  nodeHalfWidth,
  type LayoutOptions,
  type PlacedNode,
  type TopologyLayout,
} from "./layout";
import type { SwarmTopology } from "./types";

/* The distances of the fan, in pixels. A leaf hangs just below its relay; a
   chained relay without children of its own sits a touch deeper, and one
   carrying a leaf ring of its own sits deep enough for that ring to keep its
   breathing room. */
const LEAF_RING = 110;
const RELAY_RING_EMPTY = 175;
const RELAY_RING_FULL = 320;
const CLIENT_DROP = 190;
const FAN_MAX_STEP = 55;
/* Children fill from straight down outwards and never rise past the horizon:
   above it is where the parent's own wire - and the client - already is. */
const LOWER_SPAN = 170;
const LEAF_CHORD = (2 * NODE_METRICS.agentRadius + 24) * 1.45;
const RELAY_CHORD = (2 * NODE_METRICS.relayRadius + 24) * 1.45;
const STRANDED_DROP = 160;
const STRANDED_STEP = 96;
const MARGIN = 48;
const BOTTOM_MARGIN = 96;
const GAP = 24.2;

const deg2rad = (deg: number) => (deg * Math.PI) / 180;

/**
 * A rooted graph layout, without timers or random state. The client hangs at
 * the top, the relay it talks to under it, and each relay's own fleet fans
 * out on the arc underneath - leaves close on the lower half, chained relays
 * deeper like pendants, so every chain reads as a cord walking down the
 * page. Positions come from the name-ordered child lists alone, so a poll's
 * enumeration order never moves the picture.
 */
export function layoutTopologyGraph(
  topology: SwarmTopology,
  opts: LayoutOptions = {},
): TopologyLayout {
  const tree = layoutTopology(topology, opts);

  // Children of each parent over the route edges only, in a stable order.
  const children = new Map<string, PlacedNode[]>();
  for (const edge of tree.edges) {
    if (edge.alternate || edge.from.uuid === edge.to.uuid) continue;
    const list = children.get(edge.from.uuid);
    if (list) list.push(edge.to);
    else children.set(edge.from.uuid, [edge.to]);
  }
  for (const list of children.values()) {
    list.sort((a, b) => compare(a.name, b.name) || compare(a.uuid, b.uuid));
  }

  const root = tree.nodes.find((n) => n.uuid === topology.root.uuid)!;
  root.x = 0;
  root.y = 0;

  const reached = new Set<string>([root.uuid]);
  const visit = (relay: PlacedNode): void => {
    // `reached` doubles as the cycle guard: a child placed once is never
    // walked again, so a malformed topology cannot recurse forever.
    const kids = (children.get(relay.uuid) ?? []).filter(
      (kid) => !reached.has(kid.uuid),
    );
    const leaves = kids.filter((kid) => kid.kind !== "relay");
    const relays = kids.filter((kid) => kid.kind === "relay");
    // Every child takes an angle slot of its own on the lower arc - relays at
    // the ends, leaf agents under the parent - so a leaf can never sit on
    // the wire of a chained relay it would cover.
    const half = Math.ceil(relays.length / 2);
    const ordered =
      relays.length > 0 && leaves.length > 0
        ? [...relays.slice(0, half), ...leaves, ...relays.slice(half)]
        : kids;
    const total = ordered.length;
    // The arc never passes the horizon; when the children get tight on it
    // the ring itself grows rather than letting two discs touch.
    const step =
      total > 1 ? Math.min(FAN_MAX_STEP, LOWER_SPAN / (total - 1)) : 0;
    const stepRad = deg2rad(step);
    const leafRing =
      step > 0
        ? Math.max(LEAF_RING, LEAF_CHORD / (2 * Math.sin(stepRad / 2)))
        : LEAF_RING;
    const relayChordRing =
      step > 0 ? RELAY_CHORD / (2 * Math.sin(stepRad / 2)) : 0;
    ordered.forEach((child, i) => {
      const deg = deg2rad(90 + (i - (total - 1) / 2) * step);
      const dist =
        child.kind === "relay"
          ? // Two relay children hang like two pendants a small step apart;
            // one that still routes onward needs room for its own ring.
            Math.max(
              (children.get(child.uuid) ?? []).length > 0
                ? RELAY_RING_FULL
                : RELAY_RING_EMPTY,
              relayChordRing,
            )
          : leafRing;
      child.x = relay.x + Math.cos(deg) * dist;
      child.y = relay.y + Math.sin(deg) * dist;
      reached.add(child.uuid);
      if (child.kind === "relay") visit(child);
    });
  };
  visit(root);

  // The machine the page runs on starts the picture at the top.
  const client = opts.client
    ? tree.nodes.find((n) => n.uuid === CLIENT_UUID)
    : undefined;
  if (client) {
    client.x = 0;
    client.y = -CLIENT_DROP;
  }

  // Nodes the route tree never reached - offline or otherwise stranded -
  // take a row of their own below the map, in name order.
  const stranded = tree.nodes
    .filter((n) => !reached.has(n.uuid) && n.uuid !== CLIENT_UUID)
    .sort((a, b) => compare(a.name, b.name) || compare(a.uuid, b.uuid));
  if (stranded.length > 0) {
    const y =
      Math.max(...tree.nodes.map((n) => n.y), ...(client ? [client.y] : [])) +
      STRANDED_DROP;
    stranded.forEach((node, i) => {
      node.x = root.x + (i - (stranded.length - 1) / 2) * STRANDED_STEP;
      node.y = y;
    });
  }

  const edges = [...tree.edges].sort((a, b) => compare(a.id, b.id));
  const minX = Math.min(...tree.nodes.map((n) => n.x - nodeHalfWidth(n)));
  const minY = Math.min(...tree.nodes.map((n) => n.y - nodeHalfHeight(n)));
  const maxX = Math.max(...tree.nodes.map((n) => n.x + nodeHalfWidth(n)));
  const maxY = Math.max(...tree.nodes.map((n) => n.y + nodeHalfHeight(n)));
  // The root centres the picture horizontally, whatever the fan looks like:
  // the canvas is exactly two offsets wide, so the centre is on the grid.
  const half = Math.max(-minX, maxX);
  const offX = round(half + MARGIN);
  const offY = MARGIN - minY;
  const nodes = tree.nodes.map((node) => ({
    ...node,
    x: round(node.x + offX),
    y: round(node.y + offY),
  }));
  const placed = new Map(nodes.map((node) => [node.uuid, node]));
  const right = maxX + offX;
  const bottom = maxY + offY;
  return {
    nodes,
    edges: edges.map((edge) => ({
      ...edge,
      from: placed.get(edge.from.uuid)!,
      to: placed.get(edge.to.uuid)!,
      laneX: round(right + GAP),
    })),
    tiers: [],
    spineX: MARGIN / 2,
    width: round(2 * offX),
    height: round(bottom + BOTTOM_MARGIN),
  };
}

function compare(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function round(v: number): number {
  return Math.round(v * 10) / 10;
}
