export type GenerationSummary = {
  outputTokens: number;
  /**
   * Absent on a turn with no generation window to measure: an agent behind
   * an acp row streams its answer around waits of its own, so what it writes
   * per second says nothing about a model. The line then carries the clock
   * and the token count alone.
   */
  tokensPerSecond?: number;
  /** How long the turn ran; absent on a row saved before it was kept. */
  elapsedMs?: number;
};

/**
 * Precision is chosen before rounding, so 1.999 still reads as 2.00. The
 * decimal point stays a point in every locale: the token count beside it
 * never localizes either, and the Go console prints the same number.
 */
export function formatGenerationSpeed(speed: number): string {
  const digits = speed < 2 ? 2 : speed < 10 ? 1 : 0;
  return speed.toFixed(digits);
}

export function generationSummaryFromFrame(
  raw: unknown,
): GenerationSummary | null {
  if (!raw || typeof raw !== "object") return null;
  const f = raw as Record<string, unknown>;
  return f.completed === true &&
    typeof f.outputTokens === "number" &&
    f.outputTokens > 0
    ? {
        outputTokens: f.outputTokens,
        // A frame without a speed is a finished turn nobody could measure
        // (an agent behind an acp row), not a frame to drop.
        ...(typeof f.tokensPerSecond === "number" &&
        Number.isFinite(f.tokensPerSecond) &&
        f.tokensPerSecond > 0
          ? { tokensPerSecond: f.tokensPerSecond }
          : {}),
        ...(typeof f.elapsedMs === "number" &&
        Number.isFinite(f.elapsedMs) &&
        f.elapsedMs >= 0
          ? { elapsedMs: f.elapsedMs }
          : {}),
      }
    : null;
}
