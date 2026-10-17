// The rules of sharing a model, as the settings form states them. They mirror
// internal/config/shared_models.go: the server remains authoritative and
// rejects a document that breaks them; the form only reports mistakes early.

import type { FetchedModel, ProviderRow } from "./useProviderModels";

/** The provider type of a model that another Coddy shares. */
export const CODDY_PROVIDER_TYPE = "coddy";

/**
 * The keys of a model row a coddy provider serves that have three positions:
 * written true, written false, or left to the remote's listing (absent).
 */
export const TRI_STATE_MODEL_KEYS: ReadonlySet<string> = new Set([
  "allow_reasoning_off",
]);

/** True for the provider type of a model that another Coddy shares. */
export function isCoddyType(type: unknown): boolean {
  return text(type) === CODDY_PROVIDER_TYPE;
}

// Mirrors sharedAliasPattern: no slash, so a local id provider/alias splits
// at the first one.
const SHARED_ALIAS_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

function text(v: unknown): string {
  return v === undefined || v === null ? "" : String(v).trim();
}

/**
 * Whether a shared_as value is acceptable. An empty alias keeps the model
 * private and is no mistake; anything else must match the alias pattern.
 */
export function sharedAliasIsValid(raw: unknown): boolean {
  const alias = text(raw);
  return alias === "" || SHARED_ALIAS_RE.test(alias);
}

/**
 * Mirrors config.ProviderUsesSubscriptionLogin for what the browser can see:
 * the credential behind the row is a subscription login rather than an API key.
 */
export function providerUsesSubscriptionLogin(
  row: ProviderRow | undefined,
): boolean {
  if (!row) {
    return false;
  }
  switch (text(row.type)) {
    case "codex":
    case "devin":
      return true;
    case "neuraldeep":
      return text(row.api_key) === "" && text(row.api_key_command) === "";
    default:
      return false;
  }
}

/** The providers[] row a models[].model id (provider/api-model-id) points at. */
export function providerRowOfModel(
  modelId: string,
  providers: ProviderRow[],
): ProviderRow | undefined {
  const slash = modelId.indexOf("/");
  if (slash <= 0) {
    return undefined;
  }
  const name = modelId.slice(0, slash).trim();
  return providers.find((p) => text(p.name) === name);
}

/**
 * Whether a model row is served by another Coddy (its provider is of type
 * coddy). Such a model cannot be shared again: the loader refuses shared_as on it.
 */
export function modelServedByCoddy(
  model: Record<string, unknown> | undefined,
  providers: ProviderRow[],
): boolean {
  return isCoddyType(providerRowOfModel(text(model?.model), providers)?.type);
}

/**
 * Whether a model row needs shared_subscription_ack: it is shared and its
 * provider runs through a subscription login.
 */
export function sharedSubscriptionAckNeeded(
  model: Record<string, unknown> | undefined,
  providers: ProviderRow[],
): boolean {
  if (!model || text(model.shared_as) === "") {
    return false;
  }
  return providerUsesSubscriptionLogin(
    providerRowOfModel(text(model.model), providers),
  );
}

/**
 * The context window a model added from a provider listing is written with.
 * A coddy row inherits it from the remote listing, so it is not pinned locally.
 */
export function contextWindowToPin(
  providerType: string | undefined,
  reported: number | undefined,
): number | undefined {
  return text(providerType) === CODDY_PROVIDER_TYPE ? undefined : reported;
}

/** The three states of a coddy model's allow_reasoning_off setting. */
export type TriState = "remote" | "yes" | "no";

/** The position a stored key shows: absent and null follow the remote. */
export function triStateOf(value: unknown): TriState {
  if (value === undefined || value === null) {
    return "remote";
  }
  return value === true ? "yes" : "no";
}

/** The value a position writes; undefined removes the key from the row. */
export function triStateValue(state: TriState): boolean | undefined {
  return state === "remote" ? undefined : state === "yes";
}

/**
 * The levels a coddy listing offers for a model, with off after the listed
 * levels when the remote allows it.
 */
export function listedLevels(model: FetchedModel | undefined): string[] {
  const levels = model?.reasoning_levels ?? [];
  if (levels.length === 0) {
    return [];
  }
  return model?.allow_reasoning_off ? [...levels, "off"] : [...levels];
}
