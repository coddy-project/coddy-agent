import { describe, expect, it } from "vitest";
import {
  NODE_METRICS,
  connectorFor,
  layoutTopology,
  topologySummary,
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
  const relayHalf = NODE_METRICS.relayWidth / 2;

  // The span every earlier draft broke on: adjacent cards, a hand's width of
  // clear space, and a bow that has to read as a sag rather than as a V.
  it("sags gently between two neighbours a row apart", () => {
    const from = peer("left", 300, "relay");
    const to = peer("right", 300 + 244, "relay");
    const c = connectorFor({ from, to, name: "right", alternate: false });
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
    const c = connectorFor({ from, to, name: "right", alternate: false });
    const p = points(c.d);
    const xs = p.map((q) => q.x);
    expect([...xs].sort((a, b) => a - b)).toEqual(xs);

    // Deeper than the short link, and still nowhere near the next tier down.
    expect(c.labelY - from.y).toBeGreaterThan(40);
    expect(c.labelY - from.y).toBeLessThan(70);
    // Halfway along the wire, which is a few units left of halfway between the
    // two centres: the arrowhead eats more room at the far end than the gap
    // the wire leaves behind it.
    const start = points(c.d)[0] as { x: number };
    const end = points(c.d)[points(c.d).length - 1] as { x: number };
    expect(c.labelX).toBeCloseTo((start.x + end.x) / 2, 0);
  });

  it("keeps a stub pointing forward when two shapes all but touch", () => {
    const from = peer("left", 300, "relay");
    const to = peer("right", 360, "relay");
    const c = connectorFor({ from, to, name: "right", alternate: false });
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
    const c = connectorFor({ from, to, name: "left", alternate: false });
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
      alternate: false,
    });
    const b = connectorFor({
      from: parent,
      to: kid("b", 540, "relay"),
      name: "b",
      alternate: false,
    });
    // Second point of an elbow is where it reaches the rail. A rail taken from
    // the shapes rather than the rows would put a disc and a card on two.
    expect((points(a.d)[1] as { y: number }).y).toBe(
      (points(b.d)[1] as { y: number }).y,
    );
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
