import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";
import { fetchJSON, HDR } from "../coddyApi";
import { createDebouncedSessionStatsRefresh } from "./sessionStatsPoll";
import type { TokenUsage } from "./types";

export type SessionStats = {
  tokenUsageTotal?: {
    inputTokens: number;
    outputTokens: number;
    totalTokens: number;
  };
  contextBreakdown?: {
    systemPrompt: number;
    toolDefinitions: number;
    rules: number;
    skills: number;
    mcp: number;
    subagents: number;
    conversation: number;
    estimatedTotal: number;
  };
};

export type ContextBreakdown = NonNullable<SessionStats["contextBreakdown"]>;

/**
 * Token usage and context breakdown of the viewed session: the /stats fetch
 * with its debounced variant, the while-generating poll, and the token
 * baseline the composer stream adds deltas onto. The setters and the baseline
 * ref are exposed because the streaming core (which stays in App) writes them
 * directly from SSE frames.
 */
export function useSessionStats({
  sessionId,
  generating,
  viewedSessionIdRef,
}: {
  sessionId: string;
  generating: boolean;
  viewedSessionIdRef: RefObject<string>;
}): {
  tokenUsage: TokenUsage | null;
  setTokenUsage: Dispatch<SetStateAction<TokenUsage | null>>;
  contextBreakdown: ContextBreakdown | null;
  setContextBreakdown: Dispatch<SetStateAction<ContextBreakdown | null>>;
  tokenBaselineRef: RefObject<{ input: number; output: number; total: number }>;
  applySessionStatsPayload: (
    stats: SessionStats | null | undefined,
    viewing: boolean,
  ) => void;
  refreshSessionStats: (sid: string) => Promise<void>;
  debouncedRefreshSessionStats: (sid: string) => void;
} {
  const [tokenUsage, setTokenUsage] = useState<TokenUsage | null>(null);
  const [contextBreakdown, setContextBreakdown] =
    useState<ContextBreakdown | null>(null);
  const tokenBaselineRef = useRef<{
    input: number;
    output: number;
    total: number;
  }>({ input: 0, output: 0, total: 0 });

  const applySessionStatsPayload = useCallback(
    (stats: SessionStats | null | undefined, viewing: boolean) => {
      if (!viewing) {
        return;
      }
      if (stats?.tokenUsageTotal) {
        const t = stats.tokenUsageTotal;
        tokenBaselineRef.current = {
          input: t.inputTokens || 0,
          output: t.outputTokens || 0,
          total: t.totalTokens || 0,
        };
        setTokenUsage({
          inputTokens: tokenBaselineRef.current.input,
          outputTokens: tokenBaselineRef.current.output,
          totalTokens: tokenBaselineRef.current.total,
        });
      }
      if (stats?.contextBreakdown) {
        setContextBreakdown(stats.contextBreakdown);
      }
    },
    [],
  );

  const refreshSessionStats = useCallback(
    async (sid: string) => {
      const key = sid.trim();
      if (!key) {
        return;
      }
      const statsRes = await fetchJSON<{ stats?: SessionStats | null }>(
        `/coddy/sessions/${encodeURIComponent(key)}/stats`,
        { headers: { [HDR]: key } },
      );
      if (!statsRes.ok) {
        return;
      }
      applySessionStatsPayload(
        statsRes.data?.stats,
        viewedSessionIdRef.current.trim() === key,
      );
    },
    [applySessionStatsPayload],
  );

  const debouncedRefreshSessionStats = useMemo(
    () =>
      createDebouncedSessionStatsRefresh((sid) => {
        void refreshSessionStats(sid);
      }),
    [refreshSessionStats],
  );

  useEffect(() => {
    const sid = sessionId.trim();
    if (!sid || !generating) {
      return;
    }
    void refreshSessionStats(sid);
    const timer = window.setInterval(() => {
      void refreshSessionStats(sid);
    }, 800);
    return () => window.clearInterval(timer);
  }, [sessionId, generating, refreshSessionStats]);

  return {
    tokenUsage,
    setTokenUsage,
    contextBreakdown,
    setContextBreakdown,
    tokenBaselineRef,
    applySessionStatsPayload,
    refreshSessionStats,
    debouncedRefreshSessionStats,
  };
}
