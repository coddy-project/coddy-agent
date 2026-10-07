import { describe, expect, it } from "vitest";
import {
  NODE_METRICS,
  connectorFor,
  graphConnectorFor,
  layoutTopology,
  nodeHalfHeight,
  nodeHalfWidth,
  routeEdgeIds,
  topologySummary,
  type PlacedEdge,
  type PlacedNode,
  type TopologyLayout,
} from "./layout";
import type { SwarmTopology } from "./types";

/** Finds a placed node by name, failing loudly when the layout dropped it. */
function placed(layout: TopologyLayout, name: string) {
  const found = layout.nodes.find((n) => n.name === name);
  if (!found) {
    throw new Error(`layout has no node named ${name}`);
  }
  return found;
}

const chain: SwarmTopology = {
  root: { uuid: "root", name: "outer", kind: "relay", online: true },
  nodes: [
    { uuid: "mid", name: "middle", kind: "relay", online: true },
    { uuid: "a7", name: "agent7", kind: "agent", online: true },
  ],
  edges: [
    { from_uuid: "root", to_uuid: "mid", name: "middle" },
    { from_uuid: "mid", to_uuid: "a7", name: "agent7" },
  ],
  routes: {
    mid: { path: ["middle"] },
    a7: { path: ["middle", "agent7"] },
  },
  warnings: [],
};

describe("layoutTopology", () => {
  it("puts the relay at the top and each hop a tier lower", () => {
    const layout = layoutTopology(chain);
    expect(placed(layout, "outer").depth).toBe(0);
    expect(placed(layout, "middle").depth).toBe(1);
    expect(placed(layout, "agent7").depth).toBe(2);
    expect(placed(layout, "outer").y).toBeLessThan(placed(layout, "middle").y);
    expect(placed(layout, "middle").y).toBeLessThan(placed(layout, "agent7").y);
  });

  it("carries the route each node is reached by", () => {
    const layout = layoutTopology(chain);
    const agent = layout.nodes.find((n) => n.name === "agent7");
    expect(agent?.path).toEqual(["middle", "agent7"]);
  });

  it("places every node and edge", () => {
    const layout = layoutTopology(chain);
    expect(layout.nodes).toHaveLength(3);
    expect(layout.edges).toHaveLength(2);
    expect(layout.width).toBeGreaterThan(0);
    expect(layout.height).toBeGreaterThan(0);
  });

  // In a ring the same relay is both one hop and three hops away. Drawing it at
  // three would say the long way is the only way.
  it("draws a ring node at the depth of its shortest route", () => {
    const ring: SwarmTopology = {
      root: { uuid: "root", name: "outer", kind: "relay", online: true },
      nodes: [
        { uuid: "mid", name: "middle", kind: "relay", online: true },
        { uuid: "r3", name: "relay3", kind: "relay", online: true },
        { uuid: "a7", name: "agent7", kind: "agent", online: true },
      ],
      edges: [
        { from_uuid: "root", to_uuid: "mid", name: "middle" },
        { from_uuid: "root", to_uuid: "r3", name: "shortcut" },
        { from_uuid: "mid", to_uuid: "r3", name: "relay3" },
        { from_uuid: "r3", to_uuid: "a7", name: "agent7" },
      ],
      routes: {
        mid: { path: ["middle"] },
        r3: { path: ["shortcut"], alternates: [["middle", "relay3"]] },
        a7: { path: ["shortcut", "agent7"] },
      },
      warnings: [],
    };
    const layout = layoutTopology(ring);
    expect(placed(layout, "relay3").depth).toBe(1);
    expect(placed(layout, "agent7").depth).toBe(2);

    // The way round is still drawn, marked as the road not taken.
    const viaMiddle = layout.edges.find(
      (e) => e.from.name === "middle" && e.to.name === "relay3",
    );
    const direct = layout.edges.find(
      (e) => e.from.name === "outer" && e.to.name === "relay3",
    );
    expect(viaMiddle?.alternate).toBe(true);
    expect(direct?.alternate).toBe(false);
  });

  it("parks an unreachable node past the deepest tier instead of hiding it", () => {
    const stranded: SwarmTopology = {
      ...chain,
      nodes: [
        ...chain.nodes,
        { uuid: "lost", name: "lost", kind: "agent", online: false },
      ],
    };
    const layout = layoutTopology(stranded);
    const lost = placed(layout, "lost");
    expect(lost.depth).toBeGreaterThan(2);
    expect(lost.path).toEqual([]);
  });

  it("survives a relay with nothing registered", () => {
    const empty: SwarmTopology = {
      root: { uuid: "root", name: "lonely", kind: "relay", online: true },
      nodes: [],
      edges: [],
      routes: {},
      warnings: [],
    };
    const layout = layoutTopology(empty);
    expect(layout.nodes).toHaveLength(1);
    expect(layout.edges).toHaveLength(0);
  });

  it("ignores an edge pointing at a node it does not know", () => {
    const dangling: SwarmTopology = {
      ...chain,
      edges: [
        ...chain.edges,
        { from_uuid: "root", to_uuid: "ghost", name: "?" },
      ],
    };
    expect(layoutTopology(dangling).edges).toHaveLength(2);
  });
});

/** Every coordinate pair in a path, in order, whatever commands carried them. */
function points(d: string): { x: number; y: number }[] {
  const nums = (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);
  const out: { x: number; y: number }[] = [];
  for (let i = 0; i + 1 < nums.length; i += 2) {
    out.push({ x: nums[i] as number, y: nums[i + 1] as number });
  }
  return out;
}

function peer(name: string, x: number, kind: "relay" | "agent"): PlacedNode {
  return {
    uuid: name,
    name,
    kind,
    online: true,
    x,
    y: 200,
    depth: 1,
    path: [name],
  };
}

describe("connectorFor, peer links", () => {
  const relayHalf = NODE_METRICS.relayRadius;

  // The span every earlier draft broke on: adjacent cards, a hand's width of
  // clear space, and a bow that has to read as a sag rather than as a V.
  it("sags gently between two neighbours a row apart", () => {
    const from = peer("left", 300, "relay");
    const to = peer("right", 300 + 244, "relay");
    const c = connectorFor({
      from,
      to,
      name: "right",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    const p = points(c.d);
    const start = p[0] as { x: number; y: number };
    const end = p[p.length - 1] as { x: number; y: number };

    // It leaves a side and arrives at a side, not the top or the bottom.
    expect(start.y).toBe(from.y);
    expect(end.y).toBe(to.y);
    expect(start.x).toBeGreaterThan(from.x + relayHalf);
    expect(end.x).toBeLessThan(to.x - relayHalf);

    // The control points stay in order, which is the whole fix: a floor on the
    // horizontal reach used to swap them and kink the curve into a zigzag.
    const xs = p.map((q) => q.x);
    expect([...xs].sort((a, b) => a - b)).toEqual(xs);

    // A sag, measured against the distance it covers.
    const span = end.x - start.x;
    expect(span).toBeGreaterThan(50);
    expect(c.labelY - from.y).toBeGreaterThan(8);
    expect(c.labelY - from.y).toBeLessThan(span * 0.5);
    expect(c.labelX).toBeGreaterThan(start.x);
    expect(c.labelX).toBeLessThan(end.x);
  });

  it("clamps the sag on a link that crosses the whole picture", () => {
    const from = peer("left", 200, "relay");
    const to = peer("right", 700, "relay");
    const c = connectorFor({
      from,
      to,
      name: "right",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    const p = points(c.d);
    const xs = p.map((q) => q.x);
    expect([...xs].sort((a, b) => a - b)).toEqual(xs);

    // Deeper than the short link, and still nowhere near the next tier down.
    expect(c.labelY - from.y).toBeGreaterThan(40);
    expect(c.labelY - from.y).toBeLessThan(70);
    // The label belongs on the wire, clear of both cards it runs between.
    const start = points(c.d)[0] as { x: number };
    const end = points(c.d)[points(c.d).length - 1] as { x: number };
    expect(c.labelX).toBeGreaterThan(start.x);
    expect(c.labelX).toBeLessThan(end.x);
    expect(c.labelX).toBeGreaterThan(from.x + relayHalf);
    expect(c.labelX).toBeLessThan(to.x - relayHalf);
    // And on the sag, not on the row the two nodes stand on.
    expect(c.labelY).toBeGreaterThan(from.y + 20);
  });

  it("keeps a stub pointing forward when two shapes all but touch", () => {
    const from = peer("left", 300, "relay");
    const to = peer("right", 360, "relay");
    const c = connectorFor({
      from,
      to,
      name: "right",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    const p = points(c.d);
    const start = p[0] as { x: number; y: number };
    const end = p[p.length - 1] as { x: number; y: number };
    expect(end.x).toBeGreaterThan(start.x);
    expect(start.y).toBe(from.y);
    expect(end.y).toBe(to.y);
  });

  it("mirrors itself on a link that runs right to left", () => {
    const from = peer("right", 700, "relay");
    const to = peer("left", 200, "relay");
    const c = connectorFor({
      from,
      to,
      name: "left",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    const xs = points(c.d).map((q) => q.x);
    expect([...xs].sort((a, b) => b - a)).toEqual(xs);
    expect(c.labelY).toBeGreaterThan(from.y);
  });
});

describe("connectorFor, hops", () => {
  it("turns every child of one parent onto the same rail", () => {
    const parent: PlacedNode = {
      uuid: "p",
      name: "p",
      kind: "relay",
      online: true,
      x: 400,
      y: 48,
      depth: 0,
      path: [],
    };
    const kid = (name: string, x: number, kind: "relay" | "agent") => ({
      ...peer(name, x, kind),
      y: 198,
    });
    const a = connectorFor({
      from: parent,
      to: kid("a", 300, "agent"),
      name: "a",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    const b = connectorFor({
      from: parent,
      to: kid("b", 540, "relay"),
      name: "b",
      id: "probe",
      alternate: false,
      laneX: 900,
    });
    // The horizontal run IS the rail, so read its y rather than the point
    // before the corner: that one is railY minus a radius, and two children
    // whose radii happened to clamp alike would agree even off separate rails.
    const railOf = (d: string) => {
      const m = /L(-?[\d.]+) (-?[\d.]+) Q/.exec(d.slice(d.indexOf("Q")));
      return m ? Number(m[2]) : NaN;
    };
    const railA = railOf(a.d);
    const railB = railOf(b.d);
    expect(Number.isNaN(railA)).toBe(false);
    expect(railA).toBe(railB);
    // And it really is halfway between the rows, not between the shapes: a
    // disc and a card have different heights and would otherwise part.
    expect(railA).toBe((parent.y + 198) / 2);
    expect(a.peer).toBe(false);
  });
});

describe("layoutTopology row order", () => {
  // Sorting a row by name alone crosses every connector whose parent happens to
  // sort the other way, and a crossing reads as a link that is not there.
  it("lines a row up under the nodes one hop back", () => {
    const crossing: SwarmTopology = {
      root: { uuid: "root", name: "root", kind: "relay", online: true },
      nodes: [
        { uuid: "al", name: "alpha", kind: "relay", online: true },
        { uuid: "be", name: "beta", kind: "relay", online: true },
        { uuid: "zu", name: "zulu", kind: "agent", online: true },
        { uuid: "aa", name: "aaa", kind: "agent", online: true },
      ],
      edges: [],
      routes: {
        al: { path: ["alpha"] },
        be: { path: ["beta"] },
        zu: { path: ["alpha", "zulu"] },
        aa: { path: ["beta", "aaa"] },
      },
      warnings: [],
    };
    const layout = layoutTopology(crossing);
    expect(placed(layout, "alpha").x).toBeLessThan(placed(layout, "beta").x);
    expect(placed(layout, "zulu").x).toBeLessThan(placed(layout, "aaa").x);
  });
});

describe("topologySummary", () => {
  it("counts the relay you are attached to among the relays", () => {
    expect(topologySummary(chain)).toEqual({
      relays: 2,
      agents: 1,
      offline: 0,
    });
  });

  it("counts what is offline", () => {
    const withDead: SwarmTopology = {
      ...chain,
      nodes: [
        ...chain.nodes,
        { uuid: "dead", name: "dead", kind: "agent", online: false },
      ],
    };
    expect(topologySummary(withDead).offline).toBe(1);
  });
});

// A ring closes back on a relay several rows up. Both of these went wrong in
// the first cut of the redesign, and both are invisible without a fixture that
// actually has a ring in it.
describe("a ring's back edge", () => {
  // root -> r1 -> r2 -> r3, and r3 also knows r1. ComputeRoutes reaches r1 in
  // one hop, so the back edge climbs two rows.
  const ring = {
    root: { uuid: "u0", name: "root", kind: "relay" as const, online: true },
    nodes: [
      { uuid: "u1", name: "r1", kind: "relay" as const, online: true },
      { uuid: "u2", name: "r2", kind: "relay" as const, online: true },
      { uuid: "u3", name: "r3", kind: "relay" as const, online: true },
    ],
    edges: [
      { from_uuid: "u0", to_uuid: "u1", name: "r1" },
      { from_uuid: "u1", to_uuid: "u2", name: "r2" },
      { from_uuid: "u2", to_uuid: "u3", name: "r3" },
      { from_uuid: "u3", to_uuid: "u1", name: "r1" },
    ],
    routes: {
      u1: { path: ["r1"] },
      u2: { path: ["r1", "r2"] },
      u3: { path: ["r1", "r2", "r3"] },
    },
    warnings: [],
  };

  const backEdge = () => {
    const layout = layoutTopology(ring);
    const e = layout.edges.find(
      (x) => x.from.name === "r3" && x.to.name === "r1",
    );
    if (!e) {
      throw new Error("no back edge in the layout");
    }
    return { layout, edge: e };
  };

  it("is a way round, not the route in use", () => {
    // Both links into r1 are named "r1", because an edge carries the name the
    // node that registered it chose. Only the one from root is the route.
    const { layout, edge } = backEdge();
    expect(edge.alternate).toBe(true);
    const real = layout.edges.find(
      (x) => x.from.name === "root" && x.to.name === "r1",
    );
    expect(real?.alternate).toBe(false);
  });

  it("goes round the outside instead of through the row it skips", () => {
    const { layout, edge } = backEdge();
    const c = connectorFor(edge);
    const r2 = layout.nodes.find((n) => n.name === "r2");
    expect(r2).toBeTruthy();

    // Every point of the wire is clear of the card it would otherwise cross.
    const xs = points(c.d).map((p) => p.x);
    const clearOf = Math.abs(r2!.x) + NODE_METRICS.relayRadius;
    expect(Math.max(...xs)).toBeGreaterThan(clearOf);
    // And the canvas grew to hold the lane rather than clipping it.
    expect(layout.width).toBeGreaterThan(Math.max(...xs));
  });
});

describe("routeEdgeIds", () => {
  it("names every hop from the relay down to the node", () => {
    const layout = layoutTopology(chain);
    const ids = routeEdgeIds(layout, "middle/agent7");
    const named = layout.edges
      .filter((e) => ids.has(e.id))
      .map((e) => `${e.from.name}->${e.to.name}`)
      .sort();
    expect(named).toEqual(["middle->agent7", "outer->middle"]);
  });

  it("stops at the first hop for a node one hop away", () => {
    const layout = layoutTopology(chain);
    expect(routeEdgeIds(layout, "middle").size).toBe(1);
  });

  it("has nothing to light for the relay itself or an unknown route", () => {
    const layout = layoutTopology(chain);
    expect(routeEdgeIds(layout, "").size).toBe(0);
    expect(routeEdgeIds(layout, "nowhere").size).toBe(0);
  });

  // The back edge shares its name with the route's own link, so matching on the
  // name alone would light the long way round instead of the way in use.
  it("takes the route in use, never a ring's way round", () => {
    const ring: SwarmTopology = {
      root: { uuid: "u0", name: "root", kind: "relay", online: true },
      nodes: [
        { uuid: "u1", name: "r1", kind: "relay", online: true },
        { uuid: "u2", name: "r2", kind: "relay", online: true },
      ],
      edges: [
        { from_uuid: "u0", to_uuid: "u1", name: "r1" },
        { from_uuid: "u1", to_uuid: "u2", name: "r2" },
        { from_uuid: "u2", to_uuid: "u1", name: "r1" },
      ],
      routes: {
        u1: { path: ["r1"] },
        u2: { path: ["r1", "r2"] },
      },
      warnings: [],
    };
    const layout = layoutTopology(ring);
    const ids = routeEdgeIds(layout, "r1");
    expect(ids.size).toBe(1);
    const only = layout.edges.find((e) => ids.has(e.id));
    expect(only?.from.name).toBe("root");
  });
});

// A node with no route has an empty path, exactly like the root. When it was
// allowed to claim that key, every first hop out of the relay was drawn as a
// way round and the live path had nothing to light.
describe("a node with no route", () => {
  const stranded: SwarmTopology = {
    root: { uuid: "u0", name: "root", kind: "relay", online: true },
    nodes: [
      { uuid: "u1", name: "r1", kind: "relay", online: true },
      { uuid: "u9", name: "orphan", kind: "agent", online: false },
    ],
    edges: [{ from_uuid: "u0", to_uuid: "u1", name: "r1" }],
    routes: { u1: { path: ["r1"] } },
    warnings: [],
  };

  it("leaves the first hop as the route in use", () => {
    const layout = layoutTopology(stranded);
    expect(layout.edges[0]?.alternate).toBe(false);
    expect(routeEdgeIds(layout, "r1").size).toBe(1);
  });

  it("is still placed, past the deepest reachable tier", () => {
    const layout = layoutTopology(stranded);
    const orphan = placed(layout, "orphan");
    expect(orphan.path).toEqual([]);
    expect(orphan.depth).toBeGreaterThan(placed(layout, "r1").depth);
  });
});

// The machine the page runs on is where the connection starts: drawn above the
// relay, one tier up, wired to it (issue #401). Everything else moves down one
// tier and keeps its shape.
describe("layoutTopology with the local machine", () => {
  it("puts the local machine above the relay, wired to it, and moves the rest down a tier", () => {
    const plain = layoutTopology(chain);
    const withClient = layoutTopology(chain, { client: { name: "Local" } });
    const client = placed(withClient, "Local");
    const relay = placed(withClient, "outer");
    expect(client.kind).toBe("client");
    expect(client.depth).toBe(-1);
    expect(client.x).toBe(relay.x);
    expect(client.y).toBeLessThan(relay.y);
    for (const name of ["outer", "middle", "agent7"]) {
      expect(placed(withClient, name).y - placed(plain, name).y).toBe(
        relay.y - placed(plain, "outer").y,
      );
      expect(placed(withClient, name).depth).toBe(placed(plain, name).depth);
    }
    const wire = withClient.edges.find((e) => e.from.uuid === client.uuid);
    expect(wire?.to.uuid).toBe(relay.uuid);
    expect(withClient.tiers[0]).toMatchObject({ depth: -1, count: 1 });
    expect(withClient.height - plain.height).toBe(
      relay.y - placed(plain, "outer").y,
    );
  });

  // The live route runs from where the page is to where the app is.
  it("starts the route to the node the app is on at the local machine", () => {
    const layout = layoutTopology(chain, { client: { name: "Local" } });
    const client = placed(layout, "Local");
    const wire = layout.edges.find((e) => e.from.uuid === client.uuid);
    const route = routeEdgeIds(layout, "middle/agent7");
    expect(route.has(wire!.id)).toBe(true);
    expect(route.size).toBe(3);
  });
});

describe("connectorFor, the local machine", () => {
  // The local machine is a disc with its name on a chip under it, and the only
  // disc with a hop below: its wire starts under the chip, and close under it,
  // since no line of words hangs there any more.
  it("leaves the local machine just below its name", () => {
    const layout = layoutTopology(chain, { client: { name: "Local" } });
    const client = placed(layout, "Local");
    const wire = layout.edges.find((e) => e.from.uuid === client.uuid)!;
    const start = Number(/^M[\d.]+ ([\d.]+)/.exec(connectorFor(wire).d)?.[1]);
    const chipBottom =
      client.y + NODE_METRICS.chipDrop + NODE_METRICS.chipHeight / 2;
    // The same clear space a wire keeps from any shape it leaves.
    expect(start - chipBottom).toBe(10);
  });
});

describe("graphConnectorFor", () => {
  function relay(uuid: string, x: number, y: number): PlacedNode {
    return {
      uuid,
      name: uuid,
      kind: "relay",
      online: true,
      depth: 0,
      path: [],
      x,
      y,
    };
  }

  function edge(
    from: PlacedNode,
    to: PlacedNode,
    alternate = false,
  ): PlacedEdge {
    return {
      id: `${from.uuid}>${to.uuid}:${to.name}`,
      from,
      to,
      name: to.name,
      alternate,
      laneX: 0,
    };
  }

  /** Every number an SVG path string carries, in order. */
  function pathPoints(d: string): { x: number; y: number }[] {
    const nums = d.match(/-?\d+(?:\.\d+)?/g)!.map(Number);
    const out: { x: number; y: number }[] = [];
    for (let i = 0; i + 1 < nums.length; i += 2) {
      out.push({ x: nums[i]!, y: nums[i + 1]! });
    }
    return out;
  }

  it("draws a downward edge as a cubic leaving the source's rim toward the child", () => {
    const from = relay("parent", 400, 120);
    const to = relay("child", 520, 380);
    const conn = graphConnectorFor(edge(from, to));
    expect(conn.d).toMatch(/ C/);
    const points = pathPoints(conn.d);
    const start = points[0]!;
    const end = points[points.length - 1]!;
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const dist = Math.hypot(dx, dy);
    // The exit sits on the source's rim in the direction of the child and the
    // entry on the target's rim facing back - whatever the direction is.
    expect(start.x).toBeCloseTo(
      from.x + (dx / dist) * (nodeHalfWidth(from) + 10),
      0,
    );
    expect(start.y).toBeCloseTo(
      from.y + (dy / dist) * (nodeHalfHeight(from) + 10),
      0,
    );
    expect(end.x).toBeCloseTo(to.x - (dx / dist) * (nodeHalfWidth(to) + 10), 0);
    expect(end.y).toBeCloseTo(
      to.y - (dy / dist) * (nodeHalfHeight(to) + 10),
      0,
    );
    // The label sits at the cubic's midpoint, between the two endpoints.
    expect(conn.labelY).toBeGreaterThan(start.y);
    expect(conn.labelY).toBeLessThan(end.y);
    expect(conn.labelX).toBeGreaterThanOrEqual(Math.min(start.x, end.x) - 60);
    expect(conn.labelX).toBeLessThanOrEqual(Math.max(start.x, end.x) + 60);
    expect(conn.peer).toBe(false);
  });

  it("fans a parent's wires out of the rim point facing each child", () => {
    const from = relay("parent", 400, 120);
    const leftTo = relay("left", 120, 380);
    const midTo = relay("mid", 400, 380);
    const rightTo = relay("right", 760, 380);
    const left = graphConnectorFor(edge(from, leftTo));
    const mid = graphConnectorFor(edge(from, midTo));
    const right = graphConnectorFor(edge(from, rightTo));
    const exitX = (d: string, to: PlacedNode) => {
      const dist = Math.hypot(to.x - from.x, to.y - from.y);
      return from.x + ((to.x - from.x) / dist) * (nodeHalfWidth(from) + 10);
    };
    // Each wire leaves at the rim point looking at its child: left, centre,
    // and right read as a fan, not one shared stub.
    expect(pathPoints(left.d)[0]!.x).toBeCloseTo(exitX(left.d, leftTo), 1);
    expect(pathPoints(mid.d)[0]!.x).toBeCloseTo(from.x, 1);
    expect(pathPoints(right.d)[0]!.x).toBeCloseTo(exitX(right.d, rightTo), 1);
    expect(pathPoints(left.d)[0]!.x).toBeLessThan(from.x);
    expect(pathPoints(right.d)[0]!.x).toBeGreaterThan(from.x);
  });

  it("keeps an alternate edge a distinct cubic over the same endpoints", () => {
    const from = relay("parent", 400, 120);
    const to = relay("child", 520, 380);
    const primary = graphConnectorFor(edge(from, to));
    const alternate = graphConnectorFor(edge(from, to, true));
    expect(alternate.d).toMatch(/ C/);
    expect(alternate.d).not.toBe(primary.d);
    // Same anchors, different curve: the two wires never paint over each other.
    const a = pathPoints(alternate.d);
    const p = pathPoints(primary.d);
    expect(a[0]).toEqual(p[0]);
    expect(a[a.length - 1]).toEqual(p[p.length - 1]);
    expect(Number.isFinite(alternate.labelX)).toBe(true);
    expect(Number.isFinite(alternate.labelY)).toBe(true);
  });

  it("loops a backward edge rim-to-rim, finite and clear of both shapes", () => {
    const from = relay("low", 400, 380);
    const to = relay("high", 520, 120);
    const conn = graphConnectorFor(edge(from, to));
    expect(conn.d).toMatch(/ C/);
    const points = pathPoints(conn.d);
    for (const p of points) {
      expect(Number.isFinite(p.x)).toBe(true);
      expect(Number.isFinite(p.y)).toBe(true);
    }
    const start = points[0]!;
    const end = points[points.length - 1]!;
    // The wire leaves the source's rim toward the target and reaches the
    // target's rim facing back, outside both shapes.
    expect(Math.hypot(start.x - from.x, start.y - from.y)).toBeCloseTo(
      nodeHalfWidth(from) + 10,
      0,
    );
    expect(Math.hypot(end.x - to.x, end.y - to.y)).toBeCloseTo(
      nodeHalfWidth(to) + 10,
      0,
    );
    expect(Number.isFinite(conn.labelX)).toBe(true);
    expect(Number.isFinite(conn.labelY)).toBe(true);
  });

  it("bows a same-level edge to the side instead of kinking", () => {
    const from = relay("west", 300, 200);
    const to = relay("east", 620, 200);
    const conn = graphConnectorFor(edge(from, to));
    expect(conn.d).toMatch(/ C/);
    for (const p of pathPoints(conn.d)) {
      expect(Number.isFinite(p.x)).toBe(true);
      expect(Number.isFinite(p.y)).toBe(true);
    }
    expect(Number.isFinite(conn.labelX)).toBe(true);
    expect(Number.isFinite(conn.labelY)).toBe(true);
  });
});
