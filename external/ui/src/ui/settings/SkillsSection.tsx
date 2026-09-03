import { useCallback, useEffect, useState } from "react";
import {
  SchemaForm,
  IconTrash,
  type JsonSchema,
  type FieldOverride,
} from "./SchemaForm";
import { Switch } from "./Switch";
import { filterInstallableMatches } from "./installableMatches";
import { schemaFieldDesc } from "./schemaI18n";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

// Cap the install dropdown so a broad query never floods the menu; anything
// beyond this is summarized as a "+N more" hint that invites a narrower search.
const INSTALL_MENU_LIMIT = 10;

type InstalledSkill = {
  name: string;
  description: string;
  file_path: string;
  enabled: boolean;
  version?: string;
  source?: string;
  readonly?: boolean;
};

type SkillUpdate = {
  name: string;
  source: string;
  version: string;
  latest: string;
  update_available: boolean;
};

async function fetchInstalled(): Promise<InstalledSkill[]> {
  const res = await fetch("/coddy/skills");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: InstalledSkill[] };
  return data.items ?? [];
}

async function fetchUpdates(): Promise<SkillUpdate[]> {
  const res = await fetch("/coddy/skills/updates");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: SkillUpdate[] };
  return data.items ?? [];
}

type AvailablePlugin = {
  name: string;
  description: string;
  version?: string;
  source: string;
  installed: boolean;
};

async function fetchAvailable(): Promise<AvailablePlugin[]> {
  const res = await fetch("/coddy/skills/available");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: AvailablePlugin[] };
  return data.items ?? [];
}

async function apiSend(
  path: string,
  method: "POST" | "DELETE",
  body?: unknown,
): Promise<{ ok: boolean; error?: string }> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    try {
      const j = (await res.json()) as { error?: { message?: string } };
      return { ok: false, error: j.error?.message || `HTTP ${res.status}` };
    } catch {
      return { ok: false, error: `HTTP ${res.status}` };
    }
  }
  return { ok: true };
}

function IconPlug() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M7 22H4a2 2 0 0 1-2-2v-3a2 2 0 0 0-2 0V7a2 2 0 0 0 2 0H7" />
      <path d="M15 7h4a2 2 0 0 1 2 2v4a2 2 0 0 0 0 2v3a2 2 0 0 1-2 2h-3" />
      <line x1="12" y1="2" x2="12" y2="22" />
    </svg>
  );
}

function IconSync() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M21 2v6h-6" />
      <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
      <path d="M3 22v-6h6" />
      <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
    </svg>
  );
}

// Download-to-tray glyph for the "download update" action.
function IconDownload() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M12 3v12" />
      <polyline points="7 10 12 15 17 10" />
      <path d="M5 21h14" />
    </svg>
  );
}

// Checkmark shown briefly after a successful sync.
function IconCheck() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.1"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

// Flash key for the "Sync all" action (distinct from any source string).
const SYNC_ALL_KEY = " all";

/**
 * SourcesEditor renders the `skills.sources` array (config-backed via onChange)
 * with a per-marketplace Sync button and, in the footer, Add (left) plus
 * Sync all (right). It replaces the generic array control via SchemaForm's
 * fieldOverride hook.
 */
function SourcesEditor(props: {
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
  const [installQuery, setInstallQuery] = useState("");
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

  const installQ = installQuery.trim();
  const { matches: installMatches, more: installMore } =
    filterInstallableMatches(available ?? [], installQ, INSTALL_MENU_LIMIT);

  const fieldOverride: FieldOverride = ({ path, value: fv, onChange: fc }) => {
    if (path === "sources") {
      return (
        <SourcesEditor
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
        <div className="settings-row settings-row-inline">
          <Switch
            checked={autoDiscoveryOn}
            onChange={(next) => onChange({ ...value, auto_discovery: next })}
            ariaLabel={t("skills.autoDiscovery.aria")}
            dataTestId="skills-auto-discovery-toggle"
          />
          <span>
            {autoDiscoveryOn
              ? t("skills.state.enabled")
              : t("skills.state.disabled")}
          </span>
        </div>
        <p className="settings-field-desc">{autoDiscoveryDesc}</p>
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

        <div className="skills-install">
          <input
            className="settings-input skills-install-input"
            type="text"
            placeholder={t("skills.install.searchPlaceholder")}
            value={installQuery}
            onChange={(e) => setInstallQuery(e.target.value)}
            onFocus={() => void loadAvailable()}
            data-testid="skills-install-input"
          />
          {installQ ? (
            <ul
              className="skills-install-results"
              data-testid="skills-install-results"
            >
              {availableLoading && available === null ? (
                <li className="skills-install-empty settings-muted">
                  {t("skills.install.loadingMarketplaces")}
                </li>
              ) : installMatches.length === 0 ? (
                <li className="skills-install-empty settings-muted">
                  {t("skills.install.noMatches")}
                </li>
              ) : (
                <>
                  {installMatches.map((p) => (
                    <li
                      key={`${p.source}/${p.name}`}
                      className="skills-install-result"
                    >
                      <div className="skills-install-result-text">
                        <div className="skills-list-item-name">
                          {p.name}
                          {p.version ? (
                            <span className="skills-list-item-version">
                              v{p.version}
                            </span>
                          ) : null}
                        </div>
                        <div className="skills-list-item-desc">
                          {p.description || p.source}
                        </div>
                      </div>
                      <button
                        type="button"
                        className="settings-btn settings-btn-icon settings-btn-primary"
                        disabled={!!installBusy[p.name]}
                        onClick={() => onInstallPlugin(p)}
                        title={t("skills.install.installTitle", {
                          name: p.name,
                        })}
                        aria-label={t("skills.install.installAria", {
                          name: p.name,
                        })}
                        data-testid={`skills-install-${p.name}`}
                      >
                        <IconDownload />
                      </button>
                    </li>
                  ))}
                  {installMore > 0 ? (
                    <li
                      className="skills-install-empty settings-muted"
                      data-testid="skills-install-more"
                    >
                      {t("skills.install.moreHint", { count: installMore })}
                    </li>
                  ) : null}
                </>
              )}
            </ul>
          ) : null}
        </div>

        <p className="settings-field-desc">{t("skills.install.cliHint")}</p>
        {error ? <p className="settings-error">{error}</p> : null}
        {status ? <p className="settings-muted">{status}</p> : null}

        {installed.length === 0 ? (
          loading ? (
            <p className="settings-muted">{t("skills.loading")}</p>
          ) : (
            <p className="settings-muted">{t("skills.empty")}</p>
          )
        ) : (
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
                      <div className="skills-list-item-desc">
                        {sk.description}
                      </div>
                    ) : null}
                  </div>
                  {hasUpdate ? (
                    <button
                      type="button"
                      className="settings-btn settings-btn-icon settings-btn-primary skills-update-btn"
                      disabled={!!busy[sk.name]}
                      onClick={() => onUpdateSkill(sk)}
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
        )}
      </fieldset>
    </div>
  );
}
