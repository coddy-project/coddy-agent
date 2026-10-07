import { expect, test } from "vitest";

import {
  applyGoalTurnToItems,
  goalCanPause,
  goalCanResume,
  goalTone,
  goalTurnItem,
  goalTurnText,
  isBareGoalCommand,
  isGoalCommand,
  isNewerGoal,
  parseGoalTurn,
  parseSessionGoal,
  parseSessionGoalUpdate,
  sessionGoalEventOf,
  type GoalTurnItem,
  type SessionGoal,
} from "./goal";
import { translate } from "../i18n/i18n";
import type { TranscriptItem } from "./types";

const GOAL = {
  id: "goal_1",
  objective: "Make the tests pass",
  status: "active",
  setAt: "2026-10-07T10:00:00Z",
  continuations: 2,
  maxContinuations: 10,
  checks: 3,
  activeMs: 65000,
  tokensUsed: 12345,
  lastCheck: {
    verdict: "not_met",
    reason: "two tests still fail",
    remaining: ["fix parser", "fix lexer"],
    at: "2026-10-07T10:05:00Z",
  },
  checklist: [
    { text: "parser", status: "met", evidence: "go test ./parser ok" },
    { text: "lexer", status: "not_met" },
  ],
};

test("a goal object is read whole, absent numbers as zero", () => {
  const g = parseSessionGoal(GOAL)!;
  expect(g.objective).toBe("Make the tests pass");
  expect(g.status).toBe("active");
  expect(g.continuations).toBe(2);
  expect(g.maxContinuations).toBe(10);
  expect(g.tokenBudget).toBe(0);
  expect(g.lastCheck).toEqual({
    verdict: "not_met",
    reason: "two tests still fail",
    remaining: ["fix parser", "fix lexer"],
    verified: false,
    at: "2026-10-07T10:05:00Z",
    model: "",
  });
  expect(g.checklist).toEqual([
    { text: "parser", status: "met", evidence: "go test ./parser ok" },
    { text: "lexer", status: "not_met", evidence: "" },
  ]);
  expect(parseSessionGoal({ status: "active" })).toBeNull();
  expect(parseSessionGoal("goal")).toBeNull();
  // A status this client does not know reads as a plain active goal.
  expect(parseSessionGoal({ objective: "x", status: "dreaming" })!.status).toBe(
    "active",
  );
});

test("the four deliveries share one envelope", () => {
  // GET / PATCH / DELETE /coddy/sessions/{id}/goal and the messages' `goal` field.
  const rest = parseSessionGoalUpdate({
    object: "coddy.session_goal",
    sessionId: "sess_1",
    goal: GOAL,
    version: 7,
    notice: "",
  })!;
  expect(rest.sessionId).toBe("sess_1");
  expect(rest.version).toBe(7);
  expect(rest.goal?.objective).toBe("Make the tests pass");

  // The turn stream's ACP update.
  const turn = sessionGoalEventOf(
    JSON.stringify({
      sessionUpdate: "session_goal",
      sessionId: "sess_1",
      goal: GOAL,
      version: 8,
      notice: "Goal paused: Make the tests pass",
    }),
  )!;
  expect(turn.version).toBe(8);
  expect(turn.notice).toBe("Goal paused: Make the tests pass");

  // The events stream's frame, a cleared goal.
  const cleared = sessionGoalEventOf(
    JSON.stringify({
      object: "coddy.session_goal",
      sessionId: "sess_1",
      goal: null,
      version: 9,
      notice: "Goal cleared",
    }),
  )!;
  expect(cleared.goal).toBeNull();
  expect(cleared.version).toBe(9);
});

test("a payload that names no session, carries no goal key or a broken goal is refused", () => {
  expect(parseSessionGoalUpdate({ goal: GOAL, version: 1 })).toBeNull();
  // The messages read knows its session.
  expect(parseSessionGoalUpdate({ goal: null, version: 1 }, "sess_2")).toEqual({
    sessionId: "sess_2",
    goal: null,
    version: 1,
    notice: "",
  });
  expect(parseSessionGoalUpdate({ sessionId: "s", version: 1 })).toBeNull();
  expect(
    parseSessionGoalUpdate({ sessionId: "s", goal: { status: "x" } }),
  ).toBeNull();
  expect(sessionGoalEventOf("not json")).toBeNull();
});

test("only a higher version of the viewed session replaces what is held", () => {
  const next = parseSessionGoalUpdate({
    sessionId: "sess_1",
    goal: GOAL,
    version: 5,
  })!;
  expect(isNewerGoal(4, "sess_1", next)).toBe(true);
  expect(isNewerGoal(5, "sess_1", next)).toBe(false);
  expect(isNewerGoal(6, "sess_1", next)).toBe(false);
  expect(isNewerGoal(0, "sess_other", next)).toBe(false);
  expect(isNewerGoal(0, "", next)).toBe(false);
});

test("each status has its tone and its actions", () => {
  const goal = (status: string) =>
    parseSessionGoal({ ...GOAL, status }) as SessionGoal;
  expect(goalTone("active")).toBe("active");
  expect(goalTone("paused")).toBe("muted");
  expect(goalTone("limited")).toBe("muted");
  expect(goalTone("blocked")).toBe("alert");
  expect(goalTone("complete")).toBe("done");
  expect(goalCanPause(goal("active"))).toBe(true);
  expect(goalCanResume(goal("active"))).toBe(false);
  for (const s of ["paused", "blocked", "limited"]) {
    expect(goalCanPause(goal(s))).toBe(false);
    expect(goalCanResume(goal(s))).toBe(true);
  }
  expect(goalCanResume(goal("complete"))).toBe(false);
});

test("/goal is a command as the server parses it, and bare /goal is the popover's", () => {
  expect(isGoalCommand("/goal")).toBe(true);
  expect(isGoalCommand("  /goal make it fast ")).toBe(true);
  expect(isGoalCommand("/goal resume")).toBe(true);
  expect(isGoalCommand("/goals")).toBe(false);
  expect(isGoalCommand("set a /goal")).toBe(false);
  expect(isBareGoalCommand(" /goal ")).toBe(true);
  expect(isBareGoalCommand("/goal clear")).toBe(false);
});

test("a goal turn reads from either wire shape and its row text mirrors the server's note", () => {
  const live = parseGoalTurn({
    sessionUpdate: "goal_turn",
    kind: "continue",
    index: 2,
    limit: 10,
    objective: "Make the tests pass",
    reason: "two tests still fail",
    remaining: ["fix parser"],
  })!;
  const stored = parseGoalTurn({
    kind: "continue",
    index: 2,
    limit: 10,
    objective: "Make the tests pass",
    reason: "two tests still fail",
    remaining: ["fix parser"],
  })!;
  expect(live).toEqual(stored);
  expect(goalTurnText(live, translate)).toBe(
    "Goal continuation 2 of 10: two tests still fail",
  );
  const text = (raw: Record<string, unknown>) =>
    goalTurnText(parseGoalTurn(raw)!, translate);
  expect(text({ kind: "kickoff", objective: "Ship it" })).toBe(
    "Goal set: Ship it",
  );
  expect(text({ kind: "resume", objective: "Ship it" })).toBe(
    "Goal resumed: Ship it",
  );
  expect(text({ kind: "recover", index: 1, limit: 2, reason: "stalled" })).toBe(
    "Goal recovery 1 of 2: stalled",
  );
  expect(text({ kind: "wrapup", reason: "10 continuations used" })).toBe(
    "Goal budget used up, wrapping up: 10 continuations used",
  );
  expect(text({ kind: "continue", index: 3, limit: 10 })).toBe(
    "Goal continuation 3 of 10",
  );
  expect(parseGoalTurn({ objective: "no kind" })).toBeNull();
});

function goalRow(id: string, raw: Record<string, unknown>): GoalTurnItem {
  return goalTurnItem(JSON.stringify(raw), id)!;
}

test("the kickoff row takes the place of the /goal prompt this tab sent", () => {
  const prev: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hello" },
    { id: "a1", type: "assistant_message", content: "hi" },
    {
      id: "u2",
      type: "user_message",
      content: "/goal Make the tests pass",
      createdAtUtc: "2026-10-07T10:00:00Z",
    },
  ];
  const next = applyGoalTurnToItems(
    prev,
    goalRow("goal_x", { kind: "kickoff", objective: "Make the tests pass" }),
  );
  expect(next.map((it) => it.type)).toEqual([
    "user_message",
    "assistant_message",
    "goal_turn",
  ]);
  expect((next[2] as GoalTurnItem).createdAtUtc).toBe("2026-10-07T10:00:00Z");
});

test("a continuation opens a turn after the answer before it, and a replayed row is not doubled", () => {
  const prev: TranscriptItem[] = [
    {
      id: "g1",
      type: "goal_turn",
      turn: parseGoalTurn({ kind: "kickoff", objective: "x" })!,
    },
    { id: "a1", type: "assistant_message", content: "working" },
  ];
  const cont = goalRow("g2", {
    kind: "continue",
    index: 1,
    limit: 10,
    objective: "x",
  });
  const next = applyGoalTurnToItems(prev, cont);
  expect(next.map((it) => it.id)).toEqual(["g1", "a1", "g2"]);
  // The same frame again (a relay replaying what the read held): no change.
  expect(applyGoalTurnToItems(next, { ...cont, id: "g3" })).toBe(next);
  // A message typed by hand is never replaced.
  const typed: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "please continue" },
  ];
  expect(
    applyGoalTurnToItems(
      typed,
      goalRow("g4", { kind: "kickoff", objective: "y" }),
    ).map((it) => it.type),
  ).toEqual(["user_message", "goal_turn"]);
});
