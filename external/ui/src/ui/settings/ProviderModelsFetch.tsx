import { useEffect } from "react";

import { useT } from "../i18n/I18nProvider";
import { formatTurnTokens } from "../chat/turnProgress";
import {
  providerRowFetchable,
  useProviderModels,
  type ProviderRow,
} from "./useProviderModels";

function IconWarn() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 14 14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      aria-hidden="true"
    >
      <path d="M7 1.6 13 12H1L7 1.6Z" strokeLinejoin="round" />
      <path d="M7 5.4v3" strokeLinecap="round" />
      <circle cx="7" cy="10" r="0.8" fill="currentColor" stroke="none" />
    </svg>
  );
}

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
 * check instead of the add control, and an id the document lists but the
 * provider no longer advertises shows as a warn row. The fetch fires once
 * when the form opens (a row without a name and a type cannot be fetched),
 * the button stays as the manual refetch.
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

  // Opening a saved provider's form fetches its advertised list at once; a
  // brand-new row has no name/type yet, so the effect is a no-op for it.
  useEffect(() => {
    if (providerRowFetchable(provider)) {
      void fetchModels(provider);
    }
    // Mount-only: the form remounts per row; the button is the manual refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const prefix = `${name}/`;
  const advertised = new Set(models.map((m) => `${prefix}${m.id}`));
  const stale =
    fetched && !error && name
      ? existingModels.filter(
          (id) => id.startsWith(prefix) && !advertised.has(id),
        )
      : [];

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
          {stale.map((full) => (
            <li
              key={full}
              className="provider-model-row is-stale"
              data-testid={`provider-model-stale-${full.slice(prefix.length)}`}
              title={t("settings.providers.modelNotAdvertised", { id: full })}
            >
              <span className="provider-model-stale-icon">
                <IconWarn />
              </span>
              <span className="provider-model-id">
                {full.slice(prefix.length)}
              </span>
              <span className="provider-model-name">
                {t("settings.providers.modelNotAdvertised", { id: full })}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </fieldset>
  );
}
