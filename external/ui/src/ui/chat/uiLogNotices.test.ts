import { expect, test } from "vitest";
import { uiLogNoticeFeed, type RawUiLogRow } from "./uiLogNotices";

let seq = 0;
const newId = (prefix: string) => `${prefix}_${++seq}`;

/** Walks a history the way the transcript mapping does and returns where each notice lands. */
function place(roles: Array<"user" | "summary" | "assistant">, rows: RawUiLogRow[]): string[] {
  const feed = uiLogNoticeFeed(rows, newId);
  const out: string[] = [];
  roles.forEach((role, i) => {
    if (role === "user" || role === "summary") {
      for (const n of feed.beforeUserRow()) out.push(`notice:${n.message}`);
    }
    out.push(`${role}:${i}`);
  });
  for (const n of feed.end()) out.push(`notice:${n.message}`);
  return out;
}

test("the server counts compaction summaries as turns, and so does the feed", () => {
  // Two real turns and two summaries: the server stamped the error of the
  // last turn with 4, the count of every user-role message it held.
  const placed = place(
    ["user", "assistant", "summary", "summary", "user", "assistant"],
    [
      { id: "e1", level: "error", message: "first", userTurnIndex: 1, createdAt: "2026-09-23T10:00:00Z" },
      { id: "e2", level: "error", message: "HTTP 400", userTurnIndex: 4, createdAt: "2026-09-23T11:00:00Z" },
    ],
  );
  expect(placed).toEqual([
    "user:0",
    "assistant:1",
    "notice:first",
    "summary:2",
    "summary:3",
    "user:4",
    "assistant:5",
    "notice:HTTP 400",
  ]);
});

test("a notice stamped past the end of the history is still shown", () => {
  const placed = place(["user", "assistant"], [{ level: "error", message: "lost", userTurnIndex: 9 }]);
  expect(placed).toEqual(["user:0", "assistant:1", "notice:lost"]);
});

test("rows of one turn keep their time order, unknown levels and empty messages stay out", () => {
  const feed = uiLogNoticeFeed(
    [
      { level: "notice", message: "later", userTurnIndex: 1, createdAt: "2026-09-23T10:00:02Z" },
      { level: "error", message: "earlier", userTurnIndex: 1, createdAt: "2026-09-23T10:00:01Z" },
      { level: "debug", message: "hidden", userTurnIndex: 1 },
      { level: "error", message: "  ", userTurnIndex: 1 },
    ],
    newId,
  );
  expect(feed.beforeUserRow()).toEqual([]);
  expect(feed.end().map((n) => [n.level, n.message])).toEqual([
    ["error", "earlier"],
    ["notice", "later"],
  ]);
});
