export type GenerationSummary = {
  outputTokens: number;
  tokensPerSecond: number;
};

/** Precision is chosen before rounding, so 1.999 still reads as 2.00. */
export function formatGenerationSpeed(speed: number, locale: string): string {
  const digits = speed < 2 ? 2 : speed < 10 ? 1 : 0;
  return speed.toLocaleString(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
    useGrouping: false,
  });
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
    ? { outputTokens: f.outputTokens, tokensPerSecond: f.tokensPerSecond }
    : null;
}
