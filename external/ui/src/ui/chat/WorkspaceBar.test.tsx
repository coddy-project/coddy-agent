import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { WorkspaceBar, middleTruncate } from "./WorkspaceBar";
import type { WorkspaceContext } from "./workspaceContext";
import type { WorkingCopy } from "../changes/workingCopy";
import { initLocale } from "../i18n/i18n";

/**
 * The bar over the composer of a running chat names where the chat works: the
 * repository, the branch - with a worktree mark when the chat runs in a linked
 * worktree - and what git reports as changed, which opens the edits.
 */

afterEach(() => {
  cleanup();
  initLocale("en");
});

const repo: WorkspaceContext = {
  path: "/home/me/src/coddy-agent",
  name: "coddy-agent",
  is_git_repo: true,
  is_worktree: false,
  repo_root: "/home/me/src/coddy-agent",
  branch: "main",
};

const worktree: WorkspaceContext = {
  path: "/home/me/src/coddy-agent/.coddy/worktrees/feat-session-changes",
  name: "feat-session-changes",
  is_git_repo: true,
  is_worktree: true,
  repo_root: "/home/me/src/coddy-agent",
  branch: "feat/session-changes",
};

function copy(files: number, additions = 0, deletions = 0, vcs = "git"): WorkingCopy {
  return {
    loaded: true,
    error: "",
    changes: {
      sessionId: "s1",
      vcs,
      files: [],
      totals: { files, additions, deletions },
    },
  };
}

test("a repository: its name, the branch, and no worktree mark", () => {
  render(<WorkspaceBar context={repo} workingCopy={copy(0)} editsOpen={false} />);
  const name = screen.getByTestId("workspace-bar-repo");
  expect(name.textContent).toBe("coddy-agent");
  expect(name.getAttribute("title")).toBe("/home/me/src/coddy-agent");
  const branch = screen.getByTestId("workspace-bar-branch");
  expect(branch.textContent).toBe("main");
  expect(branch.getAttribute("title")).toBe("Branch main");
  expect(screen.queryByTestId("workspace-bar-worktree")).toBeNull();
  expect(screen.queryByTestId("workspace-bar-edits")).toBeNull();
});

test("a linked worktree: the repository's name and a worktree mark before the branch", () => {
  render(<WorkspaceBar context={worktree} workingCopy={copy(0)} editsOpen={false} />);
  expect(screen.getByTestId("workspace-bar-repo").textContent).toBe("coddy-agent");
  const branch = screen.getByTestId("workspace-bar-branch");
  const mark = screen.getByTestId("workspace-bar-worktree");
  expect(branch.firstElementChild).toBe(mark);
  expect(branch.getAttribute("title")).toBe(
    "Worktree /home/me/src/coddy-agent/.coddy/worktrees/feat-session-changes on branch feat/session-changes",
  );
});

test("what git reports opens the edits, and says whether they are on show", () => {
  const onOpenEdits = vi.fn();
  const { rerender } = render(
    <WorkspaceBar context={repo} workingCopy={copy(3, 18267, 280)} editsOpen={false} onOpenEdits={onOpenEdits} />,
  );
  const edits = screen.getByTestId("workspace-bar-edits");
  expect(edits.textContent).toBe("+18,267−280");
  expect(edits.getAttribute("aria-label")).toBe("Show the edits: 3 files changed");
  expect(edits.getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(edits);
  expect(onOpenEdits).toHaveBeenCalledTimes(1);
  rerender(
    <WorkspaceBar context={repo} workingCopy={copy(3, 18267, 280)} editsOpen onOpenEdits={onOpenEdits} />,
  );
  expect(screen.getByTestId("workspace-bar-edits").getAttribute("aria-pressed")).toBe("true");
});

test("a folder in no repository has a name and nothing else", () => {
  const plain: WorkspaceContext = { path: "/tmp/demo", name: "demo", is_git_repo: false, is_worktree: false };
  render(<WorkspaceBar context={plain} workingCopy={copy(0, 0, 0, "")} editsOpen={false} onOpenEdits={() => {}} />);
  expect(screen.getByTestId("workspace-bar-repo").textContent).toBe("demo");
  expect(screen.queryByTestId("workspace-bar-branch")).toBeNull();
  expect(screen.queryByTestId("workspace-bar-edits")).toBeNull();
});

test("the counts follow the language of the page", () => {
  initLocale("ru");
  render(<WorkspaceBar context={repo} workingCopy={copy(2, 18267, 280)} editsOpen={false} onOpenEdits={() => {}} />);
  const edits = screen.getByTestId("workspace-bar-edits");
  expect(edits.textContent).toBe("+18 267−280");
  expect(edits.getAttribute("aria-label")).toBe("Показать правки: изменено 2 файла");
});

test("a cut is never longer than asked, whatever the room", () => {
  for (const max of [1, 2, 3, 4, 5, 6]) {
    const cut = middleTruncate("feature/a-very-long-branch-name", max);
    expect(cut.length).toBeLessThanOrEqual(max);
    expect(cut.length).toBeGreaterThan(0);
  }
});

test("a long branch keeps both ends", () => {
  expect(middleTruncate("main", 24)).toBe("main");
  const long = "feature/a-very-long-branch-name-for-the-bar";
  const cut = middleTruncate(long, 24);
  expect(cut.length).toBe(24);
  expect(cut.startsWith("feature/a-")).toBe(true);
  expect(cut.endsWith("-for-the-bar")).toBe(true);
  expect(cut).toContain("…");
});
