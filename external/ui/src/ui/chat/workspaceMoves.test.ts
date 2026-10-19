import { expect, test } from "vitest";
import type { TranscriptItem } from "./types";
import { finishedWorkspaceMoves } from "./workspaceMoves";

const call = (
  title: string,
  status: "pending" | "in_progress" | "completed" | "failed" | "cancelled",
): TranscriptItem => ({
  id: `${title}-${status}`,
  type: "tool_call",
  toolCallId: `${title}-${status}`,
  title,
  status,
});

test("only finished calls of the tools that move the chat count", () => {
  expect(
    finishedWorkspaceMoves([
      call("worktree_open", "completed"),
      call("Run: worktree_exit", "failed"),
      call("worktree_open", "in_progress"),
      call("worktree_open", "pending"),
      call("worktree_list", "completed"),
      call("run_command", "completed"),
    ]),
  ).toBe(2);
  expect(finishedWorkspaceMoves([])).toBe(0);
});
