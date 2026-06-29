import { useCallback, useState } from "react";

export type FetchedModel = { id: string; name?: string };

type ProviderModelsResponse = {
  ok?: boolean;
  error?: string;
  models?: FetchedModel[];
};

/**
 * useProviderModels fetches the model list advertised by a saved provider's
 * server via GET /coddy/providers/{name}/models. On failure (HTTP error or
 * ok:false) it surfaces an error and an empty list so callers fall back to
 * manual model entry. `fetched` flips true once a request has completed.
 */
export function useProviderModels() {
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<FetchedModel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [fetched, setFetched] = useState(false);

  const fetchModels = useCallback(async (provider: string) => {
    const name = provider.trim();
    if (!name) {
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`/coddy/providers/${encodeURIComponent(name)}/models`);
      const data = (await res.json().catch(() => ({}))) as ProviderModelsResponse;
      if (!res.ok || !data.ok) {
        setModels([]);
        setError(data?.error || `HTTP ${res.status}`);
      } else {
        setModels(data.models ?? []);
      }
    } catch (e) {
      setModels([]);
      setError(e instanceof Error ? e.message : "request failed");
    } finally {
      setLoading(false);
      setFetched(true);
    }
  }, []);

  const reset = useCallback(() => {
    setModels([]);
    setError(null);
    setFetched(false);
    setLoading(false);
  }, []);

  return { loading, models, error, fetched, fetchModels, reset };
}
