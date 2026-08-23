import { useCallback, useState } from "react";

type ReasoningLevelsResponse = {
  ok?: boolean;
  error?: string;
  levels?: string[];
  detected?: boolean;
};

/**
 * useReasoningLevels asks the gateway which reasoning levels a logical model id
 * offers when models[].reasoning_levels is not overridden, via
 * GET /coddy/config/reasoning-levels. The model id need not be saved yet, which
 * is the whole point: the form is being filled in.
 *
 * A model with no reasoning support answers ok:true with an empty list. That is
 * not an error, so callers get `detected: false` and no `error` - writing an
 * empty override would hide the composer's reasoning selector, which is not what
 * pressing Fetch asked for.
 */
export function useReasoningLevels() {
  const [loading, setLoading] = useState(false);
  const [levels, setLevels] = useState<string[]>([]);
  const [detected, setDetected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fetched, setFetched] = useState(false);

  const fetchLevels = useCallback(async (model: string): Promise<string[]> => {
    const id = model.trim();
    if (!id) {
      return [];
    }
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(
        `/coddy/config/reasoning-levels?model=${encodeURIComponent(id)}`,
      );
      const data = (await res
        .json()
        .catch(() => ({}))) as ReasoningLevelsResponse;
      if (!res.ok || !data.ok) {
        setLevels([]);
        setDetected(false);
        setError(data?.error || `HTTP ${res.status}`);
        return [];
      }
      const next = data.levels ?? [];
      setLevels(next);
      setDetected(Boolean(data.detected) && next.length > 0);
      return next;
    } catch (e) {
      setLevels([]);
      setDetected(false);
      setError(e instanceof Error ? e.message : "request failed");
      return [];
    } finally {
      setLoading(false);
      setFetched(true);
    }
  }, []);

  const reset = useCallback(() => {
    setLevels([]);
    setDetected(false);
    setError(null);
    setFetched(false);
    setLoading(false);
  }, []);

  return { loading, levels, detected, error, fetched, fetchLevels, reset };
}
