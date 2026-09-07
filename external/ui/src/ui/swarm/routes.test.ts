import { describe, expect, it } from "vitest";
import {
  apiPathFor,
  groupByNode,
  parseSwarmSessionHash,
  routeLabel,
  sessionApiPath,
  sessionKey,
  swarmSessionHash,
} from "./routes";
import type { SwarmSession } from "./types";

function session(over: Partial<SwarmSession> = {}): SwarmSession {
  return {
    id: "sess_alpha",
    node_path: ["nas02"],
    node_name: "nas02",
    ...over,
  };
}

describe("apiPathFor", () => {
  it("leaves a local path alone", () => {
    expect(apiPathFor([], "/coddy/sessions")).toBe("/coddy/sessions");
  });

  it("mounts one node", () => {
    expect(apiPathFor(["nas02"], "/coddy/sessions")).toBe(
      "/swarm/nodes/nas02/coddy/sessions",
    );
  });

  it("walks every hop of a chain rather than collapsing it", () => {
    expect(apiPathFor(["relay2", "relay3", "agent7"], "/v1/responses")).toBe(
      "/swarm/nodes/relay2/swarm/nodes/relay3/swarm/nodes/agent7/v1/responses",
    );
  });

  it("tolerates a path without a leading slash", () => {
    expect(apiPathFor(["nas02"], "coddy/sessions")).toBe(
      "/swarm/nodes/nas02/coddy/sessions",
    );
  });

  it("routes a session by the node that owns it", () => {
    const s = session({ node_path: ["inner", "agent7"] });
    expect(sessionApiPath(s, "/coddy/sessions/sess_alpha/messages")).toBe(
      "/swarm/nodes/inner/swarm/nodes/agent7/coddy/sessions/sess_alpha/messages",
    );
  });
});

describe("session identity", () => {
  it("tells apart the same id on two nodes", () => {
    const a = session({ node_path: ["nas02"], node_name: "nas02" });
    const b = session({ node_path: ["gpu03"], node_name: "gpu03" });
    expect(sessionKey(a)).not.toBe(sessionKey(b));
  });

  it("round trips through a hash route", () => {
    const s = session({ node_path: ["inner", "agent7"], id: "sess_deep" });
    const parsed = parseSwarmSessionHash(swarmSessionHash(s));
    expect(parsed).toEqual({ nodePath: ["inner", "agent7"], id: "sess_deep" });
  });

  it("reads a bare session with no hops", () => {
    expect(parseSwarmSessionHash("#/swarm/s/sess_alpha")).toEqual({
      nodePath: [],
      id: "sess_alpha",
    });
  });

  it("refuses a hash that is not a swarm session", () => {
    expect(parseSwarmSessionHash("#/s/sess_alpha")).toBeNull();
    expect(parseSwarmSessionHash("#/swarm")).toBeNull();
    expect(parseSwarmSessionHash("#/swarm/s/")).toBeNull();
  });

  it("refuses a hash with an empty segment rather than guessing", () => {
    expect(parseSwarmSessionHash("#/swarm/s/inner//sess")).toBeNull();
  });
});

describe("groupByNode", () => {
  it("gathers sessions under the node that owns them", () => {
    const groups = groupByNode([
      session({ id: "a", node_path: ["nas02"], node_name: "nas02" }),
      session({ id: "b", node_path: ["gpu03"], node_name: "gpu03" }),
      session({ id: "c", node_path: ["nas02"], node_name: "nas02" }),
    ]);
    expect(groups.map((g) => g.node)).toEqual(["gpu03", "nas02"]);
    expect(groups[1]?.sessions.map((s) => s.id)).toEqual(["a", "c"]);
  });

  it("keeps two nodes apart when they share a session id", () => {
    const groups = groupByNode([
      session({ id: "same", node_path: ["nas02"], node_name: "nas02" }),
      session({ id: "same", node_path: ["gpu03"], node_name: "gpu03" }),
    ]);
    expect(groups).toHaveLength(2);
  });

  it("separates the same node name reached by different routes", () => {
    const groups = groupByNode([
      session({ id: "a", node_path: ["inner", "agent7"], node_name: "agent7" }),
      session({ id: "b", node_path: ["agent7"], node_name: "agent7" }),
    ]);
    expect(groups).toHaveLength(2);
  });

  it("returns nothing for an empty list", () => {
    expect(groupByNode([])).toEqual([]);
  });
});

describe("routeLabel", () => {
  it("reads as a path a person can follow", () => {
    expect(routeLabel(["outer", "inner", "agent7"])).toBe(
      "outer › inner › agent7",
    );
  });
});
