import { describe, expect, test, vi } from "vitest";
import { extensionTasks } from "./tasks";

describe("the background-task API", () => {
  const responses: Record<string, { status?: number; body?: unknown }> = {
    "/coddy/sessions/s1/background-tasks": {
      body: {
        object: "coddy.background_tasks",
        sessionId: "s1",
        running: 1,
        data: [{ id: "t1", kind: "command", status: "running" }],
      },
    },
    "/coddy/sessions/s1/background-tasks/t1": {
      body: {
        object: "coddy.background_task",
        sessionId: "s1",
        task: { id: "t1", kind: "command", status: "running" },
        output: "line one",
      },
    },
    "/coddy/sessions/s1/background-tasks/t2": { status: 404 },
  };
  const calls: Array<{ url: string; method: string }> = [];
  const request = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? "GET" });
    const row = responses[url] ?? { status: 404 };
    if (row.status) return new Response(null, { status: row.status });
    return new Response(JSON.stringify(row.body), {
      headers: { "Content-Type": "application/json" },
    });
  });

  test("the listing freezes its rows and names the running count", async () => {
    const api = extensionTasks(new AbortController().signal, { request });
    const list = await api.list("s1");
    expect(list).toMatchObject({ sessionId: "s1", running: 1 });
    expect(list.tasks).toHaveLength(1);
    expect(Object.isFrozen(list.tasks[0])).toBe(true);
  });

  test("one task carries its output; a missing one resolves undefined", async () => {
    const api = extensionTasks(new AbortController().signal, { request });
    const task = await api.get("s1", "t1");
    expect(task).toMatchObject({ id: "t1", output: "line one" });
    expect(await api.get("s1", "t2")).toBeUndefined();
  });

  test("stop posts to the stop route and clear counts", async () => {
    responses["/coddy/sessions/s1/background-tasks/t1/stop"] = {
      body: { task: { id: "t1" }, output: "" },
    };
    const api = extensionTasks(new AbortController().signal, { request });
    await api.stop("s1", "t1");
    expect(calls.at(-1)).toMatchObject({
      url: "/coddy/sessions/s1/background-tasks/t1/stop",
      method: "POST",
    });
    const clearRequest = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ cleared: 2 }), {
          headers: { "Content-Type": "application/json" },
        }),
    );
    const api2 = extensionTasks(new AbortController().signal, {
      request: clearRequest,
    });
    expect(await api2.clearFinished("s1")).toBe(2);
    expect(clearRequest.mock.calls[0]![1]).toMatchObject({ method: "DELETE" });
  });

  test("the session header rides with every read", async () => {
    const api = extensionTasks(new AbortController().signal, { request });
    await api.list("s1");
    expect(
      (request.mock.calls.at(-1)?.[1] as RequestInit | undefined)?.headers,
    ).toMatchObject({ "X-Coddy-Session-ID": "s1" });
  });
});
