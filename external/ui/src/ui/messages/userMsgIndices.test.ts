import { expect, test } from "vitest";

import type { TranscriptItem } from "../chat/types";
import { userMsgIndices } from "./userMsgIndices";

function user(id: string): TranscriptItem {
  return { id, type: "user_message", content: id };
}

function wake(id: string): TranscriptItem {
  return { id, type: "background_wake", tasks: [] };
}

function assistant(id: string): TranscriptItem {
  return { id, type: "assistant_message", content: id };
}

test("user messages are indexed in order, counting from zero", () => {
  const items = [user("u0"), assistant("a0"), user("u1"), user("u2")];
  const m = userMsgIndices(items);
  expect(m.get("u0")).toBe(0);
  expect(m.get("u1")).toBe(1);
  expect(m.get("u2")).toBe(2);
  expect(m.size).toBe(3);
});

test("a wake takes an index of its own, so a rewind after it lands on its message", () => {
  const items = [user("u0"), wake("w1"), user("u2")];
  const m = userMsgIndices(items);
  // The wake is a turn nobody typed: u2 is the second user index the server
  // counts, not the first.
  expect(m.get("u0")).toBe(0);
  expect(m.get("u2")).toBe(2);
});

test("a goal turn takes an index of its own too, and none of its rows is an editable prompt", () => {
  const goal = (id: string): TranscriptItem => ({
    id,
    type: "goal_turn",
    turn: {
      kind: "continue",
      index: 1,
      limit: 10,
      objective: "ship",
      reason: "",
      remaining: [],
    },
  });
  const items = [user("u0"), goal("g1"), wake("w2"), goal("g3"), user("u4")];
  const m = userMsgIndices(items, 5);
  expect(m.get("u0")).toBe(5);
  // Three turns nobody typed stand between the two prompts, as the server
  // counts its user-role messages.
  expect(m.get("u4")).toBe(9);
  expect(m.has("g1")).toBe(false);
  expect(m.has("g3")).toBe(false);
  expect(m.size).toBe(2);
});
