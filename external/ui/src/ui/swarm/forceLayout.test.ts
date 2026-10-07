import { describe, expect, it } from "vitest";
import { layoutTopologyGraph } from "./forceLayout";
import {
  CLIENT_UUID,
  layoutTopology,
  nodeHalfHeight,
  nodeHalfWidth,
  type LayoutOptions,
  type TopologyLayout,
} from "./layout";
import type { SwarmTopology } from "./types";

const client: LayoutOptions = { client: { name: "browser-host" } };
const empty: SwarmTopology = {
  root: { uuid: "root", name: "entry", kind: "relay", online: true },
  nodes: [],
  edges: [],
  routes: {},
  warnings: [],
};
const ring: SwarmTopology = {
  ...empty,
  nodes: [
    { uuid: "west", name: "west", kind: "relay", online: true },
    { uuid: "east", name: "east", kind: "relay", online: true },
    { uuid: "agent", name: "worker", kind: "agent", online: true },
    { uuid: "lost", name: "offline", kind: "agent", online: false },
  ],
  edges: [
    { from_uuid: "root", to_uuid: "west", name: "west" },
    { from_uuid: "root", to_uuid: "east", name: "east" },
    { from_uuid: "west", to_uuid: "east", name: "east" },
    { from_uuid: "east", to_uuid: "root", name: "entry" },
    { from_uuid: "east", to_uuid: "agent", name: "worker" },
  ],
  routes: {
    west: { path: ["west"] },
    east: { path: ["east"], alternates: [["west", "east"]] },
    agent: { path: ["east", "worker"] },
  },
};

function fan(count: number): SwarmTopology {
  const nodes: SwarmTopology["nodes"] = Array.from(
    { length: count },
    (_, i) => ({
      uuid: `node-${i}`,
      name: `node-${i}`,
      kind: i % 2 ? "agent" : "relay",
      online: true,
    }),
  );
  return {
    ...empty,
    nodes,
    edges: nodes.map((n) => ({
      from_uuid: "root",
      to_uuid: n.uuid,
      name: n.name,
    })),
    routes: Object.fromEntries(nodes.map((n) => [n.uuid, { path: [n.name] }])),
  };
}

/** A two-branch relay tree whose wires tangle when subtrees interleave. */
function treeTopology(): SwarmTopology {
  const branches: [string, string[]][] = [
    ["ra", ["a1", "a2"]],
    ["rb", ["b1", "b2"]],
  ];
  const nodes: SwarmTopology["nodes"] = [];
  const edges: SwarmTopology["edges"] = [];
  const routes: SwarmTopology["routes"] = {};
  for (const [relay, kids] of branches) {
    nodes.push({ uuid: relay, name: relay, kind: "relay", online: true });
    edges.push({ from_uuid: "root", to_uuid: relay, name: relay });
    routes[relay] = { path: [relay] };
    for (const kid of kids) {
      nodes.push({ uuid: kid, name: kid, kind: "agent", online: true });
      edges.push({ from_uuid: relay, to_uuid: kid, name: kid });
      routes[kid] = { path: [relay, kid] };
    }
  }
  return { ...empty, nodes, edges, routes };
}

type Point = { x: number; y: number };

function segmentsCross(a1: Point, a2: Point, b1: Point, b2: Point): boolean {
  const side = (p: Point, q: Point, r: Point) =>
    (q.x - p.x) * (r.y - p.y) - (q.y - p.y) * (r.x - p.x);
  const d1 = side(b1, b2, a1);
  const d2 = side(b1, b2, a2);
  const d3 = side(a1, a2, b1);
  const d4 = side(a1, a2, b2);
  return (
    ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
    ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))
  );
}

/** Strict crossings between route wires that share no endpoint. */
function countRouteCrossings(layout: TopologyLayout): number {
  const wires = layout.edges.filter((e) => !e.alternate);
  let count = 0;
  for (let i = 0; i < wires.length; i++) {
    for (let j = i + 1; j < wires.length; j++) {
      const a = wires[i]!;
      const b = wires[j]!;
      const shared = [a.from.uuid, a.to.uuid].some(
        (id) => id === b.from.uuid || id === b.to.uuid,
      );
      if (!shared && segmentsCross(a.from, a.to, b.from, b.to)) count += 1;
    }
  }
  return count;
}

function expectValid(layout: TopologyLayout, rootUUID: string): void {
  expect(Number.isFinite(layout.width)).toBe(true);
  expect(Number.isFinite(layout.height)).toBe(true);
  expect(layout.width).toBeGreaterThan(0);
  expect(layout.height).toBeGreaterThan(0);
  expect(layout.tiers).toEqual([]);
  const root = layout.nodes.find((n) => n.uuid === rootUUID)!;
  expect(root).toBeDefined();
  expect(root.x).toBeCloseTo(layout.width / 2, 1);
  expect(layout.nodes.filter((n) => n.y === root.y)).toEqual([root]);
  for (const n of layout.nodes) {
    expect(Number.isFinite(n.x) && Number.isFinite(n.y)).toBe(true);
    expect(n.x * 10).toBeCloseTo(Math.round(n.x * 10), 6);
    expect(n.y * 10).toBeCloseTo(Math.round(n.y * 10), 6);
    expect(n.x - nodeHalfWidth(n)).toBeGreaterThanOrEqual(24);
    expect(n.y - nodeHalfHeight(n)).toBeGreaterThanOrEqual(24);
    expect(n.x + nodeHalfWidth(n)).toBeLessThanOrEqual(layout.width - 24);
    expect(n.y + nodeHalfHeight(n)).toBeLessThanOrEqual(layout.height - 24);
    if (n !== root) expect(n.y).toBeGreaterThan(root.y);
  }
  for (const [i, a] of layout.nodes.entries()) {
    for (const b of layout.nodes.slice(i + 1)) {
      const gapX = Math.abs(a.x - b.x) - nodeHalfWidth(a) - nodeHalfWidth(b);
      const gapY = Math.abs(a.y - b.y) - nodeHalfHeight(a) - nodeHalfHeight(b);
      expect(
        Math.max(gapX, gapY),
        `${a.uuid} and ${b.uuid}`,
      ).toBeGreaterThanOrEqual(24);
    }
  }
  for (const edge of layout.edges) {
    expect(edge.from).toBe(layout.nodes.find((n) => n.uuid === edge.from.uuid));
    expect(edge.to).toBe(layout.nodes.find((n) => n.uuid === edge.to.uuid));
    expect(Number.isFinite(edge.laneX)).toBe(true);
  }
}

function edgeSemantics(layout: TopologyLayout) {
  return layout.edges
    .map(({ id, alternate, name, from, to }) => ({
      id,
      alternate,
      name,
      from: from.uuid,
      to: to.uuid,
    }))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

describe("layoutTopologyGraph", () => {
  it("is exactly deterministic for the same topology and client", () => {
    expect(layoutTopologyGraph(ring, client)).toEqual(
      layoutTopologyGraph(ring, client),
    );
  });

  it("is independent of node, route and edge input order", () => {
    const reversed = {
      ...ring,
      nodes: [...ring.nodes].reverse(),
      edges: [...ring.edges].reverse(),
      routes: Object.fromEntries(Object.entries(ring.routes).reverse()),
    };
    expect(layoutTopologyGraph(reversed, client)).toEqual(
      layoutTopologyGraph(ring, client),
    );
  });

  it("does not mutate the topology or options", () => {
    const before = JSON.stringify({ ring, client });
    layoutTopologyGraph(ring, client);
    expect(JSON.stringify({ ring, client })).toBe(before);
  });

  it("pins a unique synthetic client above the swarm", () => {
    const layout = layoutTopologyGraph(ring, client);
    expect(layout.nodes.filter((n) => n.uuid === CLIENT_UUID)).toHaveLength(1);
    expect(layout.nodes).toHaveLength(ring.nodes.length + 2);
    expectValid(layout, CLIENT_UUID);
  });

  it("pins the topology root above the swarm without a client", () => {
    const layout = layoutTopologyGraph(ring);
    expect(layout.nodes.some((n) => n.uuid === CLIENT_UUID)).toBe(false);
    expect(layout.nodes).toHaveLength(ring.nodes.length + 1);
    expectValid(layout, ring.root.uuid);
  });

  it.each([{}, client])(
    "preserves tree routes, depths and edge semantics with %j",
    (opts) => {
      const tree = layoutTopology(ring, opts);
      const graph = layoutTopologyGraph(ring, opts);
      expect(edgeSemantics(graph)).toEqual(edgeSemantics(tree));
      for (const n of graph.nodes) {
        const original = tree.nodes.find((t) => t.uuid === n.uuid)!;
        expect(n.path).toEqual(original.path);
        expect(n.depth).toBe(original.depth);
      }
      if (opts.client) {
        expect(graph.edges.find((e) => e.from.uuid === CLIENT_UUID)?.id).toBe(
          `${CLIENT_UUID}>root`,
        );
      }
    },
  );

  it.each([{}, client])(
    "terminates with a valid empty relay and options %j",
    (opts) => {
      const layout = layoutTopologyGraph(empty, opts);
      expect(layout.nodes).toHaveLength(opts.client ? 2 : 1);
      expectValid(layout, opts.client ? CLIENT_UUID : empty.root.uuid);
    },
  );

  it("terminates with a valid ring and disconnected node", () => {
    expectValid(layoutTopologyGraph(ring, client), CLIENT_UUID);
  });

  it("keeps a dense mixed fan collision-free and in bounds", () => {
    expectValid(layoutTopologyGraph(fan(64), client), CLIENT_UUID);
  });

  it("does not constrain equal-depth nodes to rigid hop rows", () => {
    const layout = layoutTopologyGraph(fan(8));
    const children = layout.nodes.filter((n) => n.depth === 1);
    expect(new Set(children.map((n) => n.y)).size).toBeGreaterThan(1);
  });

  it("keeps long relay labels inside non-overlapping neighboring cards", () => {
    const topology = fan(12);
    topology.nodes = topology.nodes.map((n) => ({
      ...n,
      kind: "relay",
      name: `very-long-relay-name-${n.uuid}-${"x".repeat(200)}`,
    }));
    const layout = layoutTopologyGraph(topology, client);
    expect(layout.nodes.find((n) => n.uuid === "node-0")?.name).toBe(
      topology.nodes[0]!.name,
    );
    expectValid(layout, CLIENT_UUID);
  });

  it("keeps sibling subtrees apart so route edges do not cross", () => {
    // Two branches ordered left-to-right: every descendant of the left branch
    // must stay left of every descendant of the right one, or the route wires
    // would cross.
    const topology: SwarmTopology = {
      ...empty,
      nodes: [
        { uuid: "east", name: "east", kind: "relay", online: true },
        { uuid: "west", name: "west", kind: "relay", online: true },
        { uuid: "east-a", name: "east-a", kind: "agent", online: true },
        { uuid: "east-b", name: "east-b", kind: "agent", online: true },
        { uuid: "west-a", name: "west-a", kind: "agent", online: true },
        { uuid: "west-b", name: "west-b", kind: "agent", online: true },
      ],
      edges: [
        { from_uuid: "root", to_uuid: "east", name: "east" },
        { from_uuid: "root", to_uuid: "west", name: "west" },
        { from_uuid: "east", to_uuid: "east-a", name: "east-a" },
        { from_uuid: "east", to_uuid: "east-b", name: "east-b" },
        { from_uuid: "west", to_uuid: "west-a", name: "west-a" },
        { from_uuid: "west", to_uuid: "west-b", name: "west-b" },
      ],
      routes: {
        east: { path: ["east"] },
        west: { path: ["west"] },
        "east-a": { path: ["east", "east-a"] },
        "east-b": { path: ["east", "east-b"] },
        "west-a": { path: ["west", "west-a"] },
        "west-b": { path: ["west", "west-b"] },
      },
    };
    const layout = layoutTopologyGraph(topology);
    const x = (uuid: string) => layout.nodes.find((n) => n.uuid === uuid)!.x;

    const eastX = x("east");
    const westX = x("west");
    expect(eastX).not.toBeCloseTo(westX, 1);
    const [leftBranch, rightBranch] =
      eastX < westX ? ["east", "west"] : ["west", "east"];
    const leftX = [x(`${leftBranch}-a`), x(`${leftBranch}-b`)];
    const rightX = [x(`${rightBranch}-a`), x(`${rightBranch}-b`)];
    for (const lx of [x(leftBranch), ...leftX]) {
      for (const rx of [x(rightBranch), ...rightX]) {
        expect(
          lx,
          `${leftBranch} subtree must stay left of ${rightBranch}`,
        ).toBeLessThan(rx);
      }
    }
  });

  it("draws a branching tree without crossing route wires", () => {
    // Strictly crossed segments between edges that share no endpoint read as
    // a tangle no matter how smooth the curves are. The sibling ordering must
    // keep the non-alternate wires apart.
    expect(countRouteCrossings(layoutTopologyGraph(treeTopology()))).toBe(0);
    expect(
      countRouteCrossings(
        layoutTopologyGraph(treeTopology(), { client: { name: "me" } }),
      ),
    ).toBe(0);
  });

  it("retains the tree filtering of dangling and self edges", () => {
    const topology: SwarmTopology = {
      ...ring,
      edges: [
        ...ring.edges,
        { from_uuid: "root", to_uuid: "ghost", name: "missing" },
        { from_uuid: "west", to_uuid: "west", name: "self" },
      ],
    };
    const layout = layoutTopologyGraph(topology, client);
    expect(edgeSemantics(layout)).toEqual(
      edgeSemantics(layoutTopology(topology, client)),
    );
    expectValid(layout, CLIENT_UUID);
  });
});
