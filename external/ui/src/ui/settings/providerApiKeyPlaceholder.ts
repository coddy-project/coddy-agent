import { translate } from "../i18n/i18n";

const PROVIDER_NAME_RE = /^[a-zA-Z][a-zA-Z0-9_-]*$/;

/** Mirrors internal/config.ProviderAPIKeyEnvVarName for UI placeholder text. */
export function providerAPIKeyEnvVarName(providerName: string): string {
  const s = providerName.trim();
  if (!PROVIDER_NAME_RE.test(s)) {
    return "";
  }
  return s.replace(/-/g, "_").toUpperCase() + "_API_KEY";
}

/** Placeholder for the provider api_key field; updates with the sibling provider name. */
export function providerApiKeyFieldPlaceholder(providerName: string): string {
  const env = providerAPIKeyEnvVarName(providerName);
  if (env) {
    // Build "${ENV}" with string concat so minifiers never treat "${...}" as a template slot.
    return translate("settings.field.apiKeyPlaceholder", {
      env: "$" + "{" + env + "}",
      varToken: "$" + "{" + "VAR" + "}",
    });
  }
  return translate("settings.field.apiKeyPlaceholderInvalid");
}
