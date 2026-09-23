import { useT } from "../i18n/I18nProvider";
import type { ProviderRow } from "./useProviderModels";

/**
 * ModelField edits a logical model id ("provider/api-model-id") as two plain
 * fields: a provider select fed by the providers section of the settings
 * document, and the model id as the provider's API knows it. The advertised
 * model list is fetched from the provider form instead, so this control
 * carries no fetch button; the provider part still accepts a name the document
 * does not list yet (it is preserved as an extra option), and the id part may
 * itself contain slashes - the value splits on the first one.
 */
export function ModelField(props: {
  value: string;
  onChange: (v: string) => void;
  providers: ProviderRow[];
  label?: string | undefined;
}) {
  const { value, onChange, providers } = props;
  const { t } = useT();

  const slash = value.indexOf("/");
  const provider = slash >= 0 ? value.slice(0, slash) : "";
  const modelId = slash >= 0 ? value.slice(slash + 1) : value;

  const emit = (p: string, id: string) => onChange(p ? `${p}/${id}` : id);

  const names = providers
    .map((p) => (typeof p.name === "string" ? p.name.trim() : ""))
    .filter((n) => n !== "");
  const providerOptions =
    provider !== "" && !names.includes(provider)
      ? [provider, ...names]
      : names;

  return (
    <>
      <div className="settings-row" data-testid="model-field">
        <span className="settings-label">{t("settings.field.provider")}</span>
        <select
          className="settings-input"
          value={provider}
          aria-label={t("settings.field.provider")}
          data-testid="model-field-provider"
          onChange={(e) => emit(e.target.value, modelId)}
        >
          <option value="">
            {t("settings.field.providerPlaceholder")}
          </option>
          {providerOptions.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </div>
      <div className="settings-row">
        <span className="settings-label">
          {props.label ?? t("settings.field.modelIdFallback")}
        </span>
        <input
          className="settings-input"
          value={modelId}
          aria-label={t("settings.field.modelIdFallback")}
          data-testid="model-field-model"
          placeholder={t("settings.field.modelPlaceholder")}
          onChange={(e) => emit(provider, e.target.value)}
        />
      </div>
    </>
  );
}
