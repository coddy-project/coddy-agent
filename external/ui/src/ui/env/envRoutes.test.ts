import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  connectLocal,
  connectRemote,
  connectSwarmNode,
  connectSwarmRelay,
  getEnv,
  onEnvironmentSwitch,
  setEnv,
  subscribeEnv,
  switchGeneration,
} from "./remoteEnv";

// Switching the environment reloads the page on the same address, so the route
// of one environment used to be carried into the next: a local session id asked
// of a relay, a relay's #/swarm asked of a plain agent (issue #401, point 4).
// Every environment now keeps the route it was left at.
beforeEach(() => {
  localStorage.clear();
  setEnv({ mode: "local" });
  const loc = { hash: "", reload: vi.fn() };
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

describe("the route of each environment", () => {
  it("returns to the local session a relay was opened from", () => {
    window.location.hash = "#/s/sess_local";
    connectRemote("http://relay.lan:12346", "tok", "office-relay");
    // A remote this browser has never been on opens at its home.
    expect(window.location.hash).toBe("#/");
    // On the relay the reader went to the map.
    window.location.hash = "#/swarm";
    connectLocal();
    expect(getEnv().mode).toBe("local");
    expect(window.location.hash).toBe("#/s/sess_local");
  });

  it("opens a remote at the route it was left at", () => {
    window.location.hash = "#/";
    connectRemote("http://box:12345", "tok", "box");
    window.location.hash = "#/s/sess_remote";
    connectLocal();
    expect(window.location.hash).toBe("#/");
    connectRemote("http://box:12345/", "tok", "box");
    expect(window.location.hash).toBe("#/s/sess_remote");
  });

  it("keeps the route of each remote apart", () => {
    connectRemote("http://a:1", "t", "a");
    window.location.hash = "#/s/on_a";
    connectRemote("http://b:1", "t", "b");
    expect(window.location.hash).toBe("#/");
    window.location.hash = "#/scheduler";
    connectRemote("http://a:1", "t", "a");
    expect(window.location.hash).toBe("#/s/on_a");
    connectRemote("http://b:1", "t", "b");
    expect(window.location.hash).toBe("#/scheduler");
  });

  // Entering a node from the map lands where the map meant (the session that
  // waits for an answer, or the node's home), and a click on the relay lands
  // on its map.
  it("keeps the map's choice when a node is entered, and the relay opens on its map", () => {
    connectRemote("http://relay:1", "t", "relay");
    window.location.hash = "#/swarm";
    connectSwarmNode("http://relay:1", ["worker-a"], "t", "#/s/waiting");
    expect(window.location.hash).toBe("#/s/waiting");
    connectSwarmRelay("http://relay:1", [], "t");
    expect(window.location.hash).toBe("#/swarm");
  });

  // A node is an environment like any other: chosen without a route of its
  // own (the environment menu names none), it opens where it was left.
  it("opens a node where it was left when nothing names a route", () => {
    connectRemote("http://relay:1", "t", "relay");
    connectSwarmNode("http://relay:1", ["worker-a"], "t");
    window.location.hash = "#/s/on_worker_a";
    connectRemote("http://box:1", "t", "box");
    connectSwarmNode("http://relay:1", ["worker-a"], "t");
    expect(window.location.hash).toBe("#/s/on_worker_a");
  });

  // The token of a relay or a node is the environment's; a copy in the
  // browser's per-remote list would outlive a token the configuration stops
  // handing out (connectConfiguredRemote leaves it out for the same reason).
  it("keeps the token of a swarm switch out of the browser's list", () => {
    connectRemote("http://relay:1", "from-config", "relay", {
      rememberToken: false,
    });
    connectSwarmNode("http://relay:1", ["worker-a"], "from-config");
    connectSwarmRelay("http://relay:1", [], "from-config");
    expect(localStorage.getItem("coddy_env_tokens")).toBeNull();
  });

  it("survives a browser that refuses storage", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("quota");
      });
    window.location.hash = "#/s/x";
    expect(() => connectRemote("http://box:1", "t", "box")).not.toThrow();
    expect(window.location.hash).toBe("#/");
    setItem.mockRestore();
  });
});

// A reload blanked the page for every switch, the map's included. Between two
// remotes the page itself holds nothing that changes - no sign-in, no local
// origin - so the app starts over on the new one in place: the environment and
// the address change, the modules that keep something of the old one forget
// it, and nothing reloads. To or from Local the page still reloads, since the
// sign-in of the page's own origin comes and goes with it.
describe("switching in place", () => {
  it("moves between two remotes without reloading the page", () => {
    connectRemote("http://relay:1", "t", "relay");
    const reload = window.location.reload as unknown as ReturnType<
      typeof vi.fn
    >;
    reload.mockClear();
    const forgot = vi.fn();
    const stop = onEnvironmentSwitch(forgot);
    window.location.hash = "#/swarm";
    connectSwarmNode("http://relay:1", ["worker-a"], "t", "#/swarm");
    stop();
    expect(reload).not.toHaveBeenCalled();
    expect(forgot).toHaveBeenCalledTimes(1);
    const env = getEnv();
    expect(env.mode === "remote" && env.baseUrl).toBe(
      "http://relay:1/swarm/nodes/worker-a",
    );
    expect(window.location.hash).toBe("#/swarm");
  });

  // An app started over on the new environment while the reload is on its way
  // writes the address, and a written address cancels the reload: the page
  // stayed on the old app's menu over the new environment.
  it("tells nobody before a reload", () => {
    const heard = vi.fn();
    const stop = subscribeEnv(heard);
    connectRemote("http://box:1", "t", "box");
    stop();
    expect(heard).not.toHaveBeenCalled();
    expect(getEnv().mode).toBe("remote");
  });

  // Choosing the environment the page is on is how everything is read again
  // after that remote came back: the app starts over on it, still in place.
  it("starts the app over when the same remote is chosen again", () => {
    connectRemote("http://relay:1", "t", "relay");
    const before = switchGeneration();
    const heard = vi.fn();
    const stop = subscribeEnv(heard);
    connectRemote("http://relay:1", "t", "relay");
    stop();
    expect(switchGeneration()).toBe(before + 1);
    expect(heard).toHaveBeenCalled();
  });

  it("reloads the page to and from Local", () => {
    const reload = window.location.reload as unknown as ReturnType<
      typeof vi.fn
    >;
    connectRemote("http://box:1", "t", "box");
    expect(reload).toHaveBeenCalledTimes(1);
    connectLocal();
    expect(reload).toHaveBeenCalledTimes(2);
  });
});
