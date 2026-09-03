import { useEffect } from "react";
import { Combobox } from "./Combobox";
import { IconTrash } from "./SchemaForm";
import { useReasoningLevels } from "./useReasoningLevels";
import { useT } from "../i18n/I18nProvider";

/** Level names the config package defines. The combobox stays editable, so a
 * provider tier that detection never emits (Codex "xhigh") can still be typed. */
const KNOWN_LEVELS = ["minimal", "none", "low", "medium", "high"];

/**
 * ReasoningLevelsField edits models[].reasoning_levels, whose three states mean
 * three different things and are easy to confuse:
 *
 *   key absent  - auto-detect the levels from the model id (the default)
 *   []          - hide the composer's reasoning selector for this model
 *   [a, b, ...] - offer exactly these levels
 *
 * The generic array editor can only ever produce the last two, so a model added
 * through Settings could never go back to auto-detection. This field names the
 * current state, offers "Fetch reasoning levels" to fill the list with what the
 * gateway detects for the model id being edited, and offers a way back to the
 * auto-detected default.
 */
export function ReasoningLevelsField(props: {
  value: unknown;
  onChange: (v: unknown) => void;
  model: string;
  /** Wire type of the provider the model id currently points at, as shown in
   * the (possibly unsaved) form; decides the Codex remap server-side. */
  providerType?: string | undefined;
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, model, providerType, label } = props;
  const { t } = useT();
  const { loading, detected, error, fetched, fetchLevels, reset } =
    useReasoningLevels();

  // undefined / null is "key absent"; anything else is an explicit list.
  const explicit = Array.isArray(value) ? value.map((v) => `${v}`) : null;
  const modelId = model.trim();

  // Whatever the last fetch said was about the previous id, and an answer still
  // in flight for it must not land on the new one.
  useEffect(() => {
    reset();
  }, [modelId, reset]);

  const options = KNOWN_LEVELS.map((v) => ({ value: v }));

  // A manual edit is the operator's newest intent: it abandons any fetch still
  // in flight so a late answer cannot replace what they just typed or removed.
  const editLevels = (next: string[]) => {
    reset();
    onChange(next);
  };

  // The list the operator is looking at always wins over fetch feedback: a
  // fetch that filled the list has made itself visible, and one that failed or
  // found nothing only matters while the key is still absent.
  const status = () => {
    if (explicit !== null) {
      return explicit.length === 0
        ? t("settings.reasoning.hidden")
        : t("settings.reasoning.overridden");
    }
    if (fetched && error) {
      return t("settings.reasoning.fetchError", { error });
    }
    if (fetched && !detected) {
      return t("settings.reasoning.noneDetected");
    }
    return t("settings.reasoning.autoDetected");
  };

  return (
    <div className="settings-row" data-testid="reasoning-levels-field">
      <span className="settings-label">{label}</span>
      {props.description ? (
        <p className="settings-field-desc">{props.description}</p>
      ) : null}

      <div className="model-field-controls reasoning-levels-actions">
        <button
          type="button"
          className="settings-btn"
          data-testid="reasoning-levels-fetch"
          disabled={!modelId || loading}
          onClick={() => {
            void fetchLevels(modelId, providerType).then((next) => {
              // null: the answer arrived too late (id retyped, field gone) and
              // was dropped. Empty: the id has no reasoning family, and writing
              // [] would read as the explicit opt-out, so leave the field alone
              // and let the status line explain.
              if (next && next.length > 0) {
                onChange(next);
              }
            });
          }}
        >
          {loading
            ? t("settings.reasoning.fetching")
            : t("settings.reasoning.fetch")}
        </button>
        {explicit === null ? null : (
          <button
            type="button"
            className="settings-btn"
            data-testid="reasoning-levels-auto"
            onClick={() => {
              reset();
              // undefined drops the key from the saved JSON, restoring detection.
              onChange(undefined);
            }}
          >
            {t("settings.reasoning.useAuto")}
          </button>
        )}
      </div>

      <p className="settings-field-desc" data-testid="reasoning-levels-status">
        {status()}
      </p>

      {explicit === null ? null : (
        <ul className="settings-array">
          {explicit.map((level, i) => (
            <li key={i} className="settings-array-row">
              <div className="settings-array-row-field">
                <Combobox
                  value={level}
                  onChange={(v) => {
                    const next = [...explicit];
                    next[i] = v;
                    editLevels(next);
                  }}
                  options={options}
                  ariaLabel={`${label} ${i + 1}`}
                  testid={`reasoning-levels-item-${i}`}
                />
              </div>
              <button
                type="button"
                className="settings-btn settings-btn-icon settings-btn-danger settings-array-remove"
                aria-label={t("settings.array.removeAria")}
                title={t("settings.array.removeTitle")}
                data-testid={`reasoning-levels-remove-${i}`}
                onClick={() => editLevels(explicit.filter((_, j) => j !== i))}
              >
                <IconTrash />
              </button>
            </li>
          ))}
        </ul>
      )}

      <button
        type="button"
        className="settings-btn"
        data-testid="reasoning-levels-add"
        onClick={() => editLevels([...(explicit ?? []), ""])}
      >
        {t("settings.array.add")}
      </button>
    </div>
  );
}
