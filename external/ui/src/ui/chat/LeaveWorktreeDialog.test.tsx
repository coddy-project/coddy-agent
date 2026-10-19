import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import {
  LeaveWorktreeDialog,
  leaveWorktreeCommand,
  type LeaveWorktreeChoice,
} from "./LeaveWorktreeDialog";
import type { SessionWorktree } from "./worktreeApi";
import { initLocale } from "../i18n/i18n";

/**
 * Leaving a worktree from the plate asks first, keep it or remove it, with
 * what removing would touch in view; keeping is the default answer.
 */

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
});

const base: SessionWorktree = {
  in_worktree: true,
  path: "/home/me/src/app/.coddy/worktrees/feature-login",
  branch: "feature/login",
  main_checkout: "/home/me/src/app",
  managed: true,
  uncommitted: 0,
  unpushed: 0,
  locked: false,
  submodules: false,
  running_tasks: 0,
  removable: true,
  needs_discard: false,
};

function answering(body: SessionWorktree | null) {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe("/coddy/sessions/sess_wt/worktree");
      if (body === null) await gate;
      return new Response(JSON.stringify(body ?? base), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  return release;
}

function open(onChoose: (c: LeaveWorktreeChoice) => void = () => {}) {
  render(
    <LeaveWorktreeDialog
      open
      sessionId="sess_wt"
      project="app"
      onChoose={onChoose}
      onClose={() => {}}
    />,
  );
}

const remove = () => screen.getByTestId("leave-worktree-remove");
const keep = () => screen.getByRole("button", { name: "Keep worktree" });

test("while the worktree is read, keeping is possible and removing is not", async () => {
  const release = answering(null);
  open();
  expect(screen.getByText("Checking the worktree…")).toBeTruthy();
  expect(keep()).not.toBeDisabled();
  expect(remove()).toBeDisabled();
  release();
});

test("keeping is the default answer and sends the plain way back", async () => {
  answering(base);
  const onChoose = vi.fn();
  open(onChoose);
  await waitFor(() => expect(remove()).not.toBeDisabled());
  expect(
    screen.getByText("The chat goes back to the main checkout app."),
  ).toBeTruthy();
  expect(screen.getByText("Nothing uncommitted")).toBeTruthy();
  fireEvent.click(keep());
  expect(onChoose).toHaveBeenCalledWith("keep");
});

test("uncommitted work is named, and removing it says it discards and keeps a snapshot", async () => {
  answering({ ...base, uncommitted: 3, unpushed: 2, needs_discard: true });
  const onChoose = vi.fn();
  open(onChoose);
  await waitFor(() =>
    expect(remove()).toHaveTextContent("Discard changes and remove"),
  );
  expect(screen.getByText("3 uncommitted changes")).toBeTruthy();
  expect(
    screen.getByText(
      "2 commits not pushed: the branch stays unless it is merged",
    ),
  ).toBeTruthy();
  expect(screen.getByTestId("leave-worktree-snapshot")).toBeTruthy();
  fireEvent.click(remove());
  expect(onChoose).toHaveBeenCalledWith("discard");
});

test("another chat in the worktree stops the removal and is named", async () => {
  answering({
    ...base,
    removable: false,
    other_sessions: [{ id: "sess_b", title: "Fix the login form" }],
    refusal: "1 other session(s) still work in it",
  });
  open();
  await waitFor(() =>
    expect(
      screen.getByText(
        "Other chats work here too (Fix the login form), so it cannot be removed now",
      ),
    ).toBeTruthy(),
  );
  expect(remove()).toBeDisabled();
  expect(remove()).toHaveTextContent("Remove worktree");
});

test("a repository of its own inside the worktree stops the removal and is named", async () => {
  answering({
    ...base,
    uncommitted: 1,
    removable: false,
    nested: ["vendor/dep"],
    refusal: "it holds git repositories of its own",
  });
  open();
  await waitFor(() =>
    expect(
      screen.getByText(
        "It holds git repositories of its own (vendor/dep): move them out before removing it",
      ),
    ).toBeTruthy(),
  );
  expect(remove()).toBeDisabled();
});

test("a chat no longer in a worktree is told so, with nothing to remove", async () => {
  answering({
    in_worktree: false,
    managed: false,
    uncommitted: 0,
    unpushed: 0,
    locked: false,
    submodules: false,
    running_tasks: 0,
    removable: false,
    needs_discard: false,
  });
  open();
  await waitFor(() =>
    expect(
      screen.getByText(
        "The chat is no longer in a worktree, or its folder is gone",
      ),
    ).toBeTruthy(),
  );
  expect(screen.getByTestId("leave-worktree-facts").children).toHaveLength(1);
  expect(remove()).toBeDisabled();
});

// A turn that was running when the dialog opened can move the chat into
// another worktree: the dialog reads the one it is in now, so its facts and
// its answer are about that one.
test("the dialog reads the worktree again when the chat's folder moves under it", async () => {
  const fetched = vi.fn(
    async () =>
      new Response(
        JSON.stringify(
          fetched.mock.calls.length > 1
            ? {
                ...base,
                branch: "feature/next",
                uncommitted: 2,
                needs_discard: true,
              }
            : base,
        ),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
  );
  vi.stubGlobal("fetch", fetched);
  const props = {
    open: true,
    sessionId: "sess_wt",
    project: "app",
    onChoose: () => {},
    onClose: () => {},
  };
  const view = render(
    <LeaveWorktreeDialog
      {...props}
      worktreePath="/src/app/.coddy/worktrees/feature-login"
    />,
  );
  await waitFor(() =>
    expect(screen.getByText("Branch feature/login")).toBeTruthy(),
  );
  view.rerender(
    <LeaveWorktreeDialog
      {...props}
      worktreePath="/src/app/.coddy/worktrees/feature-next"
    />,
  );
  await waitFor(() =>
    expect(screen.getByText("Branch feature/next")).toBeTruthy(),
  );
  expect(fetched).toHaveBeenCalledTimes(2);
  expect(remove()).toHaveTextContent("Discard changes and remove");
});

test("each answer is the command the console types too", () => {
  expect(leaveWorktreeCommand("keep")).toBe("/worktree exit --keep");
  expect(leaveWorktreeCommand("remove")).toBe("/worktree exit --remove");
  expect(leaveWorktreeCommand("discard")).toBe(
    "/worktree exit --remove --force",
  );
});
