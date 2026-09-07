/** Matches session stats / Composer contextBreakdown. */
export type ContextBreakdownLike = {
  systemPrompt?: number;
  toolDefinitions?: number;
  rules?: number;
  skills?: number;
  mcp?: number;
  subagents?: number;
  conversation?: number;
  estimatedTotal?: number;
};

/** Estimated tokens occupying the model context window (not cumulative API usage). */
export function contextUsedTokens(
  breakdown: ContextBreakdownLike | null | undefined,
): number {
  if (!breakdown) {
    return 0;
  }
  if (
    typeof breakdown.estimatedTotal === "number" &&
    breakdown.estimatedTotal > 0
  ) {
    return breakdown.estimatedTotal;
  }
  return (
    Math.max(0, breakdown.systemPrompt || 0) +
    Math.max(0, breakdown.toolDefinitions || 0) +
    Math.max(0, breakdown.rules || 0) +
    Math.max(0, breakdown.skills || 0) +
    Math.max(0, breakdown.mcp || 0) +
    Math.max(0, breakdown.subagents || 0) +
    Math.max(0, breakdown.conversation || 0)
  );
}

/** Percent of max context window filled (0–100). */
export function contextUsagePercent(
  maxContextTokens: number,
  breakdown: ContextBreakdownLike | null | undefined,
): number {
  if (!maxContextTokens || maxContextTokens <= 0) {
    return 0;
  }
  const used = contextUsedTokens(breakdown);
  if (used <= 0) {
    return 0;
  }
  return Math.min(100, Math.max(0, (used / maxContextTokens) * 100));
}

/** Applies an ACP usage_update immediately while detailed HTTP stats refresh. */
export function withContextUsedTokens(
  breakdown: ContextBreakdownLike | null | undefined,
  usedTokens: number,
): Required<ContextBreakdownLike> {
  const used = Math.max(0, Math.trunc(usedTokens));
  const current = {
    systemPrompt: Math.max(0, breakdown?.systemPrompt || 0),
    toolDefinitions: Math.max(0, breakdown?.toolDefinitions || 0),
    rules: Math.max(0, breakdown?.rules || 0),
    skills: Math.max(0, breakdown?.skills || 0),
    mcp: Math.max(0, breakdown?.mcp || 0),
    subagents: Math.max(0, breakdown?.subagents || 0),
  };
  const fixed =
    current.systemPrompt +
    current.toolDefinitions +
    current.rules +
    current.skills +
    current.mcp +
    current.subagents;
  return {
    ...current,
    conversation: Math.max(0, used - fixed),
    estimatedTotal: used,
  };
}
