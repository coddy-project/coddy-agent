import { useEffect, useRef, useState } from "react";

import { FieldLabel } from "./FieldHint";
import { IconSync } from "./icons";
import {
  providerRowFetchable,
  useAutoProviderModels,
  type ProviderRow,
} from "./useProviderModels";
import { useT } from "../i18n/I18nProvider";

/** What the runtime measures against when neither the config nor the
 * provider's listing names a window (internal/session). */
export const DEFAULT_CONTEXT_WINDOW = 128000;

/**
 * ContextWindowField edits models[].max_context_tokens with the provider's
 * own answer at hand. 0 (or no value) means "read the window from the
 * provider's model listing, else 128000" at run time, so an empty field
 * would say nothing: the field asks the provider (POST /coddy/providers/models
 * with the row as the settings document holds it) and shows the window it
 * reports for this model id as the placeholder - the number the runtime will
 * use. The refresh control asks again and writes that number into the field,
 * for an operator who wants it pinned; a model added from the provider form's
 * list arrives with it already written.
 */
export function ContextWindowField(props: {
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
  const { t } = useT();
  const [note, setNote] = useState("");

  const slash = model.indexOf("/");
  const provider = slash > 0 ? model.slice(0, slash) : "";
  const id = slash > 0 ? model.slice(slash + 1).trim() : "";
  const row =
    providerRow && providerRowFetchable(providerRow) && id !== ""
      ? providerRow
      : undefined;
  const { loading, models, fetched, error, fetchModels } =
    useAutoProviderModels(row);

  // The note answers an explicit ask about one model id; another id (or
  // another provider) makes it stale.
  useEffect(() => {
    setNote("");
  }, [model]);

  // A fetch resolves later than the render that started it: write through
  // the newest onChange, or a sibling field edited meanwhile would be undone,
  // and only while the model on screen is still the one that was asked about.
  const onChangeRef = useRef(onChange);
  const modelRef = useRef(model);
  useEffect(() => {
    onChangeRef.current = onChange;
    modelRef.current = model;
  });

  const stored =
    typeof value === "number" && Number.isFinite(value) && value > 0
      ? Math.floor(value)
      : 0;
  const reported = fetched
    ? models.find((m) => m.id === id)?.context_window
    : undefined;

  let placeholder: string;
  if (loading) {
    placeholder = t("settings.field.fetching");
  } else if (reported) {
    placeholder = t("settings.contextWindow.reported", {
      tokens: String(reported),
      provider,
    });
  } else {
    placeholder = t("settings.contextWindow.default", {
      tokens: String(DEFAULT_CONTEXT_WINDOW),
    });
  }

  return (
    <div className="settings-row" data-testid="context-window-field">
      <FieldLabel label={label} description={props.description} />
      <div className="context-window-controls">
        <input
          className="settings-input"
          type="number"
          min={0}
          value={stored > 0 ? String(stored) : ""}
          placeholder={placeholder}
          aria-label={label}
          data-testid="context-window-input"
          onChange={(e) => {
            setNote("");
            const n = e.target.valueAsNumber;
            onChange(Number.isFinite(n) && n > 0 ? Math.floor(n) : 0);
          }}
        />
        <button
          type="button"
          className="settings-btn settings-btn-icon"
          data-testid="context-window-fetch"
          disabled={!row || loading}
          title={t("settings.contextWindow.fetch")}
          aria-label={t("settings.contextWindow.fetch")}
          onClick={() => {
            if (!row) {
              return;
            }
            setNote("");
            const askedFor = model;
            void fetchModels(row, { runCommand: true }).then((list) => {
              // null: a newer fetch or an unmount dropped the answer. Another
              // model on screen: the answer is about the one it replaced.
              if (list === null || modelRef.current !== askedFor) {
                return;
              }
              const window = list.find((m) => m.id === id)?.context_window;
              if (window) {
                onChangeRef.current(window);
              } else {
                setNote("asked");
              }
            });
          }}
        >
          <IconSync className={loading ? "settings-icon-spin" : undefined} />
        </button>
      </div>
      {note && !loading ? (
        <p
          className={`settings-field-desc${error ? " settings-error" : ""}`}
          data-testid="context-window-note"
        >
          {error
            ? t("settings.contextWindow.fetchError", { error })
            : t("settings.contextWindow.notReported", { provider, id })}
        </p>
      ) : null}
    </div>
  );
}
