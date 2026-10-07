import { afterEach, describe, expect, it, vi } from "vitest";
import { probeActiveEnv, probeEnvHealth } from "./activeHealth";

describe("probeEnvHealth", () => {
  it("reports the local environment as up without any network call", async () => {
    expect(await probeEnvHealth({ mode: "local" })).toBe("up");
  });
});

// probeEnvHealth reaches the network through localFetch, which captures the
// native fetch at module load, so the module is mocked rather than the global.
const seen: string[] = [];
let respond: (url: string, init?: RequestInit) => Response | Error = () =>
  new Response(null, { status: 200 });

vi.mock("./remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./remoteEnv")>();
  return {
    ...actual,
    localFetch: (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      seen.push(url);
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

/** A relay: no /v1, a public /swarm/info, the node list behind `client`. */
function relay(url: string, init?: RequestInit): Response {
  if (url.endsWith("/swarm/info")) {
    return json(200, { swarm: true });
  }
  if (url.endsWith("/swarm/nodes")) {
    const auth = new Headers(init?.headers ?? undefined).get("Authorization");
    return auth === "Bearer client"
      ? json(200, { nodes: [] })
      : json(401, { error: { message: "unauthorized" } });
  }
  return new Response("404 page not found\n", { status: 404 });
}

describe("probeEnvHealth against a relay", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    seen.length = 0;
  });

  // A relay serves no model catalog of its own, so the usual probe would call a
  // healthy relay unreachable and the app would offer to switch away from it.
  it("calls a relay that accepts the client token up", async () => {
    respond = relay;
    const health = await probeEnvHealth({
      mode: "remote",
      baseUrl: "http://relay.example",
      token: "client",
    });
    expect(health).toBe("up");
    expect(seen.some((u) => u.endsWith("/swarm/info"))).toBe(true);
  });

  // /swarm/info is public: answering it says nothing about the token, and the
  // chip used to show a relay without one as healthy while the map said it
  // needed a token.
  it("calls a relay down while it refuses the token", async () => {
    respond = relay;
    const probe = await probeActiveEnv({
      mode: "remote",
      baseUrl: "http://relay.example",
      token: "",
    });
    expect(probe).toEqual({ reach: "unauthorized", relay: true });
    await expect(
      probeEnvHealth({
        mode: "remote",
        baseUrl: "http://relay.example",
        token: "",
      }),
    ).resolves.toBe("down");
  });

  it("still calls a refused agent down, and says why", async () => {
    respond = (url) =>
      url.endsWith("/v1/models")
        ? json(401, { error: { message: "unauthorized" } })
        : new Response("404 page not found\n", { status: 404 });
    const env = {
      mode: "remote" as const,
      baseUrl: "http://agent.example",
      token: "bad",
    };
    await expect(probeEnvHealth(env)).resolves.toBe("down");
    await expect(probeActiveEnv(env)).resolves.toEqual({
      reach: "unauthorized",
      relay: false,
    });
  });

  it("calls a host that is neither an agent nor a relay down", async () => {
    respond = () => new Response("nope", { status: 404 });
    await expect(
      probeEnvHealth({
        mode: "remote",
        baseUrl: "http://nothing.example",
        token: "t",
      }),
    ).resolves.toBe("down");
  });
});
