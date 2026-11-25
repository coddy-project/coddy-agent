import { afterEach, expect, it, vi } from "vitest";
import { resetConfiguredRemotesForTests } from "./configuredRemotes";
import { snapshotHealth, startActiveHealthMonitor } from "./activeHealth";
import { connectRemote, getEnv, setEnv } from "./remoteEnv";

const previousURL = "http://previous.example:12345";
const remoteURL = "http://remote.example:12345";
const requests: string[] = [];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  resetConfiguredRemotesForTests();
  requests.length = 0;
});

it("refreshes a refused token from local config after an in-place remote switch", async () => {
  vi.spyOn(window, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/coddy/config") {
      return json(200, {
        httpserver: {
          remotes: [{ name: "remote", url: remoteURL, token: "current" }],
        },
      });
    }
    if (url === `${previousURL}/v1/models`) {
      return json(200, { data: [] });
    }
    if (url === `${remoteURL}/v1/models`) {
      const authorization =
        new Headers(init?.headers).get("Authorization") ?? "";
      requests.push(authorization);
      return authorization === "Bearer current"
        ? json(200, { data: [] })
        : json(401, { error: { message: "unauthorized" } });
    }
    if (url === `${remoteURL}/swarm/info`) {
      return new Response("not a relay", { status: 404 });
    }
    return new Response("not found", { status: 404 });
  });

  setEnv({
    mode: "remote",
    baseUrl: previousURL,
    token: "previous",
    name: "previous",
  });
  startActiveHealthMonitor();
  await vi.waitFor(() => expect(snapshotHealth()).toBe("up"));

  connectRemote(remoteURL, "stale", "remote");

  await vi.waitFor(() => expect(snapshotHealth()).toBe("up"));

  expect(requests).toEqual(["Bearer stale", "Bearer current"]);
  expect(getEnv()).toMatchObject({ mode: "remote", token: "current" });
});
