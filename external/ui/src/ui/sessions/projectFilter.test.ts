import { describe, expect, test } from "vitest";
import {
  applyProjectFilter,
  isSameProject,
  isSessionProjectFilter,
} from "./projectFilter";

describe("the project filter", () => {
  const row = { cwd: "", repoRoot: "" };

  test("a row of the project: the folder, under it, or the same repository", () => {
    expect(isSameProject({ cwd: "H:/work/app" }, "H:/work/app")).toBe(true);
    expect(isSameProject({ cwd: "H:/work/app/src" }, "H:/work/app")).toBe(true);
    expect(
      isSameProject({ cwd: "H:/work/app/feature" }, "H:/work/app/feature/x"),
    ).toBe(true);
    // A shared name prefix is not a shared folder.
    expect(
      isSameProject({ cwd: "H:/work/app-feature-x" }, "H:/work/app-feature"),
    ).toBe(false);
  });

  test("a worktree session belongs to the repository's project", () => {
    expect(
      isSameProject(
        {
          cwd: "H:/wt/topic",
          repoRoot: "H:/work/app",
        },
        "H:/work/app",
      ),
    ).toBe(true);
  });

  test("another project's folder is not this one", () => {
    expect(isSameProject({ cwd: "H:/work/other" }, "H:/work/app")).toBe(false);
    expect(isSameProject({ ...row }, "H:/work/app")).toBe(false);
    expect(isSameProject({ cwd: "H:/work/other" }, "")).toBe(true);
  });

  test("the filter narrows only in force and only with a folder", () => {
    const rows = [
      { id: "1", cwd: "H:/work/app" },
      { id: "2", cwd: "H:/work/other" },
    ];
    expect(applyProjectFilter(rows, "all", "H:/work/app")).toBe(rows);
    expect(
      applyProjectFilter(rows, "project", undefined).map((r) => r.id),
    ).toEqual(["1", "2"]);
    expect(
      applyProjectFilter(rows, "project", "H:/work/app").map((r) => r.id),
    ).toEqual(["1"]);
  });

  test("cookie values are guarded", () => {
    expect(isSessionProjectFilter("project")).toBe(true);
    expect(isSessionProjectFilter("all")).toBe(true);
    expect(isSessionProjectFilter("folder")).toBe(false);
  });
});
