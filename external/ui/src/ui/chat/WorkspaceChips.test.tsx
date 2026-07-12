import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import React from "react";
import { WorkspaceChips } from "./WorkspaceChips";
import type { WorkspaceContext } from "./workspaceContext";

const plainCtx: WorkspaceContext = {
  path: "/repos/plain",
  name: "plain",
  is_git_repo: false,
  is_worktree: false,
};

const gitCtx: WorkspaceContext = {
  path: "/repos/coddy-agent",
  name: "coddy-agent",
  is_git_repo: true,
  is_worktree: false,
  repo_root: "/repos/coddy-agent",
  branch: "main",
  branches: ["main", "feature/login"],
  worktrees: [{ path: "/repos/coddy-agent", branch: "main", main: true }],
};

function renderChips(overrides: Partial<React.ComponentProps<typeof WorkspaceChips>> = {}) {
  const props: React.ComponentProps<typeof WorkspaceChips> = {
    context: gitCtx,
    worktreePref: false,
    onPickFolder: vi.fn(),
    onPickBranch: vi.fn(),
    onWorktreeToggle: vi.fn(),
    ...overrides,
  };
  const utils = render(<WorkspaceChips {...props} />);
  return { ...utils, props };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("WorkspaceChips", () => {
  it("renders nothing without a context", () => {
    const { container } = renderChips({ context: null });
    expect(container.querySelector(".composer-context-chips")).toBeNull();
  });

  it("shows only the folder chip for a non-git workspace", () => {
    renderChips({ context: plainCtx });
    expect(screen.getByTestId("composer-workspace-chip").textContent).toContain("plain");
    expect(screen.queryByTestId("composer-branch-chip")).toBeNull();
    expect(screen.queryByTestId("composer-worktree-chip")).toBeNull();
  });

  it("shows branch and worktree chips inside a git repository", () => {
    renderChips();
    expect(screen.getByTestId("composer-branch-chip").textContent).toContain("main");
    const wt = screen.getByTestId("composer-worktree-chip");
    expect(wt.getAttribute("aria-pressed")).toBe("false");
  });

  it("marks the worktree chip active when the session lives in a worktree", () => {
    renderChips({ context: { ...gitCtx, is_worktree: true } });
    expect(
      screen.getByTestId("composer-worktree-chip").getAttribute("aria-pressed"),
    ).toBe("true");
  });

  it("toggles the worktree preference", () => {
    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-worktree-chip"));
    expect(props.onWorktreeToggle).toHaveBeenCalledTimes(1);
  });

  it("opens the branch menu and picks a branch with the worktree preference", () => {
    const { props } = renderChips({ worktreePref: true });
    fireEvent.click(screen.getByTestId("composer-branch-chip"));
    const menu = screen.getByTestId("workspace-branch-menu");
    const rows = menu.querySelectorAll("[data-testid^='workspace-branch-row-']");
    expect(rows.length).toBe(2);
    fireEvent.click(screen.getByTestId("workspace-branch-row-feature/login"));
    expect(props.onPickBranch).toHaveBeenCalledWith("feature/login", true);
  });

  it("browses folders and picks one", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        path: "/repos",
        parent: "/",
        folders: [
          { name: "coddy-agent", path: "/repos/coddy-agent" },
          { name: "other", path: "/repos/other" },
        ],
      }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    await waitFor(() => {
      expect(screen.getByTestId("workspace-folder-menu")).toBeTruthy();
      expect(screen.getByTestId("workspace-folder-row-other")).toBeTruthy();
    });
    // The picker opens at the parent of the current workspace.
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain(
      "/coddy/workspace/folders?path=" + encodeURIComponent("/repos"),
    );

    fireEvent.click(screen.getByTestId("workspace-folder-row-other"));
    expect(props.onPickFolder).toHaveBeenCalledWith("/repos/other");
  });

  it("navigates up and into folders without picking", async () => {
    const listings: Record<string, unknown> = {
      "/repos": {
        path: "/repos",
        parent: "/",
        folders: [{ name: "other", path: "/repos/other" }],
      },
      "/": {
        path: "/",
        parent: "/",
        folders: [{ name: "repos", path: "/repos" }],
      },
      "/repos/other": {
        path: "/repos/other",
        parent: "/repos",
        folders: [],
      },
    };
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      const u = new URL(String(url), "http://localhost");
      const p = u.searchParams.get("path") || "";
      return Promise.resolve({ ok: true, json: async () => listings[p] });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    await waitFor(() => screen.getByTestId("workspace-folder-row-other"));

    fireEvent.click(screen.getByTestId("workspace-folder-up"));
    await waitFor(() => screen.getByTestId("workspace-folder-row-repos"));

    fireEvent.click(screen.getByTestId("workspace-folder-browse-repos"));
    await waitFor(() => screen.getByTestId("workspace-folder-row-other"));

    expect(props.onPickFolder).not.toHaveBeenCalled();
  });
});
