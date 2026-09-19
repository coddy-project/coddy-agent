import { afterEach, expect, test, vi } from "vitest";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { useSessionTurnActivity } from "./useSessionTurnActivity";
import type { TurnProgress } from "./turnProgress";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** A server whose activity answer the test swaps; the queue is always empty. */
function stubServer(activity: () => Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.endsWith("/activity")
        ? activity()
        : { messages: [], version: 1 };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
}

test("a read that finds the session idle drops the turn's progress, even when it notifies nobody", async () => {
  // The tab's own stream ends and the shell reads the activity without asking for a
  // reconcile (notify = false). That read may be the first to see the session idle,
  // and then no reconcile ever runs for this turn: the progress it left behind would
  // put the old clock and the old tokens on the first moments of the next turn.
  let answer: Record<string, unknown> = {
    sessionId: "sess_a",
    turnActive: true,
    turnStartedAt: "2026-09-18T10:00:00Z",
    turnElapsedMs: 45_000,
    turnOutputTokens: 1200,
    turnTokensEstimated: false,
  };
  stubServer(() => answer);
  const seen: Array<TurnProgress | null> = [];
  const onReconcile = vi.fn();
  const { result } = renderHook(() =>
    useSessionTurnActivity({
      sessionId: "sess_a",
      connected: true,
      postPending: () => false,
      onQueueRead: () => () => {},
      onReconcile,
      onTurnProgress: (_sid, progress) => seen.push(progress),
    }),
  );
  await waitFor(() => expect(seen.at(-1)?.outputTokens).toBe(1200));

  answer = { sessionId: "sess_a", turnActive: false };
  onReconcile.mockClear();
  await result.current.refresh("sess_a", false);
  expect(seen.at(-1)).toBeNull();
  expect(onReconcile).not.toHaveBeenCalled();
});
