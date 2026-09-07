import { useCallback, useEffect, useRef, useState } from "react";

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
 * is the whole point: the form is being filled in. The provider type shown in
 * the form travels along as provider_type, so the Codex remap follows the row
 * the operator is editing rather than the config on disk.
 *
 * A model with no reasoning support answers ok:true with an empty list. That is
 * not an error, so callers get `detected: false` and no `error` - writing an
 * empty override would hide the composer's reasoning selector, which is not what
 * pressing Fetch asked for.
 *
 * Every call takes a ticket; `reset` and a newer call invalidate older tickets,
 * and unmounting invalidates all of them. A request that answers after that
 * resolves to `null` and touches no state, so a slow answer for a model id the
 * operator has since retyped, or for a row they have since deleted, never lands
 * on the wrong entry.
 */
export function useReasoningLevels() {
  const [loading, setLoading] = useState(false);
  const [detected, setDetected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fetched, setFetched] = useState(false);
  const ticket = useRef(0);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const fetchLevels = useCallback(
    async (model: string, providerType?: string): Promise<string[] | null> => {
      const id = model.trim();
      if (!id) {
        return null;
      }
      const mine = ++ticket.current;
      const live = () => mounted.current && ticket.current === mine;
      setLoading(true);
      setError(null);

      let levels: string[] = [];
      let failure: string | null = null;
      try {
        const type = (providerType ?? "").trim();
        const query =
          `model=${encodeURIComponent(id)}` +
          (type ? `&provider_type=${encodeURIComponent(type)}` : "");
        const res = await fetch(`/coddy/config/reasoning-levels?${query}`);
        const data = (await res
          .json()
          .catch(() => ({}))) as ReasoningLevelsResponse;
        if (!res.ok || !data.ok) {
          failure = data?.error || `HTTP ${res.status}`;
        } else {
          levels = data.levels ?? [];
        }
      } catch (e) {
        failure = e instanceof Error ? e.message : "request failed";
      }

      if (!live()) {
        return null;
      }
      setLoading(false);
      setFetched(true);
      setError(failure);
      setDetected(failure === null && levels.length > 0);
      return failure === null ? levels : [];
    },
    [],
  );

  const reset = useCallback(() => {
    // Abandon whatever is in flight along with the feedback of the last answer.
    ticket.current++;
    setLoading(false);
    setFetched(false);
    setError(null);
    setDetected(false);
  }, []);

  return { loading, detected, error, fetched, fetchLevels, reset };
}
