/**
 * The running turn's clock and generated tokens, as the server reports them.
 *
 * The agent loop publishes `turn_progress` on the turn's stream and keeps the same
 * numbers behind `GET /coddy/sessions/{id}/activity`, because the composer relay does
 * not replay a frame the transcript snapshot already covers. Both are folded into one
 * value here. Pure: no React, no clock of its own, so the tests pass the time in.
 */

export type TurnProgress = {
  /** When the turn started, on THIS machine's clock (ms since the epoch). */
  startedAtMs: number;
  /** Tokens the model has generated in this turn so far. */
  outputTokens: number;
  /** An estimate is part of `outputTokens`. */
  estimated: boolean;
  /**
   * The server's name for the turn: its start on the SERVER's clock. Every reading of
   * one turn carries the same one, so it tells two turns apart where arrival times
   * cannot. Never a clock to show - the two machines may disagree about the time.
   */
  serverStartedAtMs?: number;
  /**
   * How old the turn was, on the server's clock, when this reading was taken. It dates
   * the reading: of two readings of one turn, the larger one is the newer.
   */
  serverElapsedMs?: number;
};

/**
 * For readings that do not name their turn: two starts closer than this are the same
 * turn. The start is rebuilt from "now minus the elapsed time the server reported", so
 * it moves by the latency of whichever request carried it.
 */
export const SAME_TURN_SLACK_MS = 2_000;

/**
 * A start this much earlier than the known one replaces it. Latency only ever puts the
 * counted-back start later than the real one, so the earlier reading is the closer one;
 * below this the clock would not read differently and the line is left alone.
 */
const EARLIER_START_MS = 250;

function finiteNonNegative(value: unknown): number | null {
  const n = Number(value);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

function build(
  elapsed: unknown,
  startedAt: unknown,
  tokens: unknown,
  estimated: unknown,
  ageMs: number,
  nowMs: number,
): TurnProgress | null {
  let startedAtMs: number | null = null;
  const elapsedMs = finiteNonNegative(elapsed);
  const serverStart =
    typeof startedAt === "string" && startedAt ? Date.parse(startedAt) : NaN;
  if (elapsedMs !== null) {
    // The server's own measure of the turn's age. Counting back from the local
    // clock keeps the line right when the two machines disagree about the time.
    startedAtMs = nowMs - ageMs - elapsedMs;
  } else if (Number.isFinite(serverStart) && serverStart <= nowMs) {
    startedAtMs = serverStart;
  }
  if (startedAtMs === null) {
    return null;
  }
  const progress: TurnProgress = {
    startedAtMs,
    outputTokens: Math.floor(finiteNonNegative(tokens) ?? 0),
    estimated: estimated === true,
  };
  if (Number.isFinite(serverStart)) {
    progress.serverStartedAtMs = serverStart;
  }
  if (elapsedMs !== null) {
    progress.serverElapsedMs = elapsedMs;
  }
  return progress;
}

/**
 * A `turn_progress` frame of the turn stream. `ageMs` is the relay's `age:` line: a
 * replayed frame was written that long ago, and the turn is that much older by now.
 */
export function turnProgressFromFrame(
  raw: unknown,
  ageMs: number | undefined,
  nowMs: number,
): TurnProgress | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const f = raw as Record<string, unknown>;
  const age = finiteNonNegative(ageMs) ?? 0;
  return build(
    f.elapsedMs,
    f.startedAt,
    f.outputTokens,
    f.estimated,
    age,
    nowMs,
  );
}

/** The progress fields of `GET /coddy/sessions/{id}/activity`; null when it carries none. */
export function turnProgressFromActivity(
  raw: unknown,
  nowMs: number,
): TurnProgress | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const a = raw as Record<string, unknown>;
  if (a.turnActive !== true) {
    return null;
  }
  if (a.turnElapsedMs === undefined && a.turnStartedAt === undefined) {
    return null;
  }
  return build(
    a.turnElapsedMs,
    a.turnStartedAt,
    a.turnOutputTokens,
    a.turnTokensEstimated,
    0,
    nowMs,
  );
}

function sameTurn(prev: TurnProgress, next: TurnProgress): boolean {
  if (
    prev.serverStartedAtMs !== undefined &&
    next.serverStartedAtMs !== undefined
  ) {
    return prev.serverStartedAtMs === next.serverStartedAtMs;
  }
  return Math.abs(prev.startedAtMs - next.startedAtMs) <= SAME_TURN_SLACK_MS;
}

/**
 * Folds a new reading into what the tab already shows.
 *
 * The activity read is the recovery path and races the stream: an answer read before
 * the stream's last frame may arrive after it. Readings that carry the server's date
 * are ordered by it - the newer one wins, whichever way it came, and an exact count
 * that follows a higher estimate is the truth. Readings without a date fall back to the
 * rule that the stream is taken as it is and an activity read only ever raises the
 * count. The start of a turn is kept once known, so the clock does not shift by a
 * request's latency every second; only a clearly earlier start replaces it.
 */
export function mergeTurnProgress(
  prev: TurnProgress | null | undefined,
  next: TurnProgress,
  source: "stream" | "activity",
): TurnProgress {
  if (!prev || !sameTurn(prev, next)) {
    return next;
  }
  const dated =
    prev.serverElapsedMs !== undefined && next.serverElapsedMs !== undefined;
  if (dated) {
    if ((next.serverElapsedMs ?? 0) < (prev.serverElapsedMs ?? 0)) {
      return prev;
    }
  } else if (source === "activity" && next.outputTokens <= prev.outputTokens) {
    return prev;
  }
  const startedAtMs =
    next.startedAtMs < prev.startedAtMs - EARLIER_START_MS
      ? next.startedAtMs
      : prev.startedAtMs;
  if (
    prev.outputTokens === next.outputTokens &&
    prev.estimated === next.estimated &&
    startedAtMs === prev.startedAtMs
  ) {
    return prev;
  }
  return { ...next, startedAtMs };
}

/** 0, 433, 1.2k, 13.5k, 240k, 1.2M - the width of the number never jumps around. */
export function formatTurnTokens(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens <= 0) {
    return "0";
  }
  const n = Math.floor(tokens);
  if (n < 1000) {
    return String(n);
  }
  if (n < 100_000) {
    return `${(Math.floor(n / 100) / 10).toFixed(1)}k`;
  }
  if (n < 1_000_000) {
    return `${Math.floor(n / 1000)}k`;
  }
  return `${(Math.floor(n / 100_000) / 10).toFixed(1)}M`;
}
