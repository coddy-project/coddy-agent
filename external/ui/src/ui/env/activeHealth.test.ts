import { afterEach, describe, expect, it, vi } from "vitest";
import { probeEnvHealth } from "./activeHealth";

describe("probeEnvHealth", () => {
  it("reports the local environment as up without any network call", async () => {
    expect(await probeEnvHealth({ mode: "local" })).toBe("up");
  });
});

// probeEnvHealth reaches the network through localFetch, which captures the
// native fetch at module load, so the module is mocked rather than the global.
const seen: string[] = [];
let respond: (url: string) => Response = () =>
  ({ ok: true, status: 200 }) as Response;

vi.mock("./remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./remoteEnv")>();
  return {
    ...actual,
    localFetch: (input: RequestInfo | URL) => {
      const url = String(input);
      seen.push(url);
      return Promise.resolve(respond(url));
    },
  };
});

describe("probeEnvHealth against a relay", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    seen.length = 0;
  });

  // A relay serves no model catalog of its own, so the usual probe would call a
  // healthy relay unreachable and the app would offer to switch away from it.
  it("asks whether a host without a model catalog is a swarm", async () => {
    respond = (url) => {
      if (url.endsWith("/v1/models")) {
        return { ok: false, status: 404 } as Response;
      }
      if (url.endsWith("/swarm/info")) {
        return { ok: true, status: 200 } as Response;
      }
      return { ok: false, status: 500 } as Response;
    };
    const health = await probeEnvHealth({
      mode: "remote",
      baseUrl: "http://relay.example",
      token: "t",
    });
    expect(health).toBe("up");
    expect(seen.some((u) => u.endsWith("/swarm/info"))).toBe(true);
  });

  it("still calls a refused environment down without a second question", async () => {
    respond = () => ({ ok: false, status: 401 }) as Response;
    const health = await probeEnvHealth({
      mode: "remote",
      baseUrl: "http://agent.example",
      token: "bad",
    });
    expect(health).toBe("down");
    expect(seen.some((u) => u.endsWith("/swarm/info"))).toBe(false);
  });

  it("calls a host that is neither an agent nor a relay down", async () => {
    respond = () => ({ ok: false, status: 404 }) as Response;
    await expect(
      probeEnvHealth({
        mode: "remote",
        baseUrl: "http://nothing.example",
        token: "t",
      }),
    ).resolves.toBe("down");
  });
});
