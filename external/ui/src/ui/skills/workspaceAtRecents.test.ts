import { afterEach, expect, test, vi } from "vitest";
import {
  migrateWorkspaceAtRecents,
  pickerRowFromRecent,
  readWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "./workspaceAtRecents";

const store = new Map<string, string>();

afterEach(() => {
  store.clear();
  vi.unstubAllGlobals();
});

function stubStorage() {
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => {
      store.set(k, v);
    },
    removeItem: (k: string) => {
      store.delete(k);
    },
  });
}

test("record and read MRU order with dedupe", () => {
  stubStorage();
  recordWorkspaceAtRecent("sess-a", {
    path_rel: "a.go",
    kind: "file",
  });
  recordWorkspaceAtRecent("sess-a", { path_rel: "b.go", kind: "file" });
  recordWorkspaceAtRecent("sess-a", { path_rel: "a.go", kind: "file" });
  const rows = readWorkspaceAtRecents("sess-a");
  expect(rows.map((r) => r.path_rel)).toEqual(["a.go", "b.go"]);
});

test("dir paths keep trailing slash in storage", () => {
  stubStorage();
  recordWorkspaceAtRecent("s", { path_rel: "pkg", kind: "dir" });
  expect(readWorkspaceAtRecents("s")).toEqual([
    { path_rel: "pkg/", kind: "dir" },
  ]);
});

test("migrate merges into target and drops source", () => {
  stubStorage();
  recordWorkspaceAtRecent("old", { path_rel: "x.go", kind: "file" });
  recordWorkspaceAtRecent("new", { path_rel: "y.go", kind: "file" });
  migrateWorkspaceAtRecents("old", "new");
  expect(readWorkspaceAtRecents("old")).toEqual([]);
  expect(readWorkspaceAtRecents("new").map((r) => r.path_rel)).toEqual([
    "x.go",
    "y.go",
  ]);
});

test("no_session key is a valid bucket", () => {
  stubStorage();
  recordWorkspaceAtRecent(WORKSPACE_AT_RECENTS_NO_SESSION_KEY, {
    path_rel: "z.go",
    kind: "file",
  });
  expect(readWorkspaceAtRecents(WORKSPACE_AT_RECENTS_NO_SESSION_KEY)).toHaveLength(
    1,
  );
});

test("pickerRowFromRecent builds basename name", () => {
  const r = pickerRowFromRecent({
    path_rel: "src/ui/App.tsx",
    kind: "file",
  });
  expect(r.path_rel).toBe("src/ui/App.tsx");
  expect(r.name).toBe("App.tsx");
  expect(r.kind).toBe("file");
});
