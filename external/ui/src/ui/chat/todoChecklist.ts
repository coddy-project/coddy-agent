/**
 * The session's todo checklist under the transcript (DESIGN.md "Todo checklist
 * under the transcript"): which list a frame carries, and when the list shows.
 *
 * The list comes from two places: the transcript read (`todoPlan` of
 * GET /coddy/sessions/{id}/messages) and the `plan` frames of a running turn,
 * which the todo tools publish on every change. Both are validated by
 * normalizeTodoPlanSnapshot, the reader of the tool rows' snapshots.
 */

import {
  normalizeTodoPlanSnapshot,
  type TodoPlanEntry,
} from "./todoToolPreview";

/** The `_meta` key plan_write marks a design plan's frame with (internal/plans). */
const PLAN_KIND_META = "coddy.dev/planKind";

/**
 * The checklist a `plan` frame carries, or undefined for a frame that is not
 * the session's checklist: a design plan published by plan_write rides the
 * same event, marked with its kind, and a frame whose entries do not read as a
 * checklist is dropped whole. An archived checklist is an empty list.
 */
export function todoPlanFromPlanFrame(
  data: unknown,
): TodoPlanEntry[] | undefined {
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    return undefined;
  }
  const frame = data as { entries?: unknown; _meta?: unknown };
  const meta = frame._meta;
  if (
    meta &&
    typeof meta === "object" &&
    (meta as Record<string, unknown>)[PLAN_KIND_META] !== undefined
  ) {
    return undefined;
  }
  return normalizeTodoPlanSnapshot(frame.entries);
}

/** Statuses that still ask for work. */
function isOpen(entry: TodoPlanEntry): boolean {
  return entry.status === "pending" || entry.status === "in_progress";
}

/** Whether the list still has work left in it. */
export function todoPlanOpen(
  entries: readonly TodoPlanEntry[] | undefined,
): boolean {
  return !!entries && entries.some(isOpen);
}

/**
 * What the transcript remembers of the turn on screen: which session, whether
 * a turn runs, which turn it is, and whether the list had work left at some
 * point during that turn. A turn starts where `generating` flips and also
 * where a new prompt enters the transcript (`turnKey`): a queued follow-up
 * runs inside the same admitted turn, so `generating` never drops between
 * the answer and it.
 */
export type TodoChecklistTurn = {
  sessionId: string;
  generating: boolean;
  turnKey: number;
  sawOpen: boolean;
};

/**
 * The turn state after a render: another session, a flip of `generating` or
 * a new turn key starts over from the list as it is now, and an open list
 * seen while the turn runs is remembered until the turn ends. Returns `prev`
 * when nothing changed.
 */
export function nextTodoChecklistTurn(
  prev: TodoChecklistTurn,
  sessionId: string,
  generating: boolean,
  turnKey: number,
  open: boolean,
): TodoChecklistTurn {
  if (
    prev.sessionId !== sessionId ||
    prev.generating !== generating ||
    prev.turnKey !== turnKey
  ) {
    return { sessionId, generating, turnKey, sawOpen: generating && open };
  }
  if (generating && open && !prev.sawOpen) return { ...prev, sawOpen: true };
  return prev;
}

/**
 * Whether the list shows: while it has work left, and, once every item is
 * closed, only while the turn that closed it runs - that turn is still
 * writing its answer. A list that was already closed when the turn started
 * stays hidden: the server keeps a finished list until it is replaced or
 * archived and sends it again at the start of every turn.
 */
export function todoChecklistShown(
  entries: readonly TodoPlanEntry[] | undefined,
  generating: boolean,
  sawOpenThisTurn: boolean,
): boolean {
  if (!entries || entries.length === 0) return false;
  return todoPlanOpen(entries) || (generating && sawOpenThisTurn);
}

/**
 * The items a folded list keeps in view: every item in progress, or, with none,
 * the next pending one. A finished list keeps none: its head says it is done.
 */
export function todoChecklistFocus(
  entries: readonly TodoPlanEntry[],
): Set<number> {
  const focus = new Set<number>();
  entries.forEach((entry, index) => {
    if (entry.status === "in_progress") focus.add(index);
  });
  if (focus.size === 0) {
    const next = entries.findIndex((entry) => entry.status === "pending");
    if (next >= 0) focus.add(next);
  }
  return focus;
}

/** The completed items out of every item, for the list's accessible name. */
export function todoChecklistProgress(entries: readonly TodoPlanEntry[]): {
  done: number;
  total: number;
} {
  return {
    done: entries.filter((entry) => entry.status === "completed").length,
    total: entries.length,
  };
}

/** Whether two lists hold the same items with the same statuses, in order. */
export function sameTodoPlan(
  a: readonly TodoPlanEntry[] | undefined,
  b: readonly TodoPlanEntry[] | undefined,
): boolean {
  if (a === b) return true;
  if (!a || !b || a.length !== b.length) return false;
  return a.every(
    (entry, i) =>
      entry.content === b[i]!.content && entry.status === b[i]!.status,
  );
}

/**
 * Orders the two sources of a session's list. A plan frame is the newest word
 * on the list when it arrives; a transcript read is the list as it was when the
 * read was issued, and it can be answered after a frame that came later (the
 * reads that follow an answered permission prompt run beside the turn). Such a
 * read moves nothing back.
 */
export class TodoPlanOrder {
  private readonly frameAt = new Map<string, number>();

  /** A plan frame of the session was applied at `at`. */
  noteFrame(sid: string, at: number): void {
    this.frameAt.set(sid, at);
  }

  /** Whether a read of the session issued at `issuedAt` may set the list. */
  acceptsRead(sid: string, issuedAt: number): boolean {
    return (this.frameAt.get(sid) ?? -Infinity) <= issuedAt;
  }
}
