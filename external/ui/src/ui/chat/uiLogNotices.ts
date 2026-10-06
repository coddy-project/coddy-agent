import type { TranscriptItem } from "./types";

/** One `uiLog` row as `GET /coddy/sessions/{id}/messages` returns it. */
export type RawUiLogRow = {
  id?: string;
  level?: string;
  message?: string;
  userTurnIndex?: number;
  createdAt?: string;
};

type SystemNotice = Extract<TranscriptItem, { type: "system_notice" }>;

/**
 * Hands out the notices the server keeps for a session (an LLM error, a
 * settings change) at the end of the turn they belong to, or earlier in that
 * turn when the turn went on after them.
 *
 * The server stamps a row with the number of user-role messages the history
 * held at that moment, compaction summaries and background wakes included,
 * so the feed counts the same way: the transcript mapping calls
 * `beforeUserRow` for every user-role message, whatever it renders it as,
 * and places the rows it returns before that message; `end` returns the rest
 * after the last message. A row whose turn the history no longer reaches
 * still comes out of `end`, so an error is never silently dropped.
 *
 * `beforeMessageAt` places a row inside its turn: called with the time a
 * message of the current turn was stored, it returns the rows of that turn
 * stamped before it. A provider failure the turn recovered from then stands
 * between the rows around the break instead of under the final answer, where
 * it read as the answer having failed. A row without a time keeps to the end.
 */
export function uiLogNoticeFeed(
  rows: RawUiLogRow[] | undefined,
  newId: (prefix: string) => string,
  /** User-role messages before the page the rows belong to (its window's
   *  `userRowsBefore`): the count the feed starts from. */
  userRowsBefore = 0,
): {
  beforeUserRow: () => SystemNotice[];
  beforeMessageAt: (createdAtUtc: string | undefined) => SystemNotice[];
  end: () => SystemNotice[];
} {
  const pending: Array<{ turn: number; order: number; item: SystemNotice }> = [];
  (rows || []).forEach((raw, order) => {
    const message = typeof raw.message === "string" ? raw.message.trim() : "";
    if (!message) return;
    const level = (raw.level || "error").trim() || "error";
    // Only the two levels the transcript knows how to render; a level a
    // newer server may add stays invisible rather than mis-rendered.
    if (level !== "error" && level !== "notice") return;
    const turn =
      typeof raw.userTurnIndex === "number" &&
      Number.isFinite(raw.userTurnIndex) &&
      raw.userTurnIndex >= 1
        ? Math.floor(raw.userTurnIndex)
        : 1;
    const id =
      typeof raw.id === "string" && raw.id.trim() !== "" ? raw.id.trim() : newId("s");
    const createdAtUtc = typeof raw.createdAt === "string" ? raw.createdAt : "";
    pending.push({
      turn,
      order,
      item: { id, type: "system_notice", level, message, createdAtUtc },
    });
  });
  pending.sort(
    (a, b) =>
      a.turn - b.turn ||
      (a.item.createdAtUtc || "").localeCompare(b.item.createdAtUtc || "") ||
      a.order - b.order,
  );
  let next = 0;
  let userRows = userRowsBefore;
  const upTo = (turn: number): SystemNotice[] => {
    const out: SystemNotice[] = [];
    while (next < pending.length && pending[next]!.turn <= turn) {
      out.push(pending[next]!.item);
      next++;
    }
    return out;
  };
  const before = (createdAtUtc: string | undefined): SystemNotice[] => {
    const at = createdAtUtc ? Date.parse(createdAtUtc) : Number.NaN;
    const out: SystemNotice[] = [];
    if (!Number.isFinite(at)) return out;
    while (next < pending.length && pending[next]!.turn <= userRows) {
      const stamped = Date.parse(pending[next]!.item.createdAtUtc || "");
      if (!(stamped < at)) break;
      out.push(pending[next]!.item);
      next++;
    }
    return out;
  };
  return {
    beforeUserRow: () => upTo(userRows++),
    beforeMessageAt: before,
    end: () => upTo(Number.POSITIVE_INFINITY),
  };
}
