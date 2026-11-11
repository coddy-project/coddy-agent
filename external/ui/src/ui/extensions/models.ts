/**
 * The model catalog of the server the page talks to: what Settings and the
 * composer pickers fill themselves from. An extension reads it to draw its
 * own pickers or to reason about context windows and multimodality.
 */
export type ModelCatalogRow = Readonly<
  Record<string, unknown> & { id: string }
>;
export type ExtensionModelsAPI = {
  list: () => Promise<readonly ModelCatalogRow[]>;
};

export function extensionModels(
  signal: AbortSignal,
  opts: {
    request?: (input: string, init?: RequestInit) => Promise<Response>;
  } = {},
): ExtensionModelsAPI {
  const call = opts.request ?? fetch;
  return {
    async list() {
      signal.throwIfAborted();
      const response = await call("/v1/models", { signal });
      if (!response.ok) throw new Error(`Models API: HTTP ${response.status}`);
      const body = (await response.json()) as { data?: unknown[] };
      return (body.data ?? []).map(
        (row) => Object.freeze({ ...(row as object) }) as ModelCatalogRow,
      );
    },
  };
}
