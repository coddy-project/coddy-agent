import { useEffect, useState } from "react";

import {
  providerModelsRequest,
  providerRowFetchable,
  type ProviderRow,
} from "./useProviderModels";

/**
 * What the models catalog (models_catalog, models.dev by default) suggests for
 * one model, as POST /coddy/models/catalog answers it.
 */
export interface CatalogSuggestion {
  /** The catalog's window: its input limit when it states one, else the whole window. */
  context_window?: number;
  /** The catalog's output limit. */
  max_output_tokens?: number;
  /** The max_tokens that keeps every request within the window; absent when none fits. */
  max_tokens?: number;
  /** The model reads images. */
  multimodal: boolean;
}

/** One model to look up: its API id and the window its provider reports. */
export interface CatalogModel {
  id: string;
  context_window?: number | undefined;
}

type CatalogAnswer = {
  ok?: boolean;
  enabled?: boolean;
  models?: Record<string, CatalogSuggestion>;
};

/** The suggestions of one lookup by model id; empty when the catalog is off,
 * unreachable, or knows none of the models. */
export type CatalogSuggestions = Record<string, CatalogSuggestion>;

function str(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/** How long an answer is reused before the next lookup asks again. */
export const CATALOG_ANSWER_TTL_MS = 5 * 60 * 1000;

/** At most this many answers are kept; the oldest goes first. */
const MAX_ENTRIES = 64;

type Entry = { at: number; promise: Promise<CatalogSuggestions> };

// One lookup per provider row and set of models: the provider form's list
// and the three fields of a logical model ask the same question, and the
// server's answer does not change within minutes.
const answers = new Map<string, Entry>();

// One question per logical model row: what its provider reports for the
// model, then what the catalog says of it measured against that window.
const rowAnswers = new Map<
  string,
  { at: number; promise: Promise<CatalogSuggestion | undefined> }
>();

/** Forgets every answer; tests start from an empty cache. */
export function resetModelsCatalogCache(): void {
  answers.clear();
  rowAnswers.clear();
}

/**
 * The request body for a row and its models. Only what the catalog needs
 * travels: the row's name, type and api_base, never a credential.
 */
export function modelsCatalogRequest(
  row: ProviderRow,
  models: CatalogModel[],
): {
  name: string;
  type: string;
  api_base: string;
  models: CatalogModel[];
} {
  return {
    name: str(row.name).trim(),
    type: str(row.type).trim(),
    api_base: str(row.api_base).trim(),
    models: models.map((m) =>
      m.context_window && m.context_window > 0
        ? { id: m.id, context_window: m.context_window }
        : { id: m.id },
    ),
  };
}

async function ask(body: string): Promise<CatalogSuggestions> {
  try {
    const res = await fetch("/coddy/models/catalog", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
    const data = (await res.json().catch(() => ({}))) as CatalogAnswer;
    if (!res.ok || !data.ok || !data.enabled || !data.models) {
      return {};
    }
    return data.models;
  } catch {
    return {};
  }
}

/**
 * lookupModelsCatalog answers the catalog's suggestions for the models of a
 * row, sharing one request between every caller that asks the same question
 * within CATALOG_ANSWER_TTL_MS. A failed or empty answer is not kept, so the
 * next caller asks again.
 */
export function lookupModelsCatalog(
  row: ProviderRow,
  models: CatalogModel[],
): Promise<CatalogSuggestions> {
  if (!providerRowFetchable(row) || models.length === 0) {
    return Promise.resolve({});
  }
  const body = JSON.stringify(modelsCatalogRequest(row, models));
  const now = Date.now();
  const hit = answers.get(body);
  if (hit && now - hit.at < CATALOG_ANSWER_TTL_MS) {
    return hit.promise;
  }
  const promise = ask(body).then((got) => {
    if (Object.keys(got).length === 0) {
      answers.delete(body);
    }
    return got;
  });
  answers.delete(body);
  answers.set(body, { at: now, promise });
  while (answers.size > MAX_ENTRIES) {
    const oldest = answers.keys().next().value;
    if (oldest === undefined) {
      break;
    }
    answers.delete(oldest);
  }
  return promise;
}

/**
 * useModelsCatalog keeps the catalog's suggestions for the models of a row.
 * It asks once the row can be identified and the models are known (models
 * null means "not yet", as while the provider's listing is being fetched),
 * and again when either changes; an answer about a question no longer asked
 * is dropped.
 */
export function useModelsCatalog(
  row: ProviderRow | undefined,
  models: CatalogModel[] | null,
): CatalogSuggestions {
  const key =
    row && models && models.length > 0 && providerRowFetchable(row)
      ? JSON.stringify(modelsCatalogRequest(row, models))
      : "";
  const [state, setState] = useState<{
    key: string;
    value: CatalogSuggestions;
  }>({ key: "", value: {} });
  useEffect(() => {
    if (!key) {
      return;
    }
    let live = true;
    const parsed = JSON.parse(key) as ReturnType<typeof modelsCatalogRequest>;
    void lookupModelsCatalog(
      { name: parsed.name, type: parsed.type, api_base: parsed.api_base },
      parsed.models,
    ).then((value) => {
      if (live) {
        setState({ key, value });
      }
    });
    return () => {
      live = false;
    };
  }, [key]);
  // An answer about another question than the one asked now is not shown.
  return key && state.key === key ? state.value : NO_SUGGESTIONS;
}

const NO_SUGGESTIONS: CatalogSuggestions = {};

/** The window the provider's own listing reports for one model, undefined
 * when it reports none or the listing fails. The request is the automatic
 * one: it never runs the row's api_key_command. */
async function reportedWindow(
  row: ProviderRow,
  id: string,
): Promise<number | undefined> {
  try {
    const res = await fetch("/coddy/providers/models", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(providerModelsRequest(row)),
    });
    const data = (await res.json().catch(() => ({}))) as {
      ok?: boolean;
      models?: { id?: string; context_window?: number }[];
    };
    if (!res.ok || !data.ok) {
      return undefined;
    }
    const n = data.models?.find((m) => m.id === id)?.context_window;
    return typeof n === "number" && Number.isFinite(n) && n > 0
      ? Math.floor(n)
      : undefined;
  } catch {
    return undefined;
  }
}

/**
 * lookupRowSuggestion answers what the catalog suggests for one logical model
 * of a row, its max_tokens measured against the window the provider itself
 * reports for the model when it reports one. The fields of one row share the
 * answer for CATALOG_ANSWER_TTL_MS.
 */
export function lookupRowSuggestion(
  row: ProviderRow,
  id: string,
): Promise<CatalogSuggestion | undefined> {
  if (!providerRowFetchable(row) || id === "") {
    return Promise.resolve(undefined);
  }
  const key = JSON.stringify([modelsCatalogRequest(row, []), id]);
  const now = Date.now();
  const hit = rowAnswers.get(key);
  if (hit && now - hit.at < CATALOG_ANSWER_TTL_MS) {
    return hit.promise;
  }
  const promise = reportedWindow(row, id)
    .then((reported) =>
      lookupModelsCatalog(row, [{ id, context_window: reported }]),
    )
    .then((got) => {
      const s = got[id];
      if (!s) {
        rowAnswers.delete(key);
      }
      return s;
    });
  rowAnswers.delete(key);
  rowAnswers.set(key, { at: now, promise });
  while (rowAnswers.size > MAX_ENTRIES) {
    const oldest = rowAnswers.keys().next().value;
    if (oldest === undefined) {
      break;
    }
    rowAnswers.delete(oldest);
  }
  return promise;
}

/**
 * useRowCatalogSuggestion keeps what the catalog suggests for the model a
 * logical model row names (provider/api-model-id), given the provider row it
 * points at; undefined while unknown, or when the catalog knows nothing of it.
 */
export function useRowCatalogSuggestion(
  providerRow: ProviderRow | undefined,
  model: string,
): CatalogSuggestion | undefined {
  const slash = model.indexOf("/");
  const id = slash > 0 ? model.slice(slash + 1).trim() : "";
  const row =
    providerRow && providerRowFetchable(providerRow) && id !== ""
      ? providerRow
      : undefined;
  const key = row ? JSON.stringify([providerModelsRequest(row), id]) : "";
  const [state, setState] = useState<{
    key: string;
    value: CatalogSuggestion | undefined;
  }>({ key: "", value: undefined });
  useEffect(() => {
    if (!key) {
      return;
    }
    let live = true;
    const [body, wanted] = JSON.parse(key) as [ProviderRow, string];
    void lookupRowSuggestion(body, wanted).then((value) => {
      if (live) {
        setState({ key, value });
      }
    });
    return () => {
      live = false;
    };
  }, [key]);
  return key && state.key === key ? state.value : undefined;
}
