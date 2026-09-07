import { afterEach, expect, test, vi } from "vitest";
import {
  getBackgroundTask,
  listBackgroundTasks,
  stopBackgroundTask,
} from "./api";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
  vi.restoreAllMocks();
});

function mockFetch(impl: typeof fetch) {
  globalThis.fetch = impl as typeof fetch;
}

test("the list call carries the session header and encodes the id", async () => {
  const seen: Array<[string, RequestInit | undefined]> = [];
  mockFetch((async (url: string, init?: RequestInit) => {
    seen.push([url, init]);
    return new Response(
      JSON.stringify({ object: "x", sessionId: "a/b", running: 0, data: [] }),
      { status: 200 },
    );
  }) as unknown as typeof fetch);

  const res = await listBackgroundTasks("a/b");
  expect(res.ok).toBe(true);
  expect(seen[0]?.[0]).toBe("/coddy/sessions/a%2Fb/background-tasks");
  expect(
    (seen[0]?.[1]?.headers as Record<string, string>)["X-Coddy-Session-ID"],
  ).toBe("a/b");
});

test("stop posts to the task's stop path", async () => {
  const seen: Array<[string, RequestInit | undefined]> = [];
  mockFetch((async (url: string, init?: RequestInit) => {
    seen.push([url, init]);
    return new Response(JSON.stringify({ task: {}, output: "" }), {
      status: 200,
    });
  }) as unknown as typeof fetch);

  await stopBackgroundTask("s1", "bg_1");
  expect(seen[0]?.[0]).toBe("/coddy/sessions/s1/background-tasks/bg_1/stop");
  expect(seen[0]?.[1]?.method).toBe("POST");
});

test("an error status becomes a result, not a throw", async () => {
  mockFetch((async () =>
    new Response(JSON.stringify({ error: { message: "no such task" } }), {
      status: 404,
    })) as unknown as typeof fetch);

  const res = await getBackgroundTask("s1", "bg_404");
  expect(res).toEqual({ ok: false, status: 404, message: "no such task" });
});

test("an unreachable server becomes a result, not an unhandled rejection", async () => {
  mockFetch((async () => {
    throw new TypeError("Failed to fetch");
  }) as unknown as typeof fetch);

  // The drawer polls on a timer, so a restarting server must not reject once
  // per tick.
  await expect(listBackgroundTasks("s1")).resolves.toEqual({
    ok: false,
    status: 0,
    message: "Coddy is not reachable",
  });
  await expect(getBackgroundTask("s1", "bg_1")).resolves.toMatchObject({
    ok: false,
    status: 0,
  });
  await expect(stopBackgroundTask("s1", "bg_1")).resolves.toMatchObject({
    ok: false,
    status: 0,
  });
});
