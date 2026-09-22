import { Combobox } from "./Combobox";
import {
  providerRowFetchable,
  useProviderModels,
  type ProviderRow,
} from "./useProviderModels";
import { useT } from "../i18n/I18nProvider";

/**
 * ModelField edits a logical model id (provider/api-model-id). "Fetch models"
 * pulls the advertised model lists of every provider in the settings document
 * (Kilo-style) into the model combobox - a separate provider picker is
 * redundant because each fetched option already carries its provider prefix.
 * The combobox is also editable so the id can be typed manually when no list
 * is available.
 */
export function ModelField(props: {
  value: string;
  onChange: (v: string) => void;
  providers: ProviderRow[];
  label?: string | undefined;
}) {
  const { value, onChange, providers } = props;
  const { t } = useT();
  const label = props.label ?? t("settings.field.modelIdFallback");
  const { loading, models, error, fetched, fetchModels } = useProviderModels();

  const modelOptions = models.map((m) => ({
    value: m.id,
    label: m.name ? `${m.name} — ${m.id}` : m.id,
  }));

  return (
    <div className="settings-row" data-testid="model-field">
      <span className="settings-label">{label}</span>

      <div className="model-field-controls">
        <Combobox
          value={value}
          onChange={onChange}
          options={modelOptions}
          ariaLabel={label}
          testid="model-field-model"
          placeholder={t("settings.field.modelPlaceholder")}
        />
        <button
          type="button"
          className="settings-btn"
          data-testid="model-field-fetch"
          disabled={!providers.some(providerRowFetchable) || loading}
          onClick={() => void fetchModels(providers)}
        >
          {loading
            ? t("settings.field.fetching")
            : t("settings.field.fetchModels")}
        </button>
      </div>

      {fetched && error ? (
        <p className="settings-field-desc">
          {t("settings.field.fetchError", { error })}
        </p>
      ) : null}
      {fetched && !error && models.length === 0 ? (
        <p className="settings-field-desc">{t("settings.field.noModels")}</p>
      ) : null}
    </div>
  );
}
