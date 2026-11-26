import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { CreateBranchDialog } from "./CreateBranchDialog";
import { GitGraphDialog } from "./GitGraphDialog";
import { layoutGitGraph, type GitCommit } from "./gitGraph";
import { setLocale } from "../i18n/i18n";

beforeEach(() => setLocale("en"));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("creation waits for success and leaves an error editable for retry", async () => {
  const create = vi
    .fn()
    .mockRejectedValueOnce(new Error("branch already exists"))
    .mockResolvedValue(undefined);
  const close = vi.fn();
  render(<CreateBranchDialog onCreate={create} onClose={close} />);
  const input = screen.getByRole("textbox", { name: "Branch name" });
  expect(input).toHaveFocus();
  expect(
    screen.getByRole("button", { name: "Create and switch" }),
  ).toBeDisabled();
  fireEvent.change(input, { target: { value: "feature/taken" } });
  fireEvent.submit(input.closest("form")!);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "branch already exists",
  );
  expect(close).not.toHaveBeenCalled();
  expect(input).toHaveValue("feature/taken");
  fireEvent.change(input, { target: { value: " feature/new " } });
  fireEvent.submit(input.closest("form")!);
  await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
  expect(create).toHaveBeenLastCalledWith("feature/new");
});

test("Escape cancels and Tab wraps through dialog fields", () => {
  const close = vi.fn();
  render(<CreateBranchDialog onCreate={vi.fn()} onClose={close} />);
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "feature/new" } });
  screen.getByRole("button", { name: "Create and switch" }).focus();
  fireEvent.keyDown(window, { key: "Tab" });
  expect(screen.getByRole("button", { name: "Close" })).toHaveFocus();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(close).toHaveBeenCalledTimes(1);
});

test("creation in progress keeps keyboard focus in the dialog", async () => {
  const close = vi.fn();
  let finish: () => void = () => {};
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  render(<CreateBranchDialog onCreate={() => pending} onClose={close} />);
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "feature/new" } });
  fireEvent.submit(input.closest("form")!);
  fireEvent.keyDown(window, { key: "Tab" });
  expect(screen.getByRole("dialog")).toHaveFocus();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(close).not.toHaveBeenCalled();
  finish();
  await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
});

const commit = (
  hash: string,
  parents: string[],
  refs: string[] = [],
): GitCommit => ({
  hash,
  parents,
  refs,
  subject: `Commit ${hash}`,
  author: "Developer",
  date: "2026-11-13T10:00:00Z",
});

test("graph joins both merge parents and carries the line into the root", () => {
  const rows = layoutGitGraph([
    commit("a", ["b", "c"]),
    commit("b", ["d"]),
    commit("c", ["d"]),
    commit("d", []),
  ]);
  expect(rows[0]!.edges.map(({ from, to }) => [from, to])).toEqual([
    [0, 0],
    [0, 1],
  ]);
  expect(rows[2]!.edges.map(({ from, to }) => [from, to])).toEqual([
    [0, 0],
    [1, 0],
  ]);
  expect(rows[3]!.incoming).toBe(true);
  expect(rows[3]!.edges).toEqual([]);
  expect(rows[0]!.incoming).toBe(false);
});

test("history loads another page, refreshes, and preserves labels and columns", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          commits: [commit("abcdef123", ["parent"], ["HEAD", "feature/new"])],
          has_more: true,
        }),
      ),
    )
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({ commits: [commit("parent", [])], has_more: false }),
      ),
    )
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ commits: [], has_more: false })),
    );
  vi.stubGlobal("fetch", fetcher);
  render(<GitGraphDialog path="/repo/проект" onClose={vi.fn()} />);
  expect(await screen.findByText("Commit abcdef123")).toBeInTheDocument();
  expect(screen.getByText("HEAD")).toBeInTheDocument();
  expect(screen.getByText("abcdef1")).toHaveAttribute("title", "abcdef123");
  expect(
    screen.getAllByRole("columnheader").map((cell) => cell.textContent),
  ).toEqual(["Graph", "Description", "Date", "Author", "Commit"]);
  fireEvent.click(screen.getByRole("button", { name: "Load more" }));
  expect(await screen.findByText("Commit parent")).toBeInTheDocument();
  expect(String(fetcher.mock.calls[1]![0])).toContain("offset=1");
  expect(
    new URL(
      String(fetcher.mock.calls[0]![0]),
      "http://localhost",
    ).searchParams.get("path"),
  ).toBe("/repo/проект");
  fireEvent.click(screen.getByRole("button", { name: "Refresh graph" }));
  expect(await screen.findByText("No commits yet.")).toBeInTheDocument();
  expect(screen.queryByText("Commit parent")).toBeNull();
});

test("history failure stays open and can be retried", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ commits: [], has_more: false })),
    );
  vi.stubGlobal("fetch", fetcher);
  render(<GitGraphDialog path="/repo" onClose={vi.fn()} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("offline");
  fireEvent.click(screen.getByRole("button", { name: "Refresh graph" }));
  expect(await screen.findByText("No commits yet.")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).toBeNull();
});
