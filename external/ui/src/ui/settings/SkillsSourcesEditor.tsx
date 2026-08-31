import { IconTrash } from "./SchemaForm";
import { useT } from "../i18n/I18nProvider";
import { IconCheck, IconSync } from "./settingsIcons";

// Flash key for the "Sync all" action (distinct from any source string).
export const SYNC_ALL_KEY = " all";

/**
 * SkillsSourcesEditor renders the `skills.sources` array (config-backed via
 * onChange) with a per-marketplace Sync button and, in the footer, Add (left)
 * plus Sync all (right). It replaces the generic array control via
 * SchemaForm's fieldOverride hook.
 */
export function SkillsSourcesEditor(props: {
  value: string[];
  onChange: (next: string[]) => void;
  onSyncOne: (source: string) => void;
  onSyncAll: () => void;
  syncing: boolean;
  flash: string | null;
}) {
  const { value, onChange, onSyncOne, onSyncAll, syncing, flash } = props;
  const { t } = useT();
  const sources = Array.isArray(value) ? value : [];
  return (
    <fieldset className="settings-fieldset">
      <legend>{t("skills.sources.legend")}</legend>
      <p className="settings-field-desc">{t("skills.sources.description")}</p>
      <ul className="settings-array">
        {sources.map((src, i) => (
          <li key={i} className="settings-array-row">
            <div className="settings-array-row-field">
              <input
                className="settings-input"
                type="text"
                value={src}
                placeholder={t("skills.sources.placeholder")}
                onChange={(e) => {
                  const next = [...sources];
                  next[i] = e.target.value;
                  onChange(next);
                }}
              />
            </div>
            <button
              type="button"
              className={`settings-btn settings-btn-icon${flash === src ? " is-synced" : ""}`}
              disabled={syncing || !src.trim()}
              onClick={() => onSyncOne(src)}
              title={
                flash === src
                  ? t("skills.sources.syncedTitle")
                  : t("skills.sources.syncTitle", { source: src.trim() })
              }
              aria-label={t("skills.sources.syncAria")}
              data-testid={`skills-sync-source-${i}`}
            >
              {flash === src ? <IconCheck /> : <IconSync />}
            </button>
            <button
              type="button"
              className="settings-btn settings-btn-icon settings-btn-danger settings-array-remove"
              onClick={() => onChange(sources.filter((_, j) => j !== i))}
              title={t("skills.sources.removeTitle")}
              aria-label={t("skills.sources.removeAria")}
            >
              <IconTrash />
            </button>
          </li>
        ))}
      </ul>
      <div className="skills-sources-footer">
        <button
          type="button"
          className="settings-btn"
          onClick={() => onChange([...sources, ""])}
        >
          {t("skills.sources.add")}
        </button>
        <button
          type="button"
          className={`settings-btn skills-sync-all-btn${flash === SYNC_ALL_KEY ? " is-synced" : ""}`}
          disabled={syncing || sources.length === 0}
          onClick={onSyncAll}
          title={t("skills.sources.syncAllTitle")}
          data-testid="skills-sync-all"
        >
          {flash === SYNC_ALL_KEY ? (
            <>
              <IconCheck />
              <span>{t("skills.sources.completed")}</span>
            </>
          ) : (
            <>
              <IconSync />
              <span>{t("skills.sources.syncAll")}</span>
            </>
          )}
        </button>
      </div>
    </fieldset>
  );
}
