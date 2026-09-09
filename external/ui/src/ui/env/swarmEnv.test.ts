import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectSwarmNode, getEnv, returnToSwarm, setEnv } from "./remoteEnv";

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

  // Staying on the swarm route would leave the screen asking a node whether it
  // is a relay, and being told no.
  it("leaves the swarm route when no session is named", () => {
    window.location.hash = "#/swarm";
    connectSwarmNode("http://relay.example", ["nas02"], "tok");
    expect(window.location.hash).toBe("#/");
  });

  it("refuses a call with no node to open", () => {
    setEnv({ mode: "local" });
    connectSwarmNode("http://relay.example", [], "tok");
    expect(getEnv().mode).toBe("local");
  });
});

describe("returnToSwarm", () => {
  it("goes back out to the relay and opens the swarm screen", () => {
    connectSwarmNode("http://relay.example", ["inner", "agent7"], "tok");
    returnToSwarm();
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.baseUrl).toBe("http://relay.example");
    expect(env.swarmRelay).toBeUndefined();
    expect(window.location.hash).toBe("#/swarm");
  });

  it("does nothing from an environment that came through no relay", () => {
    setEnv({ mode: "remote", baseUrl: "http://plain.example", token: "t" });
    returnToSwarm();
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote env");
    expect(env.baseUrl).toBe("http://plain.example");
  });
});
