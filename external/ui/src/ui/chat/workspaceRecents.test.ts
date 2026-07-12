import { beforeEach, describe, expect, it } from "vitest";
import {
  WORKSPACE_RECENTS_KEY,
  pushWorkspaceRecent,
  readWorkspaceRecents,
} from "./workspaceRecents";

describe("workspaceRecents", () => {
  beforeEach(() => {
    localStorage.removeItem(WORKSPACE_RECENTS_KEY);
  });

  it("returns empty list without stored data or on garbage", () => {
    expect(readWorkspaceRecents()).toEqual([]);
    localStorage.setItem(WORKSPACE_RECENTS_KEY, "{not json");
    expect(readWorkspaceRecents()).toEqual([]);
  });

  it("pushes most-recent first and dedupes by path", () => {
    pushWorkspaceRecent({ path: "/a", name: "a" });
    pushWorkspaceRecent({ path: "/b", name: "b" });
    pushWorkspaceRecent({ path: "/a", name: "a" });
    expect(readWorkspaceRecents().map((r) => r.path)).toEqual(["/a", "/b"]);
  });

  it("caps the list at 8 entries", () => {
    for (let i = 0; i < 12; i++) {
      pushWorkspaceRecent({ path: `/p${i}`, name: `p${i}` });
    }
    const rows = readWorkspaceRecents();
    expect(rows.length).toBe(8);
    expect(rows[0]?.path).toBe("/p11");
  });
});
