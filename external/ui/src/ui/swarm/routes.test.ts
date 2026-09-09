import { describe, expect, it } from "vitest";
import {
  apiPathFor,
  nodeActivity,
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

describe("routeLabel", () => {
  it("reads as a path a person can follow", () => {
    expect(routeLabel(["outer", "inner", "agent7"])).toBe(
      "outer › inner › agent7",
    );
  });
});

describe("nodeActivity", () => {
  it("counts nothing for a swarm with no sessions", () => {
    expect(nodeActivity([])).toEqual({});
  });

  it("counts sessions, turns in flight and prompts per node", () => {
    const work = nodeActivity([
      session({ id: "a", turnActive: true }),
      session({ id: "b" }),
      session({ id: "c", permissionPending: true }),
    ]);
    expect(work["nas02"]).toEqual({ sessions: 3, running: 1, waiting: 1 });
  });

  // The route is the key, not the name: two machines called the same thing sit
  // at different places on the map and must not pool their work.
  it("keeps two routes to the same name apart", () => {
    const work = nodeActivity([
      session({ id: "a", node_path: ["agent7"], turnActive: true }),
      session({ id: "b", node_path: ["inner", "agent7"] }),
    ]);
    expect(work["agent7"]?.running).toBe(1);
    expect(work["inner/agent7"]).toEqual({
      sessions: 1,
      running: 0,
      waiting: 0,
    });
  });

  it("counts one session as both running and waiting when it is both", () => {
    const work = nodeActivity([
      session({ id: "a", turnActive: true, permissionPending: true }),
    ]);
    expect(work["nas02"]).toEqual({ sessions: 1, running: 1, waiting: 1 });
  });
});
