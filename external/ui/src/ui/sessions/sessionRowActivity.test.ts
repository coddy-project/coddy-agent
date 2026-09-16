import { expect, test } from "vitest";
import {
  sessionRowNeedsUserAttention,
  sessionRowShowsPermissionPending,
  sessionRowShowsQuestionPending,
  sessionRowShowsActivity,
  sessionRowShowsUnreadDot,
} from "./sessionRowActivity";
import type { SessionRow } from "./types";

const base = (id: string, o: Partial<SessionRow> = {}): SessionRow => ({
  id,
  title: "t",
  ...o,
});

const emptySets = () => ({
  permission: new Set<string>(),
  question: new Set<string>(),
});

test("activity dot for every session with an active turn, the open one included", () => {
  const sets = emptySets();
  expect(
    sessionRowShowsActivity(
      base("a", { turnActive: true }),
      sets.permission,
      sets.question,
    ),
  ).toBe(true);
  // The conversation on screen is the one the reader is most likely waiting on;
  // hiding its mark made a running turn look finished from the History list.
  expect(
    sessionRowShowsActivity(
      base("current", { turnActive: true }),
      sets.permission,
      sets.question,
    ),
  ).toBe(true);
  expect(
    sessionRowShowsActivity(
      base("a", { turnActive: false }),
      sets.permission,
      sets.question,
    ),
  ).toBe(false);
});

test("no activity dot when session awaits user attention", () => {
  const permission = new Set(["a"]);
  const question = new Set<string>();
  expect(
    sessionRowShowsActivity(
      base("a", { turnActive: true }),
      permission,
      question,
    ),
  ).toBe(false);
  expect(sessionRowNeedsUserAttention(base("a"), permission, question)).toBe(
    true,
  );
});

test("question pending icon when session id is in pending set", () => {
  const q = new Set(["a"]);
  expect(sessionRowShowsQuestionPending(base("a"), q)).toBe(true);
  expect(sessionRowShowsQuestionPending(base("b"), q)).toBe(false);
});

test("unread dot when another session has unread completion", () => {
  expect(
    sessionRowShowsUnreadDot(base("a", { unreadComplete: true }), "b"),
  ).toBe(true);
  expect(
    sessionRowShowsUnreadDot(base("a", { unreadComplete: true }), "a"),
  ).toBe(false);
});

test("permission pending from server row flag", () => {
  expect(
    sessionRowShowsPermissionPending(
      base("srv", { permissionPending: true }),
      new Set(),
    ),
  ).toBe(true);
});

test("permission pending when session id is in pending set", () => {
  const set = new Set(["a"]);
  expect(sessionRowShowsPermissionPending(base("a"), set)).toBe(true);
  expect(sessionRowShowsPermissionPending(base("b"), set)).toBe(false);
});
