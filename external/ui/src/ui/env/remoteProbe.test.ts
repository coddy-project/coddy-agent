import { afterEach, describe, expect, it, vi } from "vitest";
import { probeRemote, relayAgents } from "./remoteProbe";

// probeRemote reaches the network through localFetch, which captures the native
// fetch at module load, so the module is mocked rather than the global.
type Seen = { url: string; init: RequestInit | undefined };
const seen: Seen[] = [];
let respond: (url: string, init?: RequestInit) => Response | Error = () =>
  new Error("unset");

vi.mock("./remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./remoteEnv")>();
  return {
    ...actual,
    localFetch: (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      seen.push({ url, init });
      const out = respond(url, init);
      return out instanceof Error ? Promise.reject(out) : Promise.resolve(out);
    },
  };
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function bearerOf(init?: RequestInit): string {
  return new Headers(init?.headers ?? undefined).get("Authorization") ?? "";
}

/** A relay: no /v1, a public /swarm/info, the rest behind its client token. */
function relay(clientToken: string) {
  return (url: string, init?: RequestInit): Response | Error => {
    if (init?.mode === "no-cors") {
      return new Response(null, { status: 200 });
    }
    if (url.endsWith("/swarm/info")) {
      return json(200, { swarm: true, name: "office" });
    }
    if (url.includes("/swarm/")) {
      return bearerOf(init) === "Bearer " + clientToken
        ? json(200, { nodes: [] })
        : json(401, { error: { message: "unauthorized" } });
    }
    return new Response("404 page not found\n", { status: 404 });
  };
}

afterEach(() => {
  seen.length = 0;
});

describe("probeRemote", () => {
  // A request the probe gave up on is not one the browser kept from the page:
  // a slow agent whose /v1/models outlived the timeout, and whose /swarm/info
  // answers at once, is not a CORS problem.
  it("calls a remote that did not answer in time down, not blocked by CORS", async () => {
    respond = (url, init) => {
      if (url.endsWith("/v1/models")) {
        // What AbortSignal.timeout rejects a fetch with.
        return Object.assign(new Error("signal timed out"), {
          name: "TimeoutError",
        });
      }
      if (init?.mode === "no-cors") {
        return new Response(null, { status: 200 });
      }
      return new Response("404 page not found\n", { status: 404 });
    };
    const probe = await probeRemote("http://slow.lan:12345", "t");
    expect(probe.reach).toBe("down");
    expect(seen.some((s) => s.init?.mode === "no-cors")).toBe(false);
  });

  it("calls an agent that lists its models with the token up", async () => {
    respond = (url) =>
      url.endsWith("/v1/models")
        ? json(200, { data: [] })
        : new Error("not asked");
    await expect(probeRemote("http://box:12345/", "t")).resolves.toEqual({
      reach: "up",
      relay: false,
    });
    expect(seen.map((s) => s.url)).toEqual(["http://box:12345/v1/models"]);
    expect(bearerOf(seen[0]?.init)).toBe("Bearer t");
  });

  it("calls an agent that refuses the token unauthorized, not down", async () => {
    respond = (url) =>
      url.endsWith("/v1/models")
        ? json(401, { error: { message: "unauthorized" } })
        : new Response("404 page not found\n", { status: 404 });
    await expect(probeRemote("http://box:12345", "bad")).resolves.toEqual({
      reach: "unauthorized",
      relay: false,
    });
  });

  // The relay serves no /v1 at all, so its 404 is not a failure: the node list
  // decides, because it is the first question that takes the token.
  it("calls a relay that accepts the client token up", async () => {
    respond = relay("client");
    await expect(probeRemote("http://relay:12346", "client")).resolves.toEqual({
      reach: "up",
      relay: true,
    });
    const nodes = seen.find((s) => s.url.endsWith("/swarm/nodes"));
    expect(bearerOf(nodes?.init)).toBe("Bearer client");
  });

  // /swarm/info needs no token, so a relay without one used to be called
  // healthy by the fallback that only asked it.
  it("calls a relay without a token unauthorized", async () => {
    respond = relay("client");
    await expect(probeRemote("http://relay:12346", "")).resolves.toEqual({
      reach: "unauthorized",
      relay: true,
    });
  });

  it("asks the public route bare only when there is no token to send", async () => {
    respond = relay("client");
    await probeRemote("http://relay:12346", "");
    const info = seen.find((s) => s.url.endsWith("/swarm/info"));
    expect(bearerOf(info?.init)).toBe("");
  });

  it("asks the public route with the token, which a node mount requires", async () => {
    // A relay entered through its parent's mount sits behind the parent's
    // client token: /swarm/info is public on a bare relay, but the mount
    // refuses a request that carries no credential at all.
    respond = (url, init) => {
      if (init?.mode === "no-cors") {
        return new Response(null, { status: 200 });
      }
      if (url.endsWith("/swarm/info")) {
        return bearerOf(init) === "Bearer parent"
          ? json(200, { swarm: true, name: "inner" })
          : json(401, { error: { message: "unauthorized" } });
      }
      if (url.endsWith("/swarm/nodes")) {
        return bearerOf(init) === "Bearer parent"
          ? json(200, { nodes: [] })
          : json(401, { error: { message: "unauthorized" } });
      }
      return new Response("404 page not found\n", { status: 404 });
    };
    await expect(
      probeRemote("http://relay:12346/swarm/nodes/inner", "parent"),
    ).resolves.toEqual({ reach: "up", relay: true });
    const info = seen.find((s) => s.url.endsWith("/swarm/info"));
    expect(bearerOf(info?.init)).toBe("Bearer parent");
  });

  // A browser that the remote's CORS leaves out gets no answer at all, which is
  // the same rejection as a machine that is off. An opaque request tells them
  // apart: it needs no CORS and fails only when nothing answers.
  it("tells a remote that answers but blocks this page by CORS from one that is off", async () => {
    respond = (_url, init) =>
      init?.mode === "no-cors"
        ? new Response(null, { status: 200 })
        : new TypeError("Failed to fetch");
    await expect(probeRemote("http://relay:12346", "t")).resolves.toEqual({
      reach: "cors",
      relay: false,
    });
    respond = () => new TypeError("Failed to fetch");
    await expect(probeRemote("http://relay:12346", "t")).resolves.toEqual({
      reach: "down",
      relay: false,
    });
  });

  it("calls a host that is neither an agent nor a relay down", async () => {
    respond = () => new Response("nope", { status: 404 });
    await expect(probeRemote("http://nothing:80", "t")).resolves.toEqual({
      reach: "down",
      relay: false,
    });
  });
});

describe("relayAgents", () => {
  it("lists every agent the relay reaches with its route, nearest first", async () => {
    respond = (url) =>
      url.endsWith("/swarm/topology")
        ? json(200, {
            nodes: [
              { uuid: "r0", name: "office", kind: "relay", online: true },
              { uuid: "r1", name: "inner", kind: "relay", online: true },
              { uuid: "a2", name: "worker-10", kind: "agent", online: true },
              { uuid: "a1", name: "worker-2", kind: "agent", online: false },
              { uuid: "a3", name: "gpu", kind: "agent", online: true },
            ],
            routes: {
              r1: { path: ["inner"] },
              a1: { path: ["worker-2"] },
              a2: { path: ["worker-10"] },
              a3: { path: ["inner", "gpu"] },
            },
          })
        : new Error("not asked");
    await expect(relayAgents("http://relay:12346", "client")).resolves.toEqual([
      { name: "worker-2", path: ["worker-2"], online: false },
      { name: "worker-10", path: ["worker-10"], online: true },
      { name: "gpu", path: ["inner", "gpu"], online: true },
    ]);
  });

  it("falls back to the direct children when the topology is not there", async () => {
    respond = (url) =>
      url.endsWith("/swarm/topology")
        ? json(500, {})
        : json(200, {
            nodes: [
              { name: "b", kind: "agent", online: true },
              { name: "inner", kind: "relay", online: true },
              { name: "a", kind: "agent", online: true },
            ],
          });
    await expect(relayAgents("http://relay:12346", "client")).resolves.toEqual([
      { name: "a", path: ["a"], online: true },
      { name: "b", path: ["b"], online: true },
    ]);
  });

  it("returns nothing when the relay does not answer", async () => {
    respond = () => new TypeError("Failed to fetch");
    await expect(relayAgents("http://relay:12346", "client")).resolves.toEqual(
      [],
    );
  });
});
