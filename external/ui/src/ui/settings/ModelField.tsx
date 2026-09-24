import { Combobox } from "./Combobox";
import type { ProviderRow } from "./useProviderModels";
import { useT } from "../i18n/I18nProvider";

function str(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/**
 * ModelField edits a logical model id ("provider/api-model-id") as two plain
 * fields: the provider, a combobox over the providers section of the settings
 * document (a name the document does not list yet can still be typed), and
 * the model id as the provider's API knows it. Picking an id out of what a
 * provider advertises is the provider form's job (ProviderModelList), so this
 * field lists nothing and fetches nothing. The value splits on its first
 * slash, so an id that itself contains slashes survives the round trip.
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

  const slash = value.indexOf("/");
  const provider = slash >= 0 ? value.slice(0, slash) : "";
  const modelId = slash >= 0 ? value.slice(slash + 1) : value;
  const emit = (p: string, id: string) => onChange(p ? `${p}/${id}` : id);

  const names = providers
    .map((p) => str(p.name).trim())
    .filter((n) => n !== "");

  return (
    <>
      <div className="settings-row" data-testid="model-field">
        <span className="settings-label">{t("settings.field.provider")}</span>
        <Combobox
          value={provider}
          onChange={(p) => emit(p, modelId)}
          options={names.map((n) => ({ value: n }))}
          ariaLabel={t("settings.field.provider")}
          testid="model-field-provider"
          placeholder={t("settings.field.providerPlaceholder")}
        />
      </div>
      <div className="settings-row">
        <span className="settings-label">{label}</span>
        <input
          className="settings-input"
          type="text"
          value={modelId}
          aria-label={label}
          data-testid="model-field-model"
          placeholder={t("settings.field.modelPlaceholder")}
          onChange={(e) => emit(provider, e.target.value)}
        />
      </div>
    </>
  );
}
