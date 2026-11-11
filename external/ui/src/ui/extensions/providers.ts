/** The provider catalog an extension may read. Credentials and provider-specific
 * connection blocks stay server-side: rows expose only the fields an interface
 * can safely display. */
export type ProviderCatalogRow = Readonly<{
  name: string;
  type: string;
  api_base?: string;
  proxy?: string;
  timeout_ms?: number;
  usage_limits_panel?: boolean;
}>;

export type ProviderModelCatalog = Readonly<{
  context_window: number;
  max_tokens?: number;
  multimodal?: true;
}>;

export type ProviderModelRow = Readonly<{
  id: string;
  name?: string;
  context_window?: number;
  catalog?: ProviderModelCatalog;
}>;

export type ExtensionProvidersAPI = {
  list: () => Promise<readonly ProviderCatalogRow[]>;
  models: (name: string) => Promise<readonly ProviderModelRow[]>;
};

type ModelsResponse = {
  ok?: boolean;
  error?: unknown;
  models?: {
    id?: string;
    name?: string;
    context_window?: number;
    catalog?: {
      context_window?: number;
      max_tokens?: number;
      multimodal?: boolean;
    };
  }[];
};

/** A token count is present only when the answer carries a positive number. */
function tokens(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : undefined;
}

function normalizeModels(
  rows: ModelsResponse["models"],
): readonly ProviderModelRow[] {
  const seen = new Set<string>();
  const list: ProviderModelRow[] = [];
  for (const model of rows ?? []) {
    if (!model.id || seen.has(model.id)) continue;
    seen.add(model.id);

    const contextWindow = tokens(model.context_window);
    const catalogWindow = tokens(model.catalog?.context_window);
    const maxTokens = tokens(model.catalog?.max_tokens);
    const catalog =
      catalogWindow === undefined
        ? undefined
        : Object.freeze({
            context_window: catalogWindow,
            ...(maxTokens === undefined ? {} : { max_tokens: maxTokens }),
            ...(model.catalog?.multimodal === true
              ? { multimodal: true as const }
              : {}),
          });
    list.push(
      Object.freeze({
        id: model.id,
        ...(model.name ? { name: model.name } : {}),
        ...(contextWindow === undefined
          ? {}
          : { context_window: contextWindow }),
        ...(catalog ? { catalog } : {}),
      }),
    );
  }
  return Object.freeze(list);
}

export function extensionProviders(
  signal: AbortSignal,
  opts: {
    /** The transport; tests swap it for a stub. */
    request?: (input: string, init?: RequestInit) => Promise<Response>;
  } = {},
): ExtensionProvidersAPI {
  const call = opts.request ?? fetch;
  const active = () => signal.throwIfAborted();
  const getJSON = async <T>(path: string): Promise<T> => {
    active();
    const response = await call(path, { method: "GET", signal });
    active();
    const body = (await response.json().catch(() => ({}))) as T & {
      ok?: boolean;
      error?: unknown;
    };
    active();
    if (!response.ok) {
      const reason =
        typeof body.error === "string" && body.error
          ? body.error
          : `HTTP ${response.status}`;
      throw new Error(`Providers API: ${reason}`);
    }
    if (body.ok === false) {
      const reason =
        typeof body.error === "string" && body.error
          ? body.error
          : "request failed";
      throw new Error(`Providers API: ${reason}`);
    }
    return body;
  };

  return {
    async list() {
      const body = await getJSON<{ providers?: unknown[] }>("/coddy/config");
      const rows: ProviderCatalogRow[] = [];
      for (const value of body.providers ?? []) {
        if (!value || typeof value !== "object" || Array.isArray(value))
          continue;
        const source = value as Record<string, unknown>;
        const row: {
          name: string;
          type: string;
          api_base?: string;
          proxy?: string;
          timeout_ms?: number;
          usage_limits_panel?: boolean;
        } = {
          name: String(source.name ?? ""),
          type: String(source.type ?? ""),
        };
        if (typeof source.api_base === "string") row.api_base = source.api_base;
        if (typeof source.proxy === "string") row.proxy = source.proxy;
        if (typeof source.timeout_ms === "number")
          row.timeout_ms = source.timeout_ms;
        if (typeof source.usage_limits_panel === "boolean")
          row.usage_limits_panel = source.usage_limits_panel;
        rows.push(Object.freeze(row));
      }
      return Object.freeze(rows);
    },
    async models(name) {
      active();
      if (typeof name !== "string" || !name.trim())
        throw new Error("Providers API: provider name must be non-empty");
      const body = await getJSON<ModelsResponse>(
        `/coddy/providers/${encodeURIComponent(name)}/models`,
      );
      return normalizeModels(body.models);
    },
  };
}
