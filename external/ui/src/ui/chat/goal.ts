/**
 * The session goal (`internal/session/goal.go`, docs/features/session-supervisor.md):
 * an objective the session supervisor keeps the agent working on, turn after
 * turn, until a check finds it met. The server publishes it whole, with a
 * version, whenever it changes - set, paused, checked, continued, cleared - and
 * every place a client meets it carries the same envelope
 * `{sessionId, goal, version, notice}`:
 *
 * - `GET`, `PATCH` and `DELETE /coddy/sessions/{id}/goal` answer with it;
 * - `GET /coddy/sessions/{id}/messages` carries it as `goal`;
 * - the turn stream names it `event: session_goal` (the ACP update, with
 *   `sessionUpdate: "session_goal"` in it);
 * - `GET /coddy/events` names it `event: session_goal` as well.
 *
 * `goal: null` means the session has no goal. A client keeps the highest
 * version it has seen, like the settings snapshot (chat/sessionSettings.ts).
 *
 * The supervisor also starts turns nobody typed. Their first user-role message
 * is the instruction the model reads; the transcript shows a compact goal row
 * in its place (`goal_turn`: the `event: goal_turn` frame live, the message's
 * `goal_turn` field after a reload).
 */

import { opensTurn } from "./backgroundWake";
import type { TranscriptItem } from "./types";

export const GOAL_STATUSES = [
  "active",
  "paused",
  "blocked",
  "complete",
  "limited",
] as const;
export type GoalStatus = (typeof GOAL_STATUSES)[number];

/** The longest objective the server takes (runes; `NewGoal` refuses more). */
export const GOAL_OBJECTIVE_MAX = 4000;

/** One supervisor verdict. */
export type GoalCheck = {
  /** met, not_met, needs_user or impossible. */
  verdict: string;
  reason: string;
  remaining: string[];
  /** A met verdict the verifier confirmed by reading the workspace itself. */
  verified: boolean;
  at: string;
  model: string;
};

/** One requirement of the objective the supervisor tracks. */
export type GoalChecklistItem = {
  text: string;
  /** met, not_met or unverified. */
  status: string;
  evidence: string;
};

export type SessionGoal = {
  id: string;
  objective: string;
  status: GoalStatus;
  /** Why a goal is paused, blocked or limited. */
  statusReason: string;
  setAt: string;
  updatedAt: string;
  /** Automatic turns used, out of maxContinuations. */
  continuations: number;
  maxContinuations: number;
  checks: number;
  activeMs: number;
  tokensUsed: number;
  /** 0 when there is no token budget. */
  tokenBudget: number;
  lastCheck: GoalCheck | null;
  checklist: GoalChecklistItem[];
  /** The model and the reasoning level /goal --model and --reasoning chose to
   * check this goal; empty when it follows the configuration. */
  model: string;
  reasoning: string;
  /** What the next check runs on, as the server resolved it: the model, and
   * its level - the goal's own, else the model's default, "default" when the
   * model configures none, empty when it has no levels. Empty from a server
   * that does not send them. */
  checkModel: string;
  checkReasoning: string;
};

/** The envelope every surface of the server delivers a goal in. */
export type SessionGoalUpdate = {
  sessionId: string;
  goal: SessionGoal | null;
  version: number;
  notice: string;
};

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0;
}

function strList(v: unknown): string[] {
  return Array.isArray(v)
    ? v.map(str).filter((s): s is string => s !== "")
    : [];
}

function isGoalStatus(v: string): v is GoalStatus {
  return (GOAL_STATUSES as readonly string[]).includes(v);
}

/** parseSessionGoal reads a goal object; null when it is not one. */
export function parseSessionGoal(raw: unknown): SessionGoal | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const o = raw as Record<string, unknown>;
  const objective = typeof o.objective === "string" ? o.objective.trim() : "";
  if (!objective) {
    return null;
  }
  const status = str(o.status);
  let lastCheck: GoalCheck | null = null;
  const lc = o.lastCheck;
  if (lc && typeof lc === "object" && !Array.isArray(lc)) {
    const c = lc as Record<string, unknown>;
    const verdict = str(c.verdict);
    if (verdict) {
      lastCheck = {
        verdict,
        reason: str(c.reason),
        remaining: strList(c.remaining),
        verified: c.verified === true,
        at: str(c.at),
        model: str(c.model),
      };
    }
  }
  const checklist: GoalChecklistItem[] = Array.isArray(o.checklist)
    ? o.checklist
        .filter(
          (r): r is Record<string, unknown> =>
            !!r && typeof r === "object" && !Array.isArray(r),
        )
        .map((r) => ({
          text: str(r.text),
          status: str(r.status) || "unverified",
          evidence: str(r.evidence),
        }))
        .filter((r) => r.text !== "")
    : [];
  return {
    id: str(o.id),
    objective,
    // A status this client does not know yet reads as the plain active goal.
    status: isGoalStatus(status) ? status : "active",
    statusReason: str(o.statusReason),
    setAt: str(o.setAt),
    updatedAt: str(o.updatedAt),
    continuations: num(o.continuations),
    maxContinuations: num(o.maxContinuations),
    checks: num(o.checks),
    activeMs: num(o.activeMs),
    tokensUsed: num(o.tokensUsed),
    tokenBudget: num(o.tokenBudget),
    lastCheck,
    checklist,
    model: str(o.model),
    reasoning: str(o.reasoning),
    checkModel: str(o.checkModel),
    checkReasoning: str(o.checkReasoning),
  };
}

/**
 * parseSessionGoalUpdate reads the envelope of any of the four deliveries: the
 * REST answer, the `goal` field of the messages read, the turn stream's ACP
 * update and the events stream's frame. `fallbackSessionId` names the session
 * when the payload does not (an old server's answer). Null when the payload
 * names no session, carries no `goal` key, or carries a goal that is not one -
 * a broken payload must not clear a goal on screen.
 */
export function parseSessionGoalUpdate(
  raw: unknown,
  fallbackSessionId = "",
): SessionGoalUpdate | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const o = raw as Record<string, unknown>;
  const sessionId = str(o.sessionId) || fallbackSessionId.trim();
  if (!sessionId || !("goal" in o)) {
    return null;
  }
  let goal: SessionGoal | null = null;
  if (o.goal !== null && o.goal !== undefined) {
    goal = parseSessionGoal(o.goal);
    if (!goal) {
      return null;
    }
  }
  return {
    sessionId,
    goal,
    version:
      typeof o.version === "number" && Number.isFinite(o.version)
        ? o.version
        : 0,
    notice: str(o.notice),
  };
}

/** sessionGoalEventOf parses the data of an `event: session_goal` frame. */
export function sessionGoalEventOf(data: string): SessionGoalUpdate | null {
  try {
    return parseSessionGoalUpdate(JSON.parse(data));
  } catch {
    return null;
  }
}

/**
 * isNewerGoal reports whether next should replace what a view holds: the same
 * session and a higher version. The same change reaches a tab down the turn
 * stream and the events stream, and an action's answer can arrive after the
 * event of a later change.
 */
export function isNewerGoal(
  heldVersion: number,
  viewedSessionId: string,
  next: SessionGoalUpdate,
): boolean {
  if (!viewedSessionId || next.sessionId !== viewedSessionId) {
    return false;
  }
  return next.version > heldVersion;
}

/** The colour family a status is drawn in (styles.css, `.goal-tone-*`). */
export type GoalTone = "active" | "muted" | "alert" | "done";

export function goalTone(status: GoalStatus): GoalTone {
  switch (status) {
    case "active":
      return "active";
    case "blocked":
      return "alert";
    case "complete":
      return "done";
    default:
      return "muted";
  }
}

/** The i18n key of a status's short word. */
export function goalStatusKey(status: GoalStatus): string {
  return `goal.status.${status}`;
}

/** The i18n key of a verdict's label; unknown verdicts read as not met. */
export function goalVerdictKey(verdict: string): string {
  switch (verdict) {
    case "met":
      return "goal.verdict.met";
    case "needs_user":
      return "goal.verdict.needsUser";
    case "impossible":
      return "goal.verdict.impossible";
    default:
      return "goal.verdict.notMet";
  }
}

/** What the operator can do with a goal in a status. */
export function goalCanPause(goal: SessionGoal): boolean {
  return goal.status === "active";
}

export function goalCanResume(goal: SessionGoal): boolean {
  return (
    goal.status === "paused" ||
    goal.status === "blocked" ||
    goal.status === "limited"
  );
}

/**
 * Whether typed text is a `/goal` command: the word alone or followed by
 * whitespace, the way the server parses it (`ParseGoalCommand`), so `/goals`
 * is not one.
 */
export function isGoalCommand(text: string): boolean {
  return /^\/goal(?:\s|$)/.test(text.trim());
}

/** A bare `/goal`: the web UI opens the goal popover instead of sending it. */
export function isBareGoalCommand(text: string): boolean {
  return text.trim() === "/goal";
}

/**
 * The prompt that sets (or replaces) a goal and starts working on it. The
 * checker a goal was set with goes with it, so an edited objective is checked
 * by the same model at the same level.
 */
export function goalSetPrompt(
  objective: string,
  checker?: { model?: string; reasoning?: string },
): string {
  const options = [
    checker?.model ? `--model ${checker.model}` : "",
    checker?.reasoning ? `--reasoning ${checker.reasoning}` : "",
  ].filter(Boolean);
  return ["/goal", ...options, objective.trim()].join(" ");
}

export const GOAL_RESUME_PROMPT = "/goal resume";

/** Why the supervisor started a turn nobody typed. */
export type GoalTurnKind =
  | "kickoff"
  | "continue"
  | "recover"
  | "resume"
  | "wrapup";

const GOAL_TURN_KINDS: readonly string[] = [
  "kickoff",
  "continue",
  "recover",
  "resume",
  "wrapup",
];

/** The marker of a goal turn's first message, from either wire shape. */
export type GoalTurn = {
  kind: GoalTurnKind;
  /** The automatic continuation's number, out of limit; 0 for a kickoff and a resume. */
  index: number;
  limit: number;
  objective: string;
  reason: string;
  remaining: string[];
};

/**
 * parseGoalTurn reads the marker of the `goal_turn` frame (ACP) or of the
 * message's `goal_turn` field; null for anything that is not one. A kind this
 * client does not know reads as a continuation, as the server's own note does.
 */
export function parseGoalTurn(raw: unknown): GoalTurn | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const o = raw as Record<string, unknown>;
  const kind = str(o.kind);
  if (!kind) {
    return null;
  }
  return {
    kind: (GOAL_TURN_KINDS.includes(kind) ? kind : "continue") as GoalTurnKind,
    index: num(o.index),
    limit: num(o.limit),
    objective: typeof o.objective === "string" ? o.objective.trim() : "",
    reason: str(o.reason),
    remaining: strList(o.remaining),
  };
}

type Translate = (
  key: string,
  params?: Record<string, string | number>,
) => string;

/**
 * goalTurnText is the one-line text of a goal row, mirroring the server's
 * `GoalTurnNote`: "Goal set: <objective>", "Goal continuation 2 of 10: <why>".
 */
export function goalTurnText(turn: GoalTurn, t: Translate): string {
  let head: string;
  switch (turn.kind) {
    case "kickoff":
      return t("goal.turn.kickoff", { objective: turn.objective });
    case "resume":
      return t("goal.turn.resume", { objective: turn.objective });
    case "wrapup":
      head = t("goal.turn.wrapup");
      break;
    case "recover":
      head = t("goal.turn.recover", { index: turn.index, limit: turn.limit });
      break;
    default:
      head = t("goal.turn.continue", { index: turn.index, limit: turn.limit });
  }
  return turn.reason
    ? t("goal.turn.withReason", { head, reason: turn.reason })
    : head;
}

export type GoalTurnItem = Extract<TranscriptItem, { type: "goal_turn" }>;

/** The transcript item of an `event: goal_turn` frame; null for one that is not JSON or names no kind. */
export function goalTurnItem(data: string, id: string): GoalTurnItem | null {
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch {
    return null;
  }
  const turn = parseGoalTurn(raw);
  if (!turn) {
    return null;
  }
  return {
    id,
    type: "goal_turn",
    turn,
    createdAtUtc: new Date().toISOString(),
  };
}

/** Whether two goal rows stand for the same turn. */
export function sameGoalTurn(a: GoalTurn, b: GoalTurn): boolean {
  return (
    a.kind === b.kind && a.index === b.index && a.objective === b.objective
  );
}

/** Rows that say nothing of a turn: what may stand after its opener before the turn's first frame. */
function saysNothing(it: TranscriptItem): boolean {
  switch (it.type) {
    case "memory_run":
    case "system_notice":
      return true;
    case "assistant_message":
      return !it.content.trim();
    default:
      return false;
  }
}

/**
 * applyGoalTurnToItems puts a goal row where the turn it opens begins. The
 * `/goal ...` prompt a tab sent stands as the turn's first row until this
 * frame names the turn; the server keeps no such message (the kickoff or the
 * resume message is the goal row after a reload), so the row takes its place
 * rather than following it. A continuation opens a turn of its own after the
 * one before it. A frame for the row already in place (a relay replaying what
 * the read already held) changes nothing.
 */
export function applyGoalTurnToItems(
  prev: TranscriptItem[],
  item: GoalTurnItem,
): TranscriptItem[] {
  let opener = -1;
  for (let i = prev.length - 1; i >= 0; i--) {
    if (opensTurn(prev[i])) {
      opener = i;
      break;
    }
  }
  if (opener >= 0 && prev.slice(opener + 1).every(saysNothing)) {
    const cur = prev[opener]!;
    if (cur.type === "goal_turn" && sameGoalTurn(cur.turn, item.turn)) {
      return prev;
    }
    if (
      cur.type === "user_message" &&
      (item.turn.kind === "kickoff" || item.turn.kind === "resume") &&
      isGoalCommand(cur.content)
    ) {
      const next = [...prev];
      next[opener] = {
        ...item,
        ...(cur.createdAtUtc ? { createdAtUtc: cur.createdAtUtc } : {}),
      };
      return next;
    }
  }
  return [...prev, item];
}
