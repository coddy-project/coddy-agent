import { useCallback, useEffect, useState } from "react";
import {
  SchemaForm,
  type JsonSchema,
  type FieldOverride,
} from "./SchemaForm";
import { Switch } from "./Switch";
import { SwitchField } from "./SwitchField";
import { schemaFieldDesc } from "./schemaI18n";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";
import { apiSend } from "./settingsApi";
import {
  fetchAvailable,
  fetchInstalled,
  fetchUpdates,
  type AvailablePlugin,
  type InstalledSkill,
  type SkillUpdate,
} from "./skillsApi";
import { SkillsInstallSearch } from "./SkillsInstallSearch";
import { SkillsInstalledList } from "./SkillsInstalledList";
import { SkillsSourcesEditor, SYNC_ALL_KEY } from "./SkillsSourcesEditor";

/**
 * SkillsSection is the combined Skills tab: the schema-driven `skills.dirs`
 * editor, a config-backed remote-sources editor (add/list/remove with a
 * per-source and a Sync-all button), and the installed-skills list with
 * versions, an iOS-style enable switch, a Download-update action when a newer
 * version exists, and a Delete action (disabled for bundled read-only skills).
 */
export function SkillsSection(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const { schema, value, onChange } = props;
  const { t } = useT();
  const [installed, setInstalled] = useState<InstalledSkill[]>([]);
  const [updates, setUpdates] = useState<Record<string, SkillUpdate>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  // Transient "synced" flash on a Sync button (SYNC_ALL_KEY or a source string).
  const [flash, setFlash] = useState<string | null>(null);
  // Marketplace browse/install control.
  const [available, setAvailable] = useState<AvailablePlugin[] | null>(null);
  const [availableLoading, setAvailableLoading] = useState(false);
  const [installBusy, setInstallBusy] = useState<Record<string, boolean>>({});
  // Name of a just-installed skill to briefly highlight in the list. We do not
  // scroll to it: the floating install menu never reflows the list, so the
  // user stays exactly where they are; the status line and the flash confirm
  // the install without a jarring jump.
  const [justInstalled, setJustInstalled] = useState<string | null>(null);

  const flashDone = useCallback((key: string) => {
    setFlash(key);
    window.setTimeout(() => setFlash((f) => (f === key ? null : f)), 1600);
  }, []);

  // firstLoad guards the "Loading:" placeholder so a refresh never unmounts the
  // list (which would collapse height and jump the scroll to the top).
  const loadInstalled = useCallback(async (firstLoad = false) => {
    if (firstLoad) setLoading(true);
    setInstalled(await fetchInstalled());
    if (firstLoad) setLoading(false);
  }, []);

  const refreshUpdates = useCallback(async () => {
    const ups = await fetchUpdates();
    const map: Record<string, SkillUpdate> = {};
    for (const u of ups) map[u.name] = u;
    setUpdates(map);
    return map;
  }, []);

  useEffect(() => {
    void loadInstalled(true);
  }, [loadInstalled]);

  // After an install, briefly flash the new row so it is easy to spot, then
  // clear the flag. No scroll - the list position is left untouched.
  useEffect(() => {
    if (!justInstalled) return;
    const tid = window.setTimeout(() => setJustInstalled(null), 2400);
    return () => window.clearTimeout(tid);
  }, [justInstalled]);

  const onToggle = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    void (async () => {
      const action = skill.enabled ? "disable" : "enable";
      const res = await apiSend(
        `/coddy/skills/${encodeURIComponent(skill.name)}/${action}`,
        "POST",
      );
      if (!res.ok) {
        setError(res.error || translate("skills.error.toggle", { action }));
      } else {
        await loadInstalled();
      }
      setBusy((p) => ({ ...p, [skill.name]: false }));
    })();
  };

  const onRemove = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    void (async () => {
      const res = await apiSend(
        `/coddy/skills/${encodeURIComponent(skill.name)}`,
        "DELETE",
      );
      if (!res.ok) {
        setError(res.error || translate("skills.error.delete"));
      } else {
        await loadInstalled();
      }
      setBusy((p) => ({ ...p, [skill.name]: false }));
    })();
  };

  const onUpdateSkill = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend(
        `/coddy/skills/${encodeURIComponent(skill.name)}/update`,
        "POST",
      );
      if (!res.ok) {
        setError(res.error || translate("skills.error.update"));
      } else {
        setStatus(translate("skills.status.updated", { name: skill.name }));
        await loadInstalled();
        await refreshUpdates();
      }
      setBusy((p) => ({ ...p, [skill.name]: false }));
    })();
  };

  // Sync all configured sources, then refresh the list and re-check versions.
  // Success is shown on the button itself (checkmark), not as a status line.
  const onSync = () => {
    setSyncing(true);
    setError(null);
    void (async () => {
      const res = await apiSend("/coddy/skills/sync", "POST");
      if (!res.ok) setError(res.error || translate("skills.error.sync"));
      else {
        await loadInstalled();
        await refreshUpdates();
        flashDone(SYNC_ALL_KEY);
      }
      setSyncing(false);
    })();
  };

  // Sync a single marketplace by its source string (works on the current row
  // value even before the settings are saved).
  const onSyncOne = (source: string) => {
    const src = source.trim();
    if (!src) return;
    setSyncing(true);
    setError(null);
    void (async () => {
      const res = await apiSend(
        `/coddy/skills/sync?source=${encodeURIComponent(src)}`,
        "POST",
      );
      if (!res.ok) setError(res.error || translate("skills.error.sync"));
      else {
        await loadInstalled();
        await refreshUpdates();
        flashDone(src);
      }
      setSyncing(false);
    })();
  };

  // Lazily fetch the plugins advertised by configured marketplaces (network /
  // git) the first time the install control is used; force to refresh after an
  // install. Plain closure over `available` so the "already loaded" guard sees
  // the current value.
  const loadAvailable = async (force = false) => {
    if (available !== null && !force) return;
    setAvailableLoading(true);
    setAvailable(await fetchAvailable());
    setAvailableLoading(false);
  };

  const onInstallPlugin = (p: AvailablePlugin) => {
    setInstallBusy((b) => ({ ...b, [p.name]: true }));
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend("/coddy/skills/install", "POST", {
        source: p.source,
        plugin: p.name,
      });
      if (!res.ok)
        setError(
          res.error || translate("skills.error.install", { name: p.name }),
        );
      else {
        setStatus(translate("skills.status.installed", { name: p.name }));
        // Optimistically drop it from the dropdown right away, then refresh.
        setAvailable((av) =>
          av
            ? av.map((a) => (a.name === p.name ? { ...a, installed: true } : a))
            : av,
        );
        await loadInstalled();
        await refreshUpdates();
        await loadAvailable(true);
        // Flash the new row (no scroll); it is now installed and usable from
        // the composer's `/` menu straight away (the server drops its slash
        // cache on install).
        setJustInstalled(p.name);
      }
      setInstallBusy((b) => ({ ...b, [p.name]: false }));
    })();
  };

  const fieldOverride: FieldOverride = ({ path, value: fv, onChange: fc }) => {
    if (path === "sources") {
      return (
        <SkillsSourcesEditor
          value={(fv as string[]) ?? []}
          onChange={(next) => fc(next)}
          onSyncOne={onSyncOne}
          onSyncAll={onSync}
          syncing={syncing}
          flash={flash}
        />
      );
    }
    // Auto-discovery is rendered as its own fieldset at the top of the section
    // (see below); suppress the default inline boolean here.
    if (path === "auto_discovery") {
      return <></>;
    }
    return null;
  };

  const autoDiscoveryOn = value.auto_discovery !== false;
  const autoDiscoveryDesc =
    schemaFieldDesc(
      "skills",
      "auto_discovery",
      (
        schema.properties?.["auto_discovery"] as
          | { description?: string }
          | undefined
      )?.description,
    ) ?? translate("skills.autoDiscovery.fallbackDesc");

  return (
    <div className="settings-skills-section">
      <fieldset className="settings-fieldset">
        <legend>{t("skills.autoDiscovery.legend")}</legend>
        <SwitchField
          checked={autoDiscoveryOn}
          onChange={(next) => onChange({ ...value, auto_discovery: next })}
          label={
            autoDiscoveryOn
              ? t("skills.state.enabled")
              : t("skills.state.disabled")
          }
          description={autoDiscoveryDesc}
          ariaLabel={t("skills.autoDiscovery.aria")}
          dataTestId="skills-auto-discovery-toggle"
        />
      </fieldset>

      <SchemaForm
        schema={schema}
        value={value}
        onChange={onChange}
        fieldOverride={fieldOverride}
        i18nDomain="skills"
      />

      <fieldset className="settings-fieldset skills-installed-box">
        <legend>{t("skills.installed.legend")}</legend>

        <SkillsInstallSearch
          available={available}
          loading={availableLoading}
          installBusy={installBusy}
          onFocusLoad={() => void loadAvailable()}
          onInstall={onInstallPlugin}
        />

        <p className="settings-field-desc">{t("skills.install.cliHint")}</p>
        {error ? <p className="settings-error">{error}</p> : null}
        {status ? <p className="settings-muted">{status}</p> : null}

        <SkillsInstalledList
          installed={installed}
          updates={updates}
          busy={busy}
          loading={loading}
          justInstalled={justInstalled}
          onToggle={onToggle}
          onRemove={onRemove}
          onUpdate={onUpdateSkill}
        />
      </fieldset>
    </div>
  );
}
