import { transcriptItemsLooselyEqual } from "./transcriptServerSnapshot";
import type { TranscriptItem } from "./types";

/**
 * Where a page of a session's history sits, as GET
 * /coddy/sessions/{id}/messages reports it in `window`: the index of its first
 * message, the length of the history, the prompts before it (compaction
 * summaries excluded: the numbering of a rewind's `userMessageIndex`) and the
 * user-role messages before it (the numbering of `uiLog` rows).
 */
export type TranscriptWindow = {
  offset: number;
  total: number;
  turnsBefore: number;
  userRowsBefore: number;
};

/**
 * The newest page a session opens with. Small enough that an old phone maps
 * and paints it in a few frames, large enough that a short session arrives
 * whole in one read.
 */
export const TAIL_PAGE_MESSAGES = 60;

/** An older page, read when the reader scrolls up to the top of what is held. */
export const OLDER_PAGE_MESSAGES = 80;

/**
 * A live window holding more messages than this is started over from the
 * newest page once the reader is back at the newest message and the session
 * is idle, so a session left open through many turns does not re-read all of
 * them after every one.
 */
export const LIVE_WINDOW_REBASE_MESSAGES = 3 * TAIL_PAGE_MESSAGES;

function nonNegativeInt(v: unknown): number | null {
  return typeof v === "number" && Number.isInteger(v) && v >= 0 ? v : null;
}

/**
 * Reads `window` off a messages response. A server that predates paged reads
 * sends none and always returns the whole history, which is the window
 * starting at 0.
 */
export function parseTranscriptWindow(
  raw: unknown,
  messageCount: number,
): TranscriptWindow {
  const full: TranscriptWindow = {
    offset: 0,
    total: messageCount,
    turnsBefore: 0,
    userRowsBefore: 0,
  };
  if (!raw || typeof raw !== "object") return full;
  const w = raw as Record<string, unknown>;
  const offset = nonNegativeInt(w.offset);
  const total = nonNegativeInt(w.total);
  if (offset === null || total === null || offset + messageCount > total) {
    return full;
  }
  return {
    offset,
    total,
    turnsBefore: nonNegativeInt(w.turnsBefore) ?? 0,
    userRowsBefore: nonNegativeInt(w.userRowsBefore) ?? 0,
  };
}

/** What a messages read asks for. */
export type TranscriptPageRequest =
  | { kind: "tail" }
  | { kind: "from"; offset: number }
  | { kind: "older"; before: number }
  | { kind: "full" };

/** The query string of a messages read, `""` for the whole history. */
export function transcriptPageQuery(req: TranscriptPageRequest): string {
  switch (req.kind) {
    case "tail":
      return `?limit=${TAIL_PAGE_MESSAGES}`;
    case "from":
      return `?from=${Math.max(0, Math.floor(req.offset))}`;
    case "older":
      return `?limit=${OLDER_PAGE_MESSAGES}&before=${Math.max(0, Math.floor(req.before))}`;
    case "full":
      return "";
  }
}

/** The query string of the tool calls a page of messages issued. */
export function transcriptToolCallsQuery(
  window: TranscriptWindow,
  messageCount: number,
): string {
  if (window.offset === 0 && messageCount >= window.total) return "";
  return `?from=${window.offset}&to=${window.offset + messageCount}`;
}

/**
 * The older part of a transcript: pages read above the live window, oldest
 * first, and the window of the oldest one. It is history that no longer
 * changes, so it stays out of every reload and merge the live window goes
 * through, and is shown above it.
 */
export type OlderTranscript = {
  sessionId: string;
  /** The live window generation these pages were read against. */
  generation: number;
  items: TranscriptItem[];
  window: TranscriptWindow;
};

/**
 * Puts an older page in front of what is already held. A row whose id is
 * already on screen - a lookalike an id preserving merge borrowed - is given a
 * fresh id, so React never sees two rows with one key.
 */
export function prependOlderPage(
  page: readonly TranscriptItem[],
  held: readonly TranscriptItem[],
  live: readonly TranscriptItem[],
  newId: (prefix: string) => string,
): TranscriptItem[] {
  const taken = new Set<string>();
  for (const it of held) taken.add(it.id);
  for (const it of live) taken.add(it.id);
  const out: TranscriptItem[] = [];
  for (const it of page) {
    if (taken.has(it.id)) {
      const fresh = { ...it, id: newId("row") } as TranscriptItem;
      taken.add(fresh.id);
      out.push(fresh);
    } else {
      taken.add(it.id);
      out.push(it);
    }
  }
  return [...out, ...held];
}

/**
 * The part of `previous` that lines up with `next` when both end at the same
 * message but start at different ones (a window started over from the newest
 * page while nothing streams): its last `next.length` rows, provided the first
 * of them describes the same step as `next[0]`. Used to carry React keys over,
 * never to merge content.
 */
export function alignedTranscriptSuffix(
  next: readonly TranscriptItem[],
  previous: readonly TranscriptItem[] | undefined,
): TranscriptItem[] | undefined {
  if (!previous || next.length === 0 || previous.length < next.length) {
    return undefined;
  }
  const tail = previous.slice(previous.length - next.length);
  return transcriptItemsLooselyEqual(next[0]!, tail[0]!) ? tail : undefined;
}
