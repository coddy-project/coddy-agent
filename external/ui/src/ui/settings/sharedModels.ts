// The rules of sharing a model, as the settings form states them. They mirror
// internal/config/shared_models.go (docs/features/shared-models.md): the
// server stays the authority and refuses a document that breaks them, the form
// only says so before Save.

import type { ProviderRow } from "./useProviderModels";

/** The provider type of a model that another Coddy shares. */
export const CODDY_PROVIDER_TYPE = "coddy";

// Mirrors sharedAliasPattern: no slash, so a local id provider/alias splits
// at the first one.
const SHARED_ALIAS_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

function text(v: unknown): string {
  return v === undefined || v === null ? "" : String(v).trim();
}

/**
 * Whether a shared_as value is acceptable. An empty alias keeps the model
 * private and is no mistake; anything else must match the alias pattern. The
 * server trims the value before it checks it, so the form does too.
 */
export function sharedAliasIsValid(raw: unknown): boolean {
  const alias = text(raw);
  return alias === "" || SHARED_ALIAS_RE.test(alias);
}

/**
 * Mirrors config.ProviderUsesSubscriptionLogin for what the browser can see:
 * the credential behind the row is a subscription login rather than a key the
 * operator holds for the purpose. Provider types codex and devin always;
 * neuraldeep while the row names no key of its own (no api_key and no
 * api_key_command). The server also reads NAME_API_KEY from its environment,
 * which the form cannot see, so a neuraldeep row that has only that variable
 * asks for the acknowledgement here and does not need it there; a tick is
 * harmless, a missing one is refused on Save.
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
 * Whether a model row has to carry shared_subscription_ack: it is shared
 * (an alias is set) and its provider runs on a subscription login. Without
 * an alias the key does nothing, so nothing is asked.
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
 * The context window a model added from the provider's listing is written
 * with. A row of type coddy gets none: the remote's listing is its source, and
 * a number copied into max_context_tokens would pin the row to what the remote
 * said that day.
 */
export function contextWindowToPin(
  providerType: string | undefined,
  reported: number | undefined,
): number | undefined {
  return text(providerType) === CODDY_PROVIDER_TYPE ? undefined : reported;
}
