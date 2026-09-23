import { useState } from "react";

/** One model entry as the provider lists it. */
export interface FetchedModel {
  id: string;
  name?: string;
  context_window?: number;
}

/** The provider row shape SchemaForm hands the override. */
export type ProviderRow = Record<string, unknown>;

/** A provider row can be listed when it has a name and a type. */
export function providerRowFetchable(row: ProviderRow): boolean {
  return (
    typeof row.name === "string" &&
    row.name.trim() !== "" &&
    typeof row.type === "string" &&
    row.type.trim() !== ""
  );
}

/**
 * useProviderModels fetches the model list one provider row advertises. The
 * request posts the row as it stands in the settings form to
 * POST /coddy/providers/models, so credentials entered but not yet saved - and
 * a provider the document does not have at all - resolve exactly as they would
 * once stored. The hook is per form row: a fresh edit gets a fresh list.
 */
export function useProviderModels() {
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<FetchedModel[]>([]);
  const [error, setError] = useState("");
  const [fetched, setFetched] = useState(false);

  const fetchModels = async (row: ProviderRow) => {
    if (loading || !providerRowFetchable(row)) {
      return;
    }
    setLoading(true);
    setError("");
    try {
      const res = await fetch("/coddy/providers/models", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(row),
      });
      const data = (await res.json()) as {
        ok?: boolean;
        error?: string;
        models?: { id: string; name?: string; context_window?: number }[];
      };
      if (!res.ok || !data.ok) {
        setModels([]);
        setError(data.error ?? `HTTP ${res.status}`);
        return;
      }
      const seen = new Set<string>();
      const list: FetchedModel[] = [];
      for (const m of data.models ?? []) {
        if (!m.id || seen.has(m.id)) {
          continue;
        }
        seen.add(m.id);
        const item: FetchedModel = { id: m.id };
        if (m.name) {
          item.name = m.name;
        }
        if (
          typeof m.context_window === "number" &&
          Number.isFinite(m.context_window) &&
          m.context_window > 0
        ) {
          item.context_window = Math.floor(m.context_window);
        }
        list.push(item);
      }
      setModels(list);
    } catch (e) {
      setModels([]);
      setError(String(e));
    } finally {
      setLoading(false);
      setFetched(true);
    }
  };

  return { loading, models, error, fetched, fetchModels };
}
