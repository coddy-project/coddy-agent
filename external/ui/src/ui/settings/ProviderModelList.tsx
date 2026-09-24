import { useMemo, useState } from "react";

import { formatTurnTokens } from "../chat/turnProgress";
import { useT } from "../i18n/I18nProvider";
import { IconCheck, IconPlus, IconSync } from "./icons";
import {
  modelDisplayName,
  providerRowFetchable,
  useAutoProviderModels,
  type ProviderRow,
} from "./useProviderModels";

/** Lists longer than this get a filter field above the rows. */
export const PROVIDER_MODELS_FILTER_THRESHOLD = 8;

function str(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/**
 * ProviderModelList closes a provider's edit form: the models the provider
 * advertises, fetched through POST /coddy/providers/models with the row as
 * the form holds it (unsaved edits included). Each row is the id, the context
 * window the listing reports, and a toggle right after them: a plus files
 * provider/id under Logical models, a check says it is there and takes it out
 * again. An id Logical models files under this provider that the provider no
 * longer advertises stays in the list, amber, checked, its explanation in the
 * row's tooltip, so it can be taken out the same way.
 *
 * The list fetches itself when the form opens and when the row becomes another
 * provider (useAutoProviderModels); after an edit to the endpoint, the key or
 * the proxy it waits for the refresh icon beside the legend, which fetches at
 * once and is the only fetch that runs the row's api_key_command. A line
 * under the legend speaks only when there is nothing to list: fetching, an
 * error, an empty answer, a row without a name or a type.
 */
export function ProviderModelList(props: {
  provider: ProviderRow;
  /** Every models[].model of the document, so listed ids are told apart. */
  existingModels: string[];
  /** Files provider/id under Logical models, with the context window the
   * provider reported for it when it did. */
  onAddModel: (id: string, contextWindow?: number | undefined) => void;
  /** Takes provider/id out of Logical models. */
  onRemoveModel: (id: string) => void;
}) {
  const { provider, existingModels, onAddModel, onRemoveModel } = props;
  const { t, tp, locale } = useT();
  const { loading, models, error, fetched, fetchModels } =
    useAutoProviderModels(provider);
  const [filter, setFilter] = useState("");

  const name = str(provider.name).trim();
  const fetchable = providerRowFetchable(provider);
  const prefix = `${name}/`;

  const listed = useMemo(() => new Set(existingModels), [existingModels]);
  const advertised = useMemo(
    () => new Set(models.map((m) => prefix + m.id)),
    [models, prefix],
  );
  // Ids the document files under this provider that the provider no longer
  // advertises. Only a successful answer can say so.
  const stale =
    fetched && !error && name
      ? existingModels.filter(
          (id) => id.startsWith(prefix) && !advertised.has(id),
        )
      : [];

  const showFilter =
    models.length + stale.length > PROVIDER_MODELS_FILTER_THRESHOLD;
  // A query applies only while its field is on screen: a shorter list after a
  // refresh must not stay filtered by text nobody can see or clear.
  const query = showFilter ? filter.trim().toLowerCase() : "";
  const matches = (id: string, label?: string) =>
    query === "" ||
    id.toLowerCase().includes(query) ||
    (label ?? "").toLowerCase().includes(query);
  const visible = models.filter((m) => matches(m.id, modelDisplayName(m)));
  const visibleStale = stale.filter((id) => matches(id.slice(prefix.length)));
  const hasRows = models.length > 0 || stale.length > 0;

  // Words only when the list cannot speak for itself.
  let status = "";
  let statusIsError = false;
  if (!fetchable) {
    status = t("settings.providerModels.needsNameAndType");
  } else if (error && !loading) {
    status = t("settings.providerModels.fetchError", { error });
    statusIsError = true;
  } else if (!hasRows && (loading || !fetched)) {
    status = t("settings.field.fetching");
  } else if (!hasRows) {
    status = t("settings.providerModels.none");
  }

  const toggle = (
    full: string,
    isListed: boolean,
    contextWindow?: number,
    stale = false,
  ) => {
    const action = isListed
      ? t("settings.providerModels.removeModel", { id: full })
      : t("settings.providerModels.addModel", { id: full });
    // The amber colour and the row's tooltip say why a stale row is there;
    // the control's name says it too, for whoever does not see either.
    const name = stale
      ? `${action}. ${t("settings.providerModels.notAdvertised", { id: full })}`
      : action;
    const id = full.slice(prefix.length);
    return (
      <button
        type="button"
        className={`provider-models-toggle${isListed ? " is-listed" : ""}`}
        data-testid={`provider-model-toggle-${id}`}
        aria-pressed={isListed}
        title={action}
        aria-label={name}
        onClick={() =>
          isListed ? onRemoveModel(full) : onAddModel(full, contextWindow)
        }
      >
        {isListed ? <IconCheck /> : <IconPlus />}
      </button>
    );
  };

  return (
    <fieldset
      className="settings-fieldset provider-models-box"
      data-testid="provider-models"
    >
      <legend>
        <span className="settings-legend-line">
          {t("settings.providerModels.legend")}
          <button
            type="button"
            className="provider-models-refresh"
            data-testid="provider-models-refresh"
            disabled={!fetchable || loading}
            title={t("settings.field.fetchModels")}
            aria-label={t("settings.field.fetchModels")}
            onClick={() => void fetchModels(provider, { runCommand: true })}
          >
            <IconSync className={loading ? "settings-icon-spin" : undefined} />
          </button>
        </span>
      </legend>
      {status ? (
        <p
          className={`provider-models-status${statusIsError ? " settings-error" : ""}`}
          data-testid="provider-models-status"
        >
          {status}
        </p>
      ) : null}
      {showFilter ? (
        <input
          className="settings-input provider-models-filter"
          type="text"
          value={filter}
          placeholder={t("settings.providerModels.filterPlaceholder")}
          aria-label={t("settings.providerModels.filterPlaceholder")}
          data-testid="provider-models-filter"
          onChange={(e) => setFilter(e.target.value)}
        />
      ) : null}
      {hasRows ? (
        <ul className="provider-models-list" data-testid="provider-models-list">
          {visible.map((m) => {
            const full = prefix + m.id;
            const isListed = listed.has(full);
            return (
              <li
                key={m.id}
                className={`provider-models-item${isListed ? " is-listed" : ""}`}
                data-testid={`provider-model-${m.id}`}
                title={modelDisplayName(m)}
              >
                <span className="provider-models-item-id">{m.id}</span>
                {m.context_window ? (
                  <span
                    className="provider-models-item-ctx"
                    title={tp(
                      "settings.providerModels.contextWindow",
                      m.context_window,
                      {
                        tokens: new Intl.NumberFormat(locale).format(
                          m.context_window,
                        ),
                      },
                    )}
                  >
                    {formatTurnTokens(m.context_window)}
                  </span>
                ) : null}
                {toggle(full, isListed, m.context_window)}
              </li>
            );
          })}
          {visibleStale.map((full) => {
            const id = full.slice(prefix.length);
            return (
              <li
                key={full}
                className="provider-models-item is-listed is-stale"
                data-testid={`provider-model-stale-${id}`}
                title={t("settings.providerModels.notAdvertised", { id: full })}
              >
                <span className="provider-models-item-id">{id}</span>
                {toggle(full, true, undefined, true)}
              </li>
            );
          })}
          {visible.length === 0 && visibleStale.length === 0 ? (
            <li className="provider-models-empty settings-muted">
              {t("settings.providerModels.noMatch")}
            </li>
          ) : null}
        </ul>
      ) : null}
    </fieldset>
  );
}
