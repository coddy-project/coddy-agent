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
};

/**
 * Two starts closer than this are the same turn: the start is rebuilt from
 * "now minus the elapsed time the server reported", so it moves by the latency of
 * whichever request carried it.
 */
export const SAME_TURN_SLACK_MS = 2_000;

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
  if (elapsedMs !== null) {
    // The server's own measure of the turn's age. Counting back from the local
    // clock keeps the line right when the two machines disagree about the time.
    startedAtMs = nowMs - ageMs - elapsedMs;
  } else if (typeof startedAt === "string" && startedAt) {
    const at = Date.parse(startedAt);
    if (Number.isFinite(at) && at <= nowMs) {
      startedAtMs = at;
    }
  }
  if (startedAtMs === null) {
    return null;
  }
  return {
    startedAtMs,
    outputTokens: Math.floor(finiteNonNegative(tokens) ?? 0),
    estimated: estimated === true,
  };
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

/**
 * Folds a new reading into what the tab already shows.
 *
 * The stream is ordered, so a frame is taken as it is - the exact count that follows an
 * estimate may be lower, and that correction is the truth. The activity read is the
 * recovery path and races the stream, so within one turn it only ever raises the count.
 * The start of a turn is kept once known, so the clock does not shift by a request's
 * latency every second.
 */
export function mergeTurnProgress(
  prev: TurnProgress | null | undefined,
  next: TurnProgress,
  source: "stream" | "activity",
): TurnProgress {
  if (
    !prev ||
    Math.abs(prev.startedAtMs - next.startedAtMs) > SAME_TURN_SLACK_MS
  ) {
    return next;
  }
  if (source === "activity" && next.outputTokens <= prev.outputTokens) {
    return prev;
  }
  if (
    prev.outputTokens === next.outputTokens &&
    prev.estimated === next.estimated
  ) {
    return prev;
  }
  return { ...next, startedAtMs: prev.startedAtMs };
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
