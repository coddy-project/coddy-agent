import { useT } from "../i18n/I18nProvider";
import { formatTurnTokens } from "../chat/turnProgress";
import {
  providerRowFetchable,
  useProviderModels,
  type ProviderRow,
} from "./useProviderModels";

function IconCheckSmall() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 14 14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M3 7.5l2.5 2.5L11 4.5" />
    </svg>
  );
}

/**
 * ProviderModelsFetch sits at the bottom of the provider edit form: it asks
 * the provider for the model list it advertises (POST /coddy/providers/models
 * with the row as edited, unsaved fields included) and offers each returned id
 * as a one-click addition to Logical models. A context window the listing
 * reports shows next to the id; an id the document already lists shows a
 * check instead of the add control.
 */
export function ProviderModelsFetch(props: {
  provider: ProviderRow;
  existingModels: string[];
  onAddModel: (id: string) => void;
}) {
  const { provider, existingModels, onAddModel } = props;
  const { t } = useT();
  const { loading, models, error, fetched, fetchModels } = useProviderModels();
  const name = typeof provider.name === "string" ? provider.name.trim() : "";
  const listed = new Set(existingModels);

  return (
    <fieldset
      className="settings-fieldset provider-models-fetch"
      data-testid="provider-models-fetch"
    >
      <legend>{t("settings.providers.modelsLegend")}</legend>
      <button
        type="button"
        className="settings-btn"
        data-testid="provider-fetch-models"
        disabled={!providerRowFetchable(provider) || loading}
        onClick={() => void fetchModels(provider)}
      >
        {loading
          ? t("settings.field.fetching")
          : t("settings.field.fetchModels")}
      </button>
      {fetched && error ? (
        <p className="settings-field-desc provider-models-error">
          {t("settings.field.fetchError", { error })}
        </p>
      ) : null}
      {fetched && !error && models.length === 0 ? (
        <p className="settings-field-desc">{t("settings.field.noModels")}</p>
      ) : null}
      {models.length > 0 ? (
        <ul className="provider-models-list">
          {models.map((m) => {
            const full = `${name}/${m.id}`;
            const added = listed.has(full);
            return (
              <li key={m.id} className="provider-model-row">
                <span className="provider-model-id">{m.id}</span>
                {m.name ? (
                  <span className="provider-model-name">{m.name}</span>
                ) : null}
                {m.context_window ? (
                  <span
                    className="provider-model-ctx"
                    title={t("settings.providers.contextWindow", {
                      tokens: m.context_window.toLocaleString("en-US"),
                    })}
                  >
                    {formatTurnTokens(m.context_window)}
                  </span>
                ) : null}
                <button
                  type="button"
                  className="provider-model-add"
                  data-testid={`provider-model-add-${m.id}`}
                  disabled={added}
                  title={
                    added
                      ? t("settings.providers.modelListed", { id: full })
                      : t("settings.providers.addModel", { id: full })
                  }
                  aria-label={
                    added
                      ? t("settings.providers.modelListed", { id: full })
                      : t("settings.providers.addModel", { id: full })
                  }
                  onClick={() => onAddModel(full)}
                >
                  {added ? <IconCheckSmall /> : "+"}
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </fieldset>
  );
}
