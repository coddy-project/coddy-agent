import { describe, expect, it } from "vitest";
import { layoutTopology, topologySummary, type TopologyLayout } from "./layout";
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
