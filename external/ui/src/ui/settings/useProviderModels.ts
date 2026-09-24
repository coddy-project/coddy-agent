import { useCallback, useEffect, useRef, useState } from "react";

/** One model entry as the provider lists it. */
export interface FetchedModel {
  id: string;
  name?: string;
  /** Context window the listing reports, absent when it reports none. */
  context_window?: number;
}

/** A providers[] row as the settings form holds it. */
export type ProviderRow = Record<string, unknown>;

/** The fields POST /coddy/providers/models reads off a row. */
const REQUEST_FIELDS = [
  "name",
  "type",
  "api_base",
  "api_key",
  "api_key_command",
  "proxy",
] as const;

export type ProviderModelsRequest = Record<
  (typeof REQUEST_FIELDS)[number],
  string
>;

function str(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/** A provider row can be listed once it has a name and a type. */
export function providerRowFetchable(row: ProviderRow): boolean {
  return str(row.name).trim() !== "" && str(row.type).trim() !== "";
}

/**
 * The request body for a row: its connection fields as strings, nothing else.
 * The route inherits what the body leaves empty from the saved provider of the
 * same name, so an unsaved row travels as typed and a saved one resolves its
 * stored credentials without them crossing the wire twice.
 *
 * The server runs a posted api_key_command. Only an explicit fetch (the
 * refresh icon) sends it: an automatic one leaves it out, so a command is
 * never run half-typed, and the route falls back to the saved credentials.
 */
export function providerModelsRequest(
  row: ProviderRow,
  runCommand = false,
): ProviderModelsRequest {
  const out = {} as ProviderModelsRequest;
  for (const key of REQUEST_FIELDS) {
    out[key] = str(row[key]);
  }
  if (!runCommand) {
    out.api_key_command = "";
  }
  return out;
}

/** How long a changed provider id or type must stay still before the list is
 * fetched again: typing an id sends one request, not one per key. */
export const AUTO_FETCH_DEBOUNCE_MS = 600;

/**
 * The display name of a listed model when it says more than the id does:
 * a listing that repeats the id with different case or punctuation ("GPT-5.6-Sol"
 * for gpt-5.6-sol) adds nothing worth showing.
 */
export function modelDisplayName(m: FetchedModel): string | undefined {
  if (!m.name) {
    return undefined;
  }
  const fold = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, "");
  return fold(m.name) === fold(m.id) ? undefined : m.name;
}

type ModelsResponse = {
  ok?: boolean;
  error?: string;
  models?: { id?: string; name?: string; context_window?: number }[];
};

function normalize(rows: ModelsResponse["models"]): FetchedModel[] {
  const seen = new Set<string>();
  const list: FetchedModel[] = [];
  for (const m of rows ?? []) {
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
  return list;
}

/**
 * useProviderModels fetches the model list one provider row advertises through
 * POST /coddy/providers/models, posting the row as it stands in the settings
 * form: credentials typed but not yet saved, and a provider the document does
 * not have at all, resolve exactly as they would once stored.
 *
 * Every call takes a ticket; a newer call, `reset` and unmounting invalidate
 * the older ones, and an answer whose ticket is stale resolves to `null` and
 * touches no state. That is what keeps a slow answer for the provider the
 * operator has since switched away from off the list of the one they picked.
 */
export function useProviderModels() {
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<FetchedModel[]>([]);
  const [error, setError] = useState("");
  const [fetched, setFetched] = useState(false);
  const ticket = useRef(0);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  /** Forgets the last answer and abandons any request in flight. */
  const reset = useCallback(() => {
    ticket.current++;
    setLoading(false);
    setModels([]);
    setError("");
    setFetched(false);
  }, []);

  const fetchModels = useCallback(
    async (
      row: ProviderRow,
      options?: { runCommand?: boolean },
    ): Promise<FetchedModel[] | null> => {
      if (!providerRowFetchable(row)) {
        return null;
      }
      const mine = ++ticket.current;
      const live = () => mounted.current && ticket.current === mine;
      setLoading(true);
      setError("");

      let list: FetchedModel[] = [];
      let failure = "";
      try {
        const res = await fetch("/coddy/providers/models", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            providerModelsRequest(row, options?.runCommand === true),
          ),
        });
        // A proxy or a load balancer in front can answer with an HTML page:
        // the status is the useful part then, not a JSON parse error.
        const data = (await res.json().catch(() => ({}))) as ModelsResponse;
        if (!res.ok || !data.ok) {
          failure = data.error ?? `HTTP ${res.status}`;
        } else {
          list = normalize(data.models);
        }
      } catch (e) {
        failure = e instanceof Error ? e.message : String(e);
      }
      if (!live()) {
        return null;
      }
      setModels(list);
      setError(failure);
      setLoading(false);
      setFetched(true);
      return list;
    },
    [],
  );

  return { loading, models, error, fetched, fetchModels, reset };
}

/**
 * useAutoProviderModels keeps the list of one provider row on its own: it
 * fetches as soon as the row can be listed, and again when the row becomes
 * another provider - its id or its type changed and stayed still for
 * AUTO_FETCH_DEBOUNCE_MS. Edits to the endpoint, the key or the proxy do not
 * fetch by themselves: a half-typed URL must not receive the key typed next
 * to it, so those wait for the caller's refresh control. Automatic fetches
 * never carry api_key_command (see providerModelsRequest); the refresh control
 * passes `{ runCommand: true }` to fetchModels for that.
 */
export function useAutoProviderModels(row: ProviderRow | undefined) {
  const api = useProviderModels();
  const { fetchModels, reset } = api;
  const identity =
    row && providerRowFetchable(row)
      ? `${str(row.type).trim()}\u0000${str(row.name).trim()}`
      : "";
  const rowRef = useRef(row);
  useEffect(() => {
    rowRef.current = row;
  });
  const settled = useRef(false);
  useEffect(() => {
    if (!identity) {
      settled.current = false;
      reset();
      return;
    }
    const delay = settled.current ? AUTO_FETCH_DEBOUNCE_MS : 0;
    settled.current = true;
    const timer = window.setTimeout(() => {
      const current = rowRef.current;
      if (current) {
        void fetchModels(current);
      }
    }, delay);
    return () => window.clearTimeout(timer);
  }, [identity, fetchModels, reset]);
  return api;
}
