import { FieldLabel } from "./FieldHint";
import { SwitchField } from "./SwitchField";
import type { ProviderRow } from "./useProviderModels";
import { useRowCatalogSuggestion } from "./useModelsCatalog";
import { useT } from "../i18n/I18nProvider";

/**
 * CatalogHint is the line under a logical model's field that says what the
 * models catalog suggests for it, with a button that writes the suggestion
 * into the field. It is a fetch result, so it is a paragraph under the
 * field, not a description behind the (i). Nothing is written without the
 * click: a value the operator set stays until they apply another.
 */
export function CatalogHint(props: {
  text: string;
  onApply: () => void;
  testId: string;
}) {
  const { t } = useT();
  return (
    <p className="settings-field-desc catalog-hint" data-testid={props.testId}>
      <span>{props.text}</span>{" "}
      <button
        type="button"
        className="catalog-hint-apply"
        data-testid={`${props.testId}-apply`}
        onClick={props.onApply}
      >
        {t("settings.catalog.apply")}
      </button>
    </p>
  );
}

function storedNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : 0;
}

/**
 * MaxTokensField edits models[].max_tokens and offers the max_tokens the
 * models catalog suggests for the model while the field holds another value
 * - the seed of a new row (4096) included, which is not a value anybody chose.
 */
export function MaxTokensField(props: {
  value: unknown;
  onChange: (v: unknown) => void;
  /** The logical model id, provider/api-model-id. */
  model: string;
  /** The provider row the id points at, as the document holds it. */
  providerRow?: ProviderRow | undefined;
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, model, providerRow, label } = props;
  const { t, locale } = useT();
  const suggestion = useRowCatalogSuggestion(providerRow, model);
  const stored = storedNumber(value);
  const suggested = suggestion?.max_tokens;
  return (
    <div className="settings-row" data-testid="max-tokens-field">
      <FieldLabel label={label} description={props.description} />
      <input
        className="settings-input"
        type="number"
        min={0}
        value={stored > 0 ? String(stored) : ""}
        aria-label={label}
        data-testid="max-tokens-input"
        onChange={(e) => {
          const n = e.target.valueAsNumber;
          onChange(Number.isFinite(n) && n > 0 ? Math.floor(n) : 0);
        }}
      />
      {suggested && suggested !== stored ? (
        <CatalogHint
          testId="max-tokens-catalog"
          text={t("settings.catalog.maxTokens", {
            tokens: new Intl.NumberFormat(locale).format(suggested),
          })}
          onApply={() => onChange(suggested)}
        />
      ) : null}
    </div>
  );
}

/**
 * MultimodalField is the models[].multimodal switch with the catalog's word
 * on it: while the catalog says the model reads images and the switch is
 * off, a line under it offers to turn it on.
 */
export function MultimodalField(props: {
  value: unknown;
  onChange: (v: unknown) => void;
  model: string;
  providerRow?: ProviderRow | undefined;
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, model, providerRow, label } = props;
  const { t } = useT();
  const suggestion = useRowCatalogSuggestion(providerRow, model);
  const checked = value === true;
  return (
    <div data-testid="multimodal-field">
      <SwitchField
        checked={checked}
        onChange={(next) => onChange(next)}
        label={label}
        description={props.description}
      />
      {suggestion?.multimodal && !checked ? (
        <CatalogHint
          testId="multimodal-catalog"
          text={t("settings.catalog.readsImages")}
          onApply={() => onChange(true)}
        />
      ) : null}
    </div>
  );
}
