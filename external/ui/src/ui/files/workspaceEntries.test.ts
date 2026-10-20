import { afterEach, beforeEach, expect, test, vi } from "vitest";

import {
  MISSING_TTL_MS,
  checkWorkspaceEntry,
  knownWorkspaceEntry,
  resetWorkspaceEntries,
} from "./workspaceEntries";

const scope = { sessionId: "s1", workspacePath: "/work/repo" };

type StatBody = { paths: string[] };

function stubStat(kinds: Record<string, string>) {
  const asked: StatBody[] = [];
  const fetchMock = vi.fn(
    async (_url: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as StatBody;
      asked.push(body);
      return new Response(
        JSON.stringify({
          object: "coddy.workspace_stat",
          entries: body.paths
            .filter((path) => kinds[path])
            .map((path) => ({ path_rel: path, kind: kinds[path] })),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, asked };
}

beforeEach(() => {
  vi.useFakeTimers();
  resetWorkspaceEntries();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

test("paths asked together go out in one request, and the answer is kept", async () => {
  const { fetchMock, asked } = stubStat({ "a.go": "file", docs: "directory" });
  const answers = Promise.all([
    checkWorkspaceEntry(scope, "a.go"),
    checkWorkspaceEntry(scope, "docs"),
    checkWorkspaceEntry(scope, "missing.go"),
    checkWorkspaceEntry(scope, "a.go"),
  ]);
  await vi.advanceTimersByTimeAsync(100);
  expect(await answers).toEqual(["file", "directory", null, "file"]);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(String(fetchMock.mock.calls[0]![0])).toBe(
    "/coddy/sessions/s1/workspace/stat",
  );
  expect(asked[0]!.paths).toEqual(["a.go", "docs", "missing.go"]);

  expect(knownWorkspaceEntry(scope, "a.go")).toBe("file");
  expect(await checkWorkspaceEntry(scope, "a.go")).toBe("file");
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("a path that was not there is asked again after a while, and at once when asked fresh", async () => {
  const kinds: Record<string, string> = {};
  const { fetchMock } = stubStat(kinds);
  const first = checkWorkspaceEntry(scope, "new.go");
  await vi.advanceTimersByTimeAsync(100);
  expect(await first).toBeNull();
  expect(knownWorkspaceEntry(scope, "new.go")).toBeNull();

  kinds["new.go"] = "file";
  const fresh = checkWorkspaceEntry(scope, "new.go", { fresh: true });
  await vi.advanceTimersByTimeAsync(100);
  expect(await fresh).toBe("file");
  expect(fetchMock).toHaveBeenCalledTimes(2);

  delete kinds["new.go"];
  const gone = checkWorkspaceEntry(scope, "new.go", { fresh: true });
  await vi.advanceTimersByTimeAsync(100);
  expect(await gone).toBeNull();
  vi.advanceTimersByTime(MISSING_TTL_MS + 1);
  expect(knownWorkspaceEntry(scope, "new.go")).toBeUndefined();
});

test("another workspace of the same chat is asked on its own", async () => {
  const { fetchMock } = stubStat({ "a.go": "file" });
  const here = checkWorkspaceEntry(scope, "a.go");
  await vi.advanceTimersByTimeAsync(100);
  await here;
  const there = checkWorkspaceEntry(
    { sessionId: "s1", workspacePath: "/work/repo/.coddy/worktrees/b" },
    "a.go",
  );
  await vi.advanceTimersByTimeAsync(100);
  expect(await there).toBe("file");
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("a request that fails is not repeated by every path that asks next", async () => {
  const fetchMock = vi.fn(
    async () => new Response("not found", { status: 404 }),
  );
  vi.stubGlobal("fetch", fetchMock);
  const failed = checkWorkspaceEntry(scope, "a.go");
  await vi.advanceTimersByTimeAsync(100);
  // Not "not there": the server was not asked, so a card does not call its
  // file deleted, and a path is no link meanwhile.
  expect(await failed).toBeUndefined();
  expect(await checkWorkspaceEntry(scope, "a.go")).toBeUndefined();
  expect(knownWorkspaceEntry(scope, "a.go")).toBeNull();
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("a long list is split into requests the server takes", async () => {
  const { asked } = stubStat({});
  const paths = Array.from({ length: 300 }, (_, i) => `f${i}.go`);
  const all = Promise.all(
    paths.map((path) => checkWorkspaceEntry(scope, path)),
  );
  await vi.advanceTimersByTimeAsync(100);
  await all;
  expect(asked.map((body) => body.paths.length)).toEqual([256, 44]);
});
