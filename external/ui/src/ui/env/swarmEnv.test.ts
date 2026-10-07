import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  connectSwarmNode,
  connectSwarmRelay,
  getEnv,
  setEnv,
  swarmMountPath,
  swarmMountRoot,
  swarmRootRelay,
} from "./remoteEnv";

// The env module caches and reloads the page, so both are stubbed.
beforeEach(() => {
  localStorage.clear();
  const loc = {
    hash: "",
    reload: vi.fn(),
  };
  vi.stubGlobal("location", loc as unknown as Location);
  Object.defineProperty(window, "location", {
    value: loc,
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("connectSwarmNode", () => {
  // A node's mount is a base URL with a path, which is all the rest of the app
  // has ever needed: every existing screen then works against that node with a
  // relay in the middle and no idea it is there.
  it("points the app at the node's mount under the relay", () => {
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    const env = getEnv();
    expect(env.mode).toBe("remote");
    if (env.mode !== "remote") return;
    expect(env.baseUrl).toBe("http://relay.example/swarm/nodes/nas02");
    expect(env.token).toBe("tok");
  });

  it("writes out every hop of a chain", () => {
    connectSwarmNode("http://relay.example", ["inner", "agent7"], "tok");
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.baseUrl).toBe(
      "http://relay.example/swarm/nodes/inner/swarm/nodes/agent7",
    );
    expect(env.name).toBe("agent7");
  });

  // Inside a node the relay's own routes are no longer under the base URL, so
  // without remembering where we came from there is no way back but to type the
  // relay's address again.
  it("remembers the relay it was reached through", () => {
    connectSwarmNode("http://relay.example", ["inner", "agent7"], "tok");
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.swarmRelay).toBe("http://relay.example");
    expect(env.swarmNode).toBe("inner/agent7");
  });

  it("can land on a particular session", () => {
    connectSwarmNode("http://relay.example", ["nas02"], "tok", "#/s/sess_a");
    expect(window.location.hash).toBe("#/s/sess_a");
  });

  // The route of the environment being left is not carried to a node: a node
  // the app has not been on opens at its home. (The map over a node is a route
  // of that node's own, kept for it like any other.)
  it("opens a node it has not been on at its home, whatever route it left", () => {
    setEnv({ mode: "remote", baseUrl: "http://relay.example", token: "tok" });
    window.location.hash = "#/swarm";
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    expect(window.location.hash).toBe("#/");
  });

  // Choosing the node the app is on again keeps it where it is, the map over
  // it included, rather than dropping it back to its home.
  it("keeps the route of the node chosen again", () => {
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    window.location.hash = "#/swarm";
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    expect(window.location.hash).toBe("#/swarm");
  });

  it("refuses a call with no node to open", () => {
    setEnv({ mode: "local" });
    connectSwarmNode("http://relay.example", [], "tok");
    expect(getEnv().mode).toBe("local");
  });
});

describe("connectSwarmRelay", () => {
  // From the map over a node, a click on the relay connects to the relay
  // itself: the node is left, and the app opens on the relay's own map
  // (issue #401).
  it("connects from a node to the relay itself", () => {
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    connectSwarmRelay("http://relay.example", [], "tok");
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.baseUrl).toBe("http://relay.example");
    expect(env.swarmRelay).toBeUndefined();
    expect(env.swarmNode).toBeUndefined();
    expect(window.location.hash).toBe("#/swarm");
    expect(window.location.reload).toHaveBeenCalled();
  });

  // A relay chained under this one is a relay, not a node: its mount is
  // entered as an environment of its own, which answers /swarm/info and so
  // opens on its own map - and whose nodes are entered through it in turn.
  it("opens a relay chained under this one as a relay, through its mount", () => {
    connectSwarmRelay("http://relay.example", ["middle"], "tok");
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.baseUrl).toBe("http://relay.example/swarm/nodes/middle");
    expect(env.name).toBe("middle");
    expect(env.token).toBe("tok");
    // Not a node: the app asks it whether it is a relay rather than drawing
    // a chat for it.
    expect(env.swarmRelay).toBeUndefined();
    expect(window.location.hash).toBe("#/swarm");
  });
});

describe("swarm mount helpers", () => {
  // A mount URL spells the whole chain from the outermost relay down, so the
  // map can always be drawn by the root relay no matter how deep the env is.
  it("swarmMountRoot returns the relay a mount hangs off", () => {
    expect(swarmMountRoot("http://r:1/swarm/nodes/east/swarm/nodes/deep")).toBe(
      "http://r:1",
    );
    expect(swarmMountRoot("http://r:1/swarm/nodes/east")).toBe("http://r:1");
    expect(swarmMountRoot("http://r:1")).toBe("");
    expect(swarmMountRoot("http://node.example")).toBe("");
  });

  it("swarmMountPath lists the chained names, outermost first", () => {
    expect(
      swarmMountPath("http://r:1/swarm/nodes/east/swarm/nodes/deep"),
    ).toEqual(["east", "deep"]);
    expect(swarmMountPath("http://r:1/swarm/nodes/east")).toEqual(["east"]);
    expect(swarmMountPath("http://r:1")).toEqual([]);
  });

  it("swarmRootRelay resolves to the outermost relay of the chain", () => {
    // A node reached through a chained relay: its swarmRelay is itself a
    // mount, and the root is still the outermost relay.
    expect(
      swarmRootRelay({
        baseUrl: "http://r:1/swarm/nodes/east/swarm/nodes/laptop",
        swarmRelay: "http://r:1/swarm/nodes/east",
      }),
    ).toBe("http://r:1");
    // A chained relay env: the mount is in its own baseUrl.
    expect(swarmRootRelay({ baseUrl: "http://r:1/swarm/nodes/east" })).toBe(
      "http://r:1",
    );
    // A plain node mount and a direct remote stand as they are.
    expect(
      swarmRootRelay({
        baseUrl: "http://r:1/swarm/nodes/box",
        swarmRelay: "http://r:1",
      }),
    ).toBe("http://r:1");
    expect(swarmRootRelay({ baseUrl: "http://other.example" })).toBe(
      "http://other.example",
    );
  });
});
