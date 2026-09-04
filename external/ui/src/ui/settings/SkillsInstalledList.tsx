import { IconTrash } from "./SchemaForm";
import { useT } from "../i18n/I18nProvider";
import { IconDownload, IconPlug } from "./settingsIcons";
import type { InstalledSkill, SkillUpdate } from "./skillsApi";

/** Installed-skills list with version badges, enable switches, and actions. */
export function SkillsInstalledList(props: {
  installed: InstalledSkill[];
  updates: Record<string, SkillUpdate>;
  busy: Record<string, boolean>;
  loading: boolean;
  justInstalled: string | null;
  onToggle: (sk: InstalledSkill) => void;
  onRemove: (sk: InstalledSkill) => void;
  onUpdate: (sk: InstalledSkill) => void;
}) {
  const {
    installed,
    updates,
    busy,
    loading,
    justInstalled,
    onToggle,
    onRemove,
    onUpdate,
  } = props;
  const { t } = useT();

  if (installed.length === 0) {
    return loading ? (
      <p className="settings-muted">{t("skills.loading")}</p>
    ) : (
      <p className="settings-muted">{t("skills.empty")}</p>
    );
  }

  return (
    <ul className="skills-list">
      {installed.map((sk) => {
        const upd = updates[sk.name];
        const hasUpdate = !!upd?.update_available;
        return (
          <li
            key={sk.name}
            className={`skills-list-item${sk.enabled ? "" : " is-disabled"}${sk.name === justInstalled ? " is-just-installed" : ""}`}
          >
            <IconPlug />
            <div className="skills-list-item-text">
              <div className="skills-list-item-name">
                {sk.name}
                {sk.version ? (
                  <span className="skills-list-item-version">
                    v{sk.version}
                  </span>
                ) : null}
                {sk.source ? (
                  <span
                    className="skills-list-item-badge"
                    title={t("skills.badge.syncedFrom", {
                      source: sk.source,
                    })}
                  >
                    {t("skills.badge.remote")}
                  </span>
                ) : null}
              </div>
              {sk.description ? (
                <div className="skills-list-item-desc">{sk.description}</div>
              ) : null}
            </div>
            {hasUpdate ? (
              <button
                type="button"
                className="settings-btn settings-btn-icon settings-btn-primary skills-update-btn"
                disabled={!!busy[sk.name]}
                onClick={() => onUpdate(sk)}
                title={t("skills.update.title", {
                  name: sk.name,
                  from: upd?.version || sk.version || "?",
                  to: upd?.latest,
                })}
                aria-label={t("skills.update.aria", {
                  name: sk.name,
                  version: upd?.latest,
                })}
                data-testid={`skills-update-${sk.name}`}
              >
                <IconDownload />
              </button>
            ) : null}
            <button
              type="button"
              role="switch"
              aria-checked={sk.enabled}
              className="skill-switch"
              disabled={!!busy[sk.name]}
              onClick={() => onToggle(sk)}
              title={
                sk.enabled
                  ? t("skills.switch.enabledTitle")
                  : t("skills.switch.disabledTitle")
              }
              aria-label={t(
                sk.enabled
                  ? "skills.switch.disableAria"
                  : "skills.switch.enableAria",
                { name: sk.name },
              )}
              data-testid={`skills-toggle-${sk.name}`}
            >
              <span className="skill-switch-thumb" />
            </button>
            <button
              type="button"
              className="settings-btn settings-btn-icon settings-btn-danger"
              disabled={!!busy[sk.name] || !!sk.readonly}
              onClick={() => onRemove(sk)}
              title={
                sk.readonly
                  ? t("skills.delete.bundledTitle")
                  : t("skills.delete.title")
              }
              aria-label={t("skills.delete.aria", { name: sk.name })}
              data-testid={`skills-delete-${sk.name}`}
            >
              <IconTrash />
            </button>
          </li>
        );
      })}
    </ul>
  );
}
