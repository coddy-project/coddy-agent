import { describe, expect, test } from "vitest";
import { resolveLatestLeaf, type BranchDataMinimal } from "./resolveLatestLeaf";

function makeFetch(map: Record<string, BranchDataMinimal | null>) {
  return async (id: string): Promise<BranchDataMinimal | null> => map[id] ?? null;
}

describe("resolveLatestLeaf", () => {
  test("returns start id when no branch points", async () => {
    const fetch = makeFetch({ a: { branchPoints: [] } });
    expect(await resolveLatestLeaf("a", fetch)).toBe("a");
  });

  test("returns start id when fetchBranches returns null", async () => {
    const fetch = makeFetch({});
    expect(await resolveLatestLeaf("a", fetch)).toBe("a");
  });

  test("follows own branch point to most recently updated child", async () => {
    const fetch = makeFetch({
      root: {
        branchPoints: [
          {
            own: true,
            sessions: [
              { sessionId: "root", lastUpdatedAt: 100 },
              { sessionId: "child1", lastUpdatedAt: 200 },
              { sessionId: "child2", lastUpdatedAt: 300 },
            ],
          },
        ],
      },
      child2: { branchPoints: [] },
    });
    expect(await resolveLatestLeaf("root", fetch)).toBe("child2");
  });

  test("ignores sibling views (own: false)", async () => {
    const fetch = makeFetch({
      a: {
        branchPoints: [
          {
            own: false,
            sessions: [
              { sessionId: "sibling1", lastUpdatedAt: 9999 },
              { sessionId: "sibling2", lastUpdatedAt: 8888 },
            ],
          },
        ],
      },
    });
    // Should not follow siblings — stays at 'a'
    expect(await resolveLatestLeaf("a", fetch)).toBe("a");
  });

  test("traverses multiple levels following own points", async () => {
    const fetch = makeFetch({
      root: {
        branchPoints: [
          {
            own: true,
            sessions: [
              { sessionId: "root", lastUpdatedAt: 100 },
              { sessionId: "child", lastUpdatedAt: 200 },
            ],
          },
        ],
      },
      child: {
        branchPoints: [
          {
            own: false,
            sessions: [{ sessionId: "sibling", lastUpdatedAt: 9999 }],
          },
          {
            own: true,
            sessions: [
              { sessionId: "child", lastUpdatedAt: 200 },
              { sessionId: "grandchild", lastUpdatedAt: 500 },
            ],
          },
        ],
      },
      grandchild: { branchPoints: [] },
    });
    expect(await resolveLatestLeaf("root", fetch)).toBe("grandchild");
  });

  test("stops when no unvisited child has higher lastUpdatedAt", async () => {
    const fetch = makeFetch({
      a: {
        branchPoints: [
          {
            own: true,
            sessions: [
              { sessionId: "a", lastUpdatedAt: 500 },
              { sessionId: "b", lastUpdatedAt: 400 },
            ],
          },
        ],
      },
      b: { branchPoints: [] },
    });
    // 'b' is the only child and its lastUpdatedAt (400) < a (500), but it's unvisited,
    // so the algorithm should still move to it.
    expect(await resolveLatestLeaf("a", fetch)).toBe("b");
  });

  test("respects maxHops limit", async () => {
    const fetch = makeFetch({
      a: {
        branchPoints: [
          { own: true, sessions: [{ sessionId: "a" }, { sessionId: "b", lastUpdatedAt: 1 }] },
        ],
      },
      b: {
        branchPoints: [
          { own: true, sessions: [{ sessionId: "b" }, { sessionId: "c", lastUpdatedAt: 2 }] },
        ],
      },
      c: {
        branchPoints: [
          { own: true, sessions: [{ sessionId: "c" }, { sessionId: "d", lastUpdatedAt: 3 }] },
        ],
      },
      d: { branchPoints: [] },
    });
    // With maxHops=2 should stop at 'c' (hops: root→b→c, then limit hit)
    expect(await resolveLatestLeaf("a", fetch, 2)).toBe("c");
    // With maxHops=10 should reach 'd'
    expect(await resolveLatestLeaf("a", fetch, 10)).toBe("d");
  });

  test("falls back to the previous session when a listed leaf is gone", async () => {
    const fetch = makeFetch({
      root: {
        branchPoints: [
          {
            own: true,
            sessions: [
              { sessionId: "root", lastUpdatedAt: 100 },
              // Deleted bundle still listed in a stale branch file: its own
              // /branches call 404s, so the walk must not land on it.
              { sessionId: "deleted", lastUpdatedAt: 300 },
            ],
          },
        ],
      },
    });
    expect(await resolveLatestLeaf("root", fetch)).toBe("root");
  });

  test("handles missing own field as undefined (legacy data — treated as not-false)", async () => {
    const fetch = makeFetch({
      root: {
        branchPoints: [
          {
            // own is undefined — should still be followed (not explicitly false)
            sessions: [
              { sessionId: "root", lastUpdatedAt: 1 },
              { sessionId: "child", lastUpdatedAt: 2 },
            ],
          },
        ],
      },
      child: { branchPoints: [] },
    });
    expect(await resolveLatestLeaf("root", fetch)).toBe("child");
  });
});
