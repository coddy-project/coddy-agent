import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import React from "react";
import { WorkspaceChips } from "./WorkspaceChips";
import type { WorkspaceContext } from "./workspaceContext";
import { WORKSPACE_RECENTS_KEY, pushWorkspaceRecent } from "./workspaceRecents";
import { setLocale } from "../i18n/i18n";

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

function renderChips(
  overrides: Partial<React.ComponentProps<typeof WorkspaceChips>> = {},
) {
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

beforeEach(() => {
  setLocale("en");
  localStorage.removeItem(WORKSPACE_RECENTS_KEY);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setLocale("en");
});

describe("WorkspaceChips", () => {
  it("renders nothing without a context", () => {
    const { container } = renderChips({ context: null });
    expect(container.querySelector(".composer-context-chips")).toBeNull();
  });

  it("shows only the folder chip for a non-git workspace", () => {
    renderChips({ context: plainCtx });
    expect(screen.getByTestId("composer-workspace-chip").textContent).toContain(
      "plain",
    );
    expect(screen.queryByTestId("composer-branch-chip")).toBeNull();
    expect(screen.queryByTestId("composer-worktree-chip")).toBeNull();
  });

  it("renders the worktree control as a real checkbox", () => {
    renderChips();
    const box = screen.getByTestId(
      "composer-worktree-checkbox",
    ) as HTMLInputElement;
    expect(box.type).toBe("checkbox");
    expect(box.checked).toBe(false);
  });

  it("checks and disables the worktree checkbox when the session lives in a worktree", () => {
    renderChips({ context: { ...gitCtx, is_worktree: true } });
    const box = screen.getByTestId(
      "composer-worktree-checkbox",
    ) as HTMLInputElement;
    expect(box.checked).toBe(true);
    expect(box.disabled).toBe(true);
  });

  it("toggles the worktree preference through the checkbox", () => {
    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-worktree-checkbox"));
    expect(props.onWorktreeToggle).toHaveBeenCalledTimes(1);
  });

  it("opens the branch menu and picks a branch with the worktree preference", () => {
    const { props } = renderChips({ worktreePref: true });
    fireEvent.click(screen.getByTestId("composer-branch-chip"));
    const menu = screen.getByTestId("workspace-branch-menu");
    const rows = menu.querySelectorAll(
      "[data-testid^='workspace-branch-row-']",
    );
    expect(rows.length).toBe(2);
    fireEvent.click(screen.getByTestId("workspace-branch-row-feature/login"));
    expect(props.onPickBranch).toHaveBeenCalledWith("feature/login", true);
  });

  it("locks every control once the conversation started", () => {
    renderChips({ locked: true });
    expect(
      (screen.getByTestId("composer-workspace-chip") as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      (screen.getByTestId("composer-branch-chip") as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      (screen.getByTestId("composer-worktree-checkbox") as HTMLInputElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    expect(screen.queryByTestId("workspace-folder-menu")).toBeNull();
  });

  it("lists recent folders with the current workspace checked", () => {
    pushWorkspaceRecent({ path: "/repos/other", name: "other" });
    renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    const menu = screen.getByTestId("workspace-folder-menu");
    expect(menu.textContent).toContain("Recent");
    const current = screen.getByTestId("workspace-recent-row-coddy-agent");
    expect(current.className).toContain("is-selected");
    expect(screen.getByTestId("workspace-recent-row-other")).toBeTruthy();
  });

  it("localizes workspace controls in Russian", () => {
    setLocale("ru");
    renderChips({ context: { ...gitCtx, branch: "" } });

    expect(screen.getByTestId("composer-branch-chip")).toHaveTextContent(
      "отсоединённая",
    );
    expect(screen.getByTestId("composer-worktree-chip")).toHaveTextContent(
      "рабочее дерево",
    );
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    expect(screen.getByTestId("workspace-folder-menu")).toHaveTextContent(
      "Недавние",
    );
    expect(screen.getByTestId("workspace-open-folder")).toHaveTextContent(
      "Открыть папку…",
    );
  });

  it("picks a recent folder and remembers it", () => {
    pushWorkspaceRecent({ path: "/repos/other", name: "other" });
    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    fireEvent.click(screen.getByTestId("workspace-recent-row-other"));
    expect(props.onPickFolder).toHaveBeenCalledWith("/repos/other");
  });

  it("opens the folder browser modal from 'Open folder…' and picks a browsed folder", async () => {
    const listings: Record<string, unknown> = {
      "/repos": {
        path: "/repos",
        parent: "/",
        folders: [{ name: "other", path: "/repos/other" }],
      },
      "/repos/other": {
        path: "/repos/other",
        parent: "/repos",
        folders: [],
      },
      "/": {
        path: "/",
        parent: "/",
        folders: [{ name: "repos", path: "/repos" }],
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
    fireEvent.click(screen.getByTestId("workspace-open-folder"));

    // The browser starts at the parent of the current workspace.
    await waitFor(() => screen.getByTestId("workspace-folder-modal"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-other"));

    // Navigate up and back down, then open the browsed folder.
    fireEvent.click(screen.getByTestId("workspace-modal-up"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-repos"));
    fireEvent.click(screen.getByTestId("workspace-modal-row-repos"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-other"));
    fireEvent.click(screen.getByTestId("workspace-modal-row-other"));
    await waitFor(() =>
      expect(screen.getByTestId("workspace-modal-open").textContent).toContain(
        "Open",
      ),
    );

    fireEvent.click(screen.getByTestId("workspace-modal-open"));
    expect(props.onPickFolder).toHaveBeenCalledWith("/repos/other");
    expect(screen.queryByTestId("workspace-folder-modal")).toBeNull();
  });

  it("reaches other drives through the drive level and a typed path", async () => {
    // Windows-shaped server: a drive root has no parent directory, so the
    // listing points at the ":drives:" volume level instead.
    const winCtx: WorkspaceContext = {
      path: "H:\\repos\\app",
      name: "app",
      is_git_repo: false,
      is_worktree: false,
    };
    const listings: Record<string, unknown> = {
      "H:\\repos": {
        path: "H:\\repos",
        parent: "H:\\",
        folders: [{ name: "app", path: "H:\\repos\\app" }],
      },
      "H:\\": {
        path: "H:\\",
        parent: ":drives:",
        folders: [{ name: "repos", path: "H:\\repos" }],
      },
      ":drives:": {
        path: ":drives:",
        parent: ":drives:",
        drives: true,
        folders: [
          { name: "C:", path: "C:\\" },
          { name: "H:", path: "H:\\" },
        ],
      },
      "C:\\": {
        path: "C:\\",
        parent: ":drives:",
        folders: [{ name: "Users", path: "C:\\Users" }],
      },
      "D:\\work": {
        path: "D:\\work",
        parent: "D:\\",
        folders: [],
      },
    };
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      const u = new URL(String(url), "http://localhost");
      const p = u.searchParams.get("path") || "";
      const hit = listings[p];
      return Promise.resolve({ ok: Boolean(hit), json: async () => hit });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { props } = renderChips({ context: winCtx });
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    fireEvent.click(screen.getByTestId("workspace-open-folder"));

    // Starts at the parent of the workspace on the same drive, not at "/".
    await waitFor(() => screen.getByTestId("workspace-modal-row-app"));
    expect(screen.getByTestId("workspace-modal-path")).toHaveProperty(
      "value",
      "H:\\repos",
    );

    // Up to the drive root, then up again into the drive list.
    fireEvent.click(screen.getByTestId("workspace-modal-up"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-repos"));
    fireEvent.click(screen.getByTestId("workspace-modal-up"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-C:"));

    // The drive level is a place to navigate, not a workspace to open.
    expect(screen.getByTestId("workspace-modal-open")).toHaveProperty(
      "disabled",
      true,
    );
    expect(screen.queryByTestId("workspace-modal-up")).toBeNull();

    fireEvent.click(screen.getByTestId("workspace-modal-row-C:"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-Users"));
    expect(props.onPickFolder).not.toHaveBeenCalled();

    // A pasted path jumps to a third drive; the button says what it will do.
    const field = screen.getByTestId("workspace-modal-path");
    fireEvent.change(field, { target: { value: '"D:\\work"' } });
    expect(screen.getByTestId("workspace-modal-open").textContent).toBe("Go");
    fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() =>
      expect(screen.getByTestId("workspace-modal-open").textContent).toBe(
        "Open",
      ),
    );

    fireEvent.click(screen.getByTestId("workspace-modal-open"));
    expect(props.onPickFolder).toHaveBeenCalledWith("D:\\work");
  });

  it("keeps the most recently requested folder when an older listing arrives late", async () => {
    let resolveRoot: (response: unknown) => void = () => {};
    const rootResponse = new Promise((resolve) => {
      resolveRoot = resolve;
    });
    const listings: Record<string, unknown> = {
      "/repos": {
        path: "/repos",
        parent: "/",
        folders: [{ name: "other", path: "/repos/other" }],
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
      if (p === "/") {
        return rootResponse;
      }
      return Promise.resolve({ ok: true, json: async () => listings[p] });
    });
    vi.stubGlobal("fetch", fetchMock);

    renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    fireEvent.click(screen.getByTestId("workspace-open-folder"));
    await waitFor(() => screen.getByTestId("workspace-modal-row-other"));

    // The slow parent request starts first. The old listing is still visible,
    // so a user can navigate into a child before that response comes back.
    fireEvent.click(screen.getByTestId("workspace-modal-up"));
    fireEvent.click(screen.getByTestId("workspace-modal-row-other"));
    await waitFor(() =>
      expect(screen.getByTestId("workspace-modal-path")).toHaveProperty(
        "value",
        "/repos/other",
      ),
    );

    resolveRoot({
      ok: true,
      json: async () => ({
        path: "/",
        parent: "/",
        folders: [{ name: "repos", path: "/repos" }],
      }),
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByTestId("workspace-modal-path")).toHaveProperty(
      "value",
      "/repos/other",
    );
  });

  it("cancels the folder browser modal without picking", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ path: "/repos", parent: "/", folders: [] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const { props } = renderChips();
    fireEvent.click(screen.getByTestId("composer-workspace-chip"));
    fireEvent.click(screen.getByTestId("workspace-open-folder"));
    await waitFor(() => screen.getByTestId("workspace-folder-modal"));
    fireEvent.click(screen.getByTestId("workspace-modal-cancel"));
    expect(screen.queryByTestId("workspace-folder-modal")).toBeNull();
    expect(props.onPickFolder).not.toHaveBeenCalled();
  });
});
