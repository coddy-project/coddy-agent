export type GenerationSummary = {
  outputTokens: number;
  tokensPerSecond: number;
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
    f.outputTokens > 0 &&
    typeof f.tokensPerSecond === "number" &&
    Number.isFinite(f.tokensPerSecond) &&
    f.tokensPerSecond > 0
    ? {
        outputTokens: f.outputTokens,
        tokensPerSecond: f.tokensPerSecond,
        ...(typeof f.elapsedMs === "number" &&
        Number.isFinite(f.elapsedMs) &&
        f.elapsedMs >= 0
          ? { elapsedMs: f.elapsedMs }
          : {}),
      }
    : null;
}
