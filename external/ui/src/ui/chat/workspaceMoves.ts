import type { TranscriptItem } from "./types";

/** The tools that move a session to another folder. */
const MOVING_TOOLS = new Set(["worktree_open", "worktree_exit"]);

/**
 * Finished calls of a tool that moves the chat to another folder
 * (worktree_open, worktree_exit), however they ended. The plate over the
 * composer reads where the chat works again when this count moves, so it
 * follows the move during the turn, not only after it.
 */
export function finishedWorkspaceMoves(
  items: readonly TranscriptItem[],
): number {
  let n = 0;
  for (const it of items) {
    if (
      it.type === "tool_call" &&
      it.status !== "pending" &&
      it.status !== "in_progress" &&
      MOVING_TOOLS.has(
        (it.title || "")
          .replace(/^run:\s*/i, "")
          .trim()
          .toLowerCase(),
      )
    ) {
      n += 1;
    }
  }
  return n;
}
