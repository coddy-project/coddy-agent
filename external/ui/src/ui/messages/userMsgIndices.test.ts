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
