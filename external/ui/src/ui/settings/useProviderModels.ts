import { useCallback, useState } from "react";

export type FetchedModel = { id: string; name?: string };

/**
 * A providers[] row as the settings document holds it. Only the fields the
 * models endpoint reads are carried; the document may contain more.
 */
export type ProviderRow = {
  name?: string;
  type?: string;
  api_base?: string;
  api_key?: string;
  api_key_command?: string;
  proxy?: string;
};

type ProviderModelsResponse = {
  ok?: boolean;
  error?: string;
  models?: FetchedModel[];
};

/** providerRowFetchable says whether a providers[] row has enough to ask for
 * its model list: the name (it prefixes every fetched id) and the type. */
export function providerRowFetchable(row: ProviderRow): boolean {
  return (row.name ?? "").trim() !== "" && (row.type ?? "").trim() !== "";
}

function providerModelsBody(row: ProviderRow): string {
  return JSON.stringify({
    name: (row.name ?? "").trim(),
    type: (row.type ?? "").trim(),
    api_base: row.api_base ?? "",
    api_key: row.api_key ?? "",
    api_key_command: row.api_key_command ?? "",
    proxy: row.proxy ?? "",
  });
}

/**
 * useProviderModels fetches the model lists advertised by the provider rows of
 * the settings document via POST /coddy/providers/models: the row travels in
 * the request body, so a provider that has not been saved yet is fetched the
 * same way as a stored one (issue #335). Each returned id is prefixed with its
 * provider name (provider/model), the shape models[].model stores. A provider
 * that fails contributes its error to the message but does not drop the lists
 * that did come back, so manual entry stays possible per provider. `fetched`
 * flips true once the requests settle.
 */
export function useProviderModels() {
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<FetchedModel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [fetched, setFetched] = useState(false);

  const fetchModels = useCallback(async (providers: ProviderRow[]) => {
    const rows = providers.filter(providerRowFetchable);
    if (!rows.length) {
      return;
    }
    setLoading(true);
    setError(null);
    const merged: FetchedModel[] = [];
    const errors: string[] = [];
    const seen = new Set<string>();
    await Promise.all(
      rows.map(async (row) => {
        const name = (row.name ?? "").trim();
        try {
          const res = await fetch("/coddy/providers/models", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: providerModelsBody(row),
          });
          const data = (await res
            .json()
            .catch(() => ({}))) as ProviderModelsResponse;
          if (!res.ok || !data.ok) {
            const msg = data?.error || `HTTP ${res.status}`;
            errors.push(rows.length > 1 ? `${name}: ${msg}` : msg);
            return;
          }
          for (const m of data.models ?? []) {
            const id = `${name}/${m.id}`;
            // Two form rows may carry the same provider name before the
            // document is saved - keep the merged pick-list unique anyway.
            if (!seen.has(id)) {
              seen.add(id);
              merged.push(m.name ? { id, name: m.name } : { id });
            }
          }
        } catch (e) {
          errors.push(
            `${name}: ${e instanceof Error ? e.message : "request failed"}`,
          );
        }
      }),
    );
    setModels(merged);
    setError(errors.length ? errors.join("; ") : null);
    setLoading(false);
    setFetched(true);
  }, []);

  return { loading, models, error, fetched, fetchModels };
}
