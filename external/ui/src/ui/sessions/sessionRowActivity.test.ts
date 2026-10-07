import { expect, test } from "vitest";
import {
  reconcilePermissionPendingSessionIds,
  reconcileQuestionPendingSessionIds,
  sessionRowAttentionMarker,
  sessionRowNeedsUserAttention,
  sessionRowShowsPermissionPending,
  sessionRowShowsQuestionPending,
  sessionRowShowsActivity,
  sessionRowShowsErrorSeen,
  sessionRowShowsErrorUnseen,
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

test("activity dot while background tasks run with no turn in flight", () => {
  const sets = emptySets();
  // A detached command outlives the turn that started it; a row without the
  // mark said the session was finished while its work was still going.
  expect(
    sessionRowShowsActivity(
      base("bg", { turnActive: false, backgroundRunning: 2 }),
      sets.permission,
      sets.question,
    ),
  ).toBe(true);
  expect(
    sessionRowShowsActivity(
      base("bg", { backgroundRunning: 0 }),
      sets.permission,
      sets.question,
    ),
  ).toBe(false);
});

test("no activity dot for background tasks while the row awaits attention", () => {
  expect(
    sessionRowShowsActivity(
      base("bg", { backgroundRunning: 1 }),
      new Set(["bg"]),
      new Set<string>(),
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

test("server-reported permission pending suppresses activity too", () => {
  const sets = emptySets();
  const row = base("srv", { turnActive: true, permissionPending: true });
  expect(
    sessionRowNeedsUserAttention(row, sets.permission, sets.question),
  ).toBe(true);
  expect(sessionRowShowsActivity(row, sets.permission, sets.question)).toBe(
    false,
  );
});

test("server-reported question pending suppresses activity too", () => {
  const sets = emptySets();
  const row = base("srv-question", { turnActive: true, questionPending: true });
  expect(
    sessionRowNeedsUserAttention(row, sets.permission, sets.question),
  ).toBe(true);
  expect(sessionRowShowsActivity(row, sets.permission, sets.question)).toBe(
    false,
  );
  expect(sessionRowShowsQuestionPending(row, sets.question)).toBe(true);
});

test("question pending icon when session id is in pending set", () => {
  const q = new Set(["a"]);
  expect(sessionRowShowsQuestionPending(base("a"), q)).toBe(true);
  expect(sessionRowShowsQuestionPending(base("b"), q)).toBe(false);
});

test("a refreshed false question row clears a stale local marker", () => {
  const previous = new Set(["remote"]);
  expect(
    reconcileQuestionPendingSessionIds(
      [base("remote", { questionPending: false })],
      previous,
      "",
      false,
    ),
  ).not.toContain("remote");
});

test("a refreshed false permission row clears a stale local marker", () => {
  const previous = new Set(["remote"]);
  expect(
    reconcilePermissionPendingSessionIds(
      [base("remote", { permissionPending: false })],
      previous,
      "",
      false,
    ),
  ).not.toContain("remote");
});

test("an unresolved local prompt survives until its session row is listed", () => {
  expect(
    reconcileQuestionPendingSessionIds([], new Set(), "current", true),
  ).toContain("current");
});

test("an unresolved local permission prompt survives until its session row is listed", () => {
  expect(
    reconcilePermissionPendingSessionIds([], new Set(), "current", true),
  ).toContain("current");
});

test("a settled permission yields to a refreshed question on the same session", () => {
  const row = base("remote", {
    permissionPending: false,
    questionPending: true,
  });
  const permission = reconcilePermissionPendingSessionIds(
    [row],
    new Set(["remote"]),
    "",
    false,
  );
  const question = reconcileQuestionPendingSessionIds(
    [row],
    new Set(),
    "",
    false,
  );
  expect(permission).not.toContain("remote");
  expect(sessionRowAttentionMarker(row, permission, question)).toBe("question");
});

test("permission attention takes priority over question attention", () => {
  const row = base("both", {
    permissionPending: true,
    questionPending: true,
  });
  expect(sessionRowAttentionMarker(row, new Set(), new Set())).toBe(
    "permission",
  );
  expect(sessionRowNeedsUserAttention(row, new Set(), new Set())).toBe(true);
});

test("unread dot when another session has unread completion", () => {
  expect(
    sessionRowShowsUnreadDot(base("a", { unreadComplete: true }), "b"),
  ).toBe(true);
  expect(
    sessionRowShowsUnreadDot(base("a", { unreadComplete: true }), "a"),
  ).toBe(false);
});

test("a failed turn is unseen until the activity cursor reaches its generation", () => {
  expect(
    sessionRowShowsErrorUnseen(
      base("failed", { lastErrorSeq: 4, readActivitySeq: 3 }),
      "other",
    ),
  ).toBe(true);
  expect(
    sessionRowShowsErrorUnseen(
      base("failed", { lastErrorSeq: 4, readActivitySeq: 4 }),
      "other",
    ),
  ).toBe(false);
  // The session on screen has just been acknowledged through markActivityRead,
  // so its History row never claims the failure is still unseen.
  expect(
    sessionRowShowsErrorUnseen(
      base("current", { lastErrorSeq: 4, readActivitySeq: 3 }),
      "current",
    ),
  ).toBe(false);
});

test("a viewed failed turn keeps a red outline until a successful turn clears it", () => {
  expect(
    sessionRowShowsErrorSeen(
      base("failed", { lastErrorSeq: 4, readActivitySeq: 4 }),
      "other",
    ),
  ).toBe(true);
  expect(
    sessionRowShowsErrorSeen(
      base("failed", { lastErrorSeq: 4, readActivitySeq: 3 }),
      "other",
    ),
  ).toBe(false);
  expect(
    sessionRowShowsErrorSeen(
      base("ok", { lastErrorSeq: 0, readActivitySeq: 9 }),
      "other",
    ),
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
