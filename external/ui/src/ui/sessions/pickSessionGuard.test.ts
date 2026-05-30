import { expect, test } from "vitest";
import { isRedundantSessionPick } from "./pickSessionGuard";

test("same real session id is a redundant pick", () => {
  expect(isRedundantSessionPick("sess-abc", "sess-abc")).toBe(true);
});

test("same id with surrounding whitespace is still redundant", () => {
  expect(isRedundantSessionPick("  sess-abc  ", " sess-abc")).toBe(true);
});

test("different session id is not redundant", () => {
  expect(isRedundantSessionPick("sess-b", "sess-a")).toBe(false);
});

test("picking a session when none is active is not redundant", () => {
  expect(isRedundantSessionPick("sess-a", "")).toBe(false);
});

test("draft session is never a redundant pick even when ids match", () => {
  expect(isRedundantSessionPick("draft_abc123", "draft_abc123")).toBe(false);
});
