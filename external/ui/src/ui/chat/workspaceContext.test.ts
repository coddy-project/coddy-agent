import { describe, expect, it } from "vitest";
import {
  branchChipVisible,
  cleanPathInput,
  folderChipLabel,
  isWorktreeBadgeActive,
  pathParent,
  sortedBranches,
  worktreeForBranch,
  type WorkspaceContext,
} from "./workspaceContext";

const gitCtx: WorkspaceContext = {
  path: "/repos/coddy-agent",
  name: "coddy-agent",
  is_git_repo: true,
  is_worktree: false,
  repo_root: "/repos/coddy-agent",
  branch: "main",
  branches: ["zeta", "main", "feature/login"],
  worktrees: [
    { path: "/repos/coddy-agent", branch: "main", main: true },
    { path: "/home/.coddy/worktrees/coddy-agent/feature-login", branch: "feature/login", main: false },
  ],
};

describe("workspaceContext helpers", () => {
  it("labels the folder chip from name, path basename, or fallback", () => {
    expect(folderChipLabel(null)).toBe("workspace");
    expect(folderChipLabel(gitCtx)).toBe("coddy-agent");
    expect(
      folderChipLabel({ ...gitCtx, name: "", path: "/tmp/alpha" }),
    ).toBe("alpha");
  });

  it("shows the branch chip only inside git repositories", () => {
    expect(branchChipVisible(null)).toBe(false);
    expect(branchChipVisible({ ...gitCtx, is_git_repo: false })).toBe(false);
    expect(branchChipVisible(gitCtx)).toBe(true);
  });

  it("sorts branches with the current one first", () => {
    expect(sortedBranches(gitCtx)).toEqual(["main", "feature/login", "zeta"]);
    expect(sortedBranches({ ...gitCtx, branch: "zeta" })).toEqual([
      "zeta",
      "feature/login",
      "main",
    ]);
  });

  it("finds a non-main worktree for a branch", () => {
    expect(worktreeForBranch(gitCtx, "feature/login")?.path).toBe(
      "/home/.coddy/worktrees/coddy-agent/feature-login",
    );
    expect(worktreeForBranch(gitCtx, "main")).toBeNull();
    expect(worktreeForBranch(gitCtx, "zeta")).toBeNull();
  });

  it("marks the worktree badge active from context or preference", () => {
    expect(isWorktreeBadgeActive(null, false)).toBe(false);
    expect(isWorktreeBadgeActive(gitCtx, false)).toBe(false);
    expect(isWorktreeBadgeActive(gitCtx, true)).toBe(true);
    expect(isWorktreeBadgeActive({ ...gitCtx, is_worktree: true }, false)).toBe(true);
  });

  it("walks up posix paths", () => {
    expect(pathParent("/repos/coddy-agent")).toBe("/repos");
    expect(pathParent("/repos/coddy-agent/")).toBe("/repos");
    expect(pathParent("/repos")).toBe("/");
    expect(pathParent("/")).toBe("/");
    expect(pathParent("")).toBe("");
  });

  it("walks up windows paths without changing drive", () => {
    expect(pathParent("H:\\PycharmProjects\\work")).toBe("H:\\PycharmProjects");
    // The parent of a first-level folder is the drive root, not "H:" (which
    // means the drive's current directory) and not "/" (another volume).
    expect(pathParent("H:\\PycharmProjects")).toBe("H:\\");
    expect(pathParent("H:\\")).toBe("H:\\");
    expect(pathParent("C:/repos/app")).toBe("C:/repos");
  });

  it("stops at the top of a UNC share", () => {
    expect(pathParent("\\\\server\\share\\dir")).toBe("\\\\server\\share");
    expect(pathParent("\\\\server\\share")).toBe("\\\\server\\share");
  });

  it("cleans typed and pasted paths", () => {
    expect(cleanPathInput('  D:\\work  ')).toBe("D:\\work");
    expect(cleanPathInput('"D:\\work with spaces"')).toBe("D:\\work with spaces");
    expect(cleanPathInput('"')).toBe('"');
    expect(cleanPathInput("")).toBe("");
  });
});
