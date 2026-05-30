import { expect, test } from "vitest";
import {
  sessionRowNeedsUserAttention,
  sessionRowShowsPermissionPending,
  sessionRowShowsQuestionPending,
  sessionRowShowsSpinner,
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

test("spinner when another session has active turn", () => {
  const sets = emptySets();
  expect(
    sessionRowShowsSpinner(
      base("a", { turnActive: true }),
      "b",
      sets.permission,
      sets.question,
    ),
  ).toBe(true);
  expect(
    sessionRowShowsSpinner(
      base("a", { turnActive: true }),
      "a",
      sets.permission,
      sets.question,
    ),
  ).toBe(false);
  expect(
    sessionRowShowsSpinner(
      base("a", { turnActive: false }),
      "b",
      sets.permission,
      sets.question,
    ),
  ).toBe(false);
});

test("no spinner when session awaits user attention", () => {
  const permission = new Set(["a"]);
  const question = new Set<string>();
  expect(
    sessionRowShowsSpinner(
      base("a", { turnActive: true }),
      "b",
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

