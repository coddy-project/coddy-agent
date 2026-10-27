import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { IconTrash } from "./SchemaForm";
import { LegendWithHint } from "./FieldHint";
import { SETTINGS_TEXTS, landmarkAnchor } from "./settingsSections";
import { IconCheck, IconDownload, IconShield, IconSync } from "./icons";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";
import {
  entryKey,
  fetchMarketplaces,
  liveSessionId,
  sessionHeaders,
  showsEntryTrustControl,
  type MarketplaceEntry,
  type MarketplaceListing,
} from "./marketplaces";
import {
  snapshotSettingsConfig,
  subscribeSettingsConfig,
} from "./settingsConfigStore";

// Flash key of the "Sync all" action, distinct from any entry key.
const SYNC_ALL_KEY = " all";
// Busy key of the add form, distinct from any entry key.
const ADD_KEY = " add";

async function send(
  sessionId: string | undefined,
  path: string,
  method: "POST" | "DELETE",
  body?: unknown,
): Promise<{ ok: boolean; error?: string; body?: unknown }> {
  const init: RequestInit = {
    method,
    headers: sessionHeaders(sessionId, body !== undefined),
  };
  if (body !== undefined) init.body = JSON.stringify(body);
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return { ok: false };
  }
  if (res.ok) {
    try {
      return { ok: true, body: await res.json() };
    } catch {
      return { ok: true };
    }
  }
  try {
    const j = (await res.json()) as { error?: { message?: string } };
    return { ok: false, error: j.error?.message || `HTTP ${res.status}` };
  } catch {
    return { ok: false, error: `HTTP ${res.status}` };
  }
}

function kindBadgeKey(e: MarketplaceEntry): string {
  return e.kind === "marketplace"
    ? "skills.sources.kind.marketplace"
    : "skills.sources.kind.source";
}

function originBadgeKey(e: MarketplaceEntry): string {
  switch (e.origin) {
    case "system":
      return "skills.sources.origin.system";
    case "project":
      return "skills.sources.origin.project";
    default:
      return "skills.sources.origin.home";
  }
}

// What a check answered with: how many newer versions it found, and the
// marketplaces and sources it could not read.
type CheckFound = { count: number; unread: string[] };

function checkFound(body: unknown): CheckFound {
  const b = body as { updates?: unknown; failed?: unknown } | undefined;
  const unread = Array.isArray(b?.failed)
    ? (b.failed as Array<{ source?: unknown }>)
        .map((f) => (typeof f?.source === "string" ? f.source : ""))
        .filter((s) => s !== "")
    : [];
  return {
    count: Array.isArray(b?.updates) ? b.updates.length : 0,
    unread,
  };
}

/**
 * MarketplacesEditor is the marketplaces list of Settings -> Skills. It is
 * API-driven like the MCP tab (/coddy/skills/sources*): what it shows is
 * declared in ~/.coddy/marketplaces.json and the viewed workspace's
 * .coddy/marketplaces.json, never in the settings document, and every action
 * applies at once. A row is a source (installed whole by its install button,
 * or as it is added) or a marketplace (a catalog), with where it is declared;
 * a project row under ask carries the MCP shield that approves it for the
 * workspace, and the built-in catalog a shield that says it is always
 * trusted. The check of a row, or of every row, reads the lists and installs
 * nothing: the newer versions it finds are installed from the list of
 * installed skills.
 */
export function MarketplacesEditor(props: {
  activeSessionId?: string | undefined;
  /** Called after a check or an install, so the installed list can follow. */
  onSynced: () => Promise<void> | void;
  /**
   * How many installed skills have a newer version, as the installed list
   * knows it now: after a check, the line under the legend follows it, so it
   * stays true once those updates are installed.
   */
  updateCount?: number;
}) {
  const { t, tp } = useT();
  const sessionId = props.activeSessionId || undefined;
  const onSynced = props.onSynced;
  const [listing, setListing] = useState<MarketplaceListing | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [syncing, setSyncing] = useState(false);
  const [flash, setFlash] = useState<string | null>(null);
  // What the last check found, or null before one and after a change that
  // makes it stale (an add, an install, a failed check).
  const [found, setFound] = useState<CheckFound | null>(null);
  const updateCount = found ? (props.updateCount ?? found.count) : 0;
  const [draft, setDraft] = useState("");
  const [scope, setScope] = useState<"global" | "local">("global");
  // A project entry is written into the session's workspace; before the chat
  // has a session the server would take its own default folder, which is not
  // the one on screen, so only your file can be written then.
  const projectScopeAvailable = liveSessionId(sessionId) !== undefined;
  const effectiveScope = projectScopeAvailable ? scope : "global";
  // The saved settings: skills.project_trust decides which rows hold, so a
  // save reads the list again.
  const savedSettings = useSyncExternalStore(
    subscribeSettingsConfig,
    snapshotSettingsConfig,
  ).config;
  // A listing asked for a session left since must not paint over the
  // listing of the current one.
  const genRef = useRef(0);

  const load = useCallback(async () => {
    const gen = ++genRef.current;
    try {
      const next = await fetchMarketplaces(sessionId);
      if (gen !== genRef.current) return;
      setListing(next);
      setLoadError(null);
    } catch {
      if (gen !== genRef.current) return;
      setLoadError(translate("skills.sources.error.load"));
    }
  }, [sessionId]);

  useEffect(() => {
    void load();
  }, [load, savedSettings]);

  const flashDone = useCallback((key: string) => {
    setFlash(key);
    window.setTimeout(() => setFlash((f) => (f === key ? null : f)), 1600);
  }, []);

  const withBusy = (key: string, run: () => Promise<void>) => {
    setBusy((b) => ({ ...b, [key]: true }));
    setError(null);
    void (async () => {
      try {
        await run();
      } finally {
        setBusy((b) => ({ ...b, [key]: false }));
      }
    })();
  };

  // A check reads the lists and installs nothing; what it finds shows up as
  // the update buttons of the installed list.
  const onSync = (key?: string) => {
    setSyncing(true);
    setError(null);
    setFound(null);
    void (async () => {
      const path = key
        ? `/coddy/skills/sync?source=${encodeURIComponent(key)}`
        : "/coddy/skills/sync";
      try {
        const res = await send(sessionId, path, "POST");
        if (!res.ok) setError(res.error || translate("skills.error.sync"));
        else {
          await onSynced();
          setFound(checkFound(res.body));
          flashDone(key ?? SYNC_ALL_KEY);
        }
      } catch {
        // The refresh after the sync failed (the connection dropped): say
        // so, and never leave every Sync button disabled.
        setError(translate("skills.error.sync"));
      } finally {
        setSyncing(false);
      }
    })();
  };

  // Installs every plugin a source publishes: the explicit install the list
  // offers, since nothing installs a source on its own.
  const onInstall = (e: MarketplaceEntry) => {
    const key = entryKey(e);
    setFound(null);
    withBusy(key, async () => {
      const res = await send(sessionId, "/coddy/skills/install", "POST", {
        source: e.source,
      });
      if (!res.ok) {
        setError(
          res.error ||
            translate("skills.sources.error.install", { source: key }),
        );
        return;
      }
      await onSynced();
      flashDone(`${key} install`);
    });
  };

  const onRemove = (e: MarketplaceEntry) => {
    const key = entryKey(e);
    withBusy(key, async () => {
      // Only the file of the row: removing your entry leaves the project's
      // checked-in copy alone, and the reverse.
      const res = await send(
        sessionId,
        `/coddy/skills/sources?source=${encodeURIComponent(key)}&origin=${encodeURIComponent(e.origin)}`,
        "DELETE",
      );
      if (!res.ok)
        setError(
          res.error ||
            translate("skills.sources.error.remove", { source: key }),
        );
      await load();
    });
  };

  const onToggleTrust = (e: MarketplaceEntry) => {
    const key = entryKey(e);
    withBusy(key, async () => {
      const res = e.trusted
        ? await send(sessionId, "/coddy/skills/sources/untrust", "POST", {
            key,
          })
        : await send(sessionId, "/coddy/skills/sources/trust", "POST", {
            key,
            fingerprint: e.fingerprint,
          });
      if (!res.ok)
        setError(
          res.error || translate("skills.sources.error.trust", { source: key }),
        );
      await load();
    });
  };

  const onAdd = () => {
    const source = draft.trim();
    if (!source) return;
    setFound(null);
    withBusy(ADD_KEY, async () => {
      // A source is installed as it is added: nothing installs it later. One
      // in the list already, and the built-in catalog, install nothing.
      const res = await send(sessionId, "/coddy/skills/sources", "POST", {
        source,
        scope: effectiveScope,
        sync: true,
      });
      if (!res.ok) {
        setError(
          res.error || translate("skills.sources.error.add", { source }),
        );
        await load();
        return;
      }
      setDraft("");
      if ((res.body as { added?: unknown } | undefined)?.added === false) {
        setError(translate("skills.sources.addedAlready", { source }));
      }
      await load();
      await onSynced();
    });
  };

  const entries = listing?.entries ?? [];
  const policy = listing?.projectTrust ?? "ask";

  return (
    <fieldset
      className="settings-fieldset skills-marketplaces-box"
      data-testid="skills-marketplaces"
      data-settings-anchor={landmarkAnchor("skills", "marketplaces")}
    >
      <LegendWithHint
        label={t(SETTINGS_TEXTS.skillsMarketplaces.label)}
        description={t(SETTINGS_TEXTS.skillsMarketplaces.desc)}
      />
      {loadError ? (
        <p
          className="settings-error"
          data-testid="skills-marketplaces-load-error"
        >
          {loadError}
        </p>
      ) : null}
      {(listing?.errors ?? []).map((msg) => (
        <p key={msg} className="settings-error">
          {msg}
        </p>
      ))}
      {error ? <p className="settings-error">{error}</p> : null}
      {found !== null && found.unread.length > 0 ? (
        <p className="settings-error" data-testid="skills-check-unread">
          {t("skills.sources.check.unread", {
            sources: found.unread.join(", "),
          })}
        </p>
      ) : null}
      {found !== null && (updateCount > 0 || found.unread.length === 0) ? (
        <p className="settings-muted" data-testid="skills-check-status">
          {updateCount > 0
            ? tp("skills.sources.check.updates", updateCount)
            : t("skills.sources.check.current")}
        </p>
      ) : null}
      <ul
        className="skills-marketplaces"
        data-testid="skills-marketplaces-list"
      >
        {entries.map((e) => {
          const key = entryKey(e);
          const held = e.status !== "ready";
          return (
            <li
              key={`${e.origin}:${e.kind}:${key}`}
              className={`skills-marketplace${held ? " is-held" : ""}`}
              data-testid={`skills-marketplace-${key}`}
            >
              <div className="skills-marketplace-head">
                <div className="skills-marketplace-text">
                  <div className="skills-list-item-name">
                    <span className="skills-marketplace-key">{key}</span>
                    <span className="skills-list-item-badge">
                      {t(kindBadgeKey(e))}
                    </span>
                    <span
                      className="skills-list-item-badge"
                      title={e.source_path || undefined}
                      data-testid={`skills-marketplace-origin-${key}`}
                    >
                      {t(originBadgeKey(e))}
                    </span>
                  </div>
                  {e.kind === "marketplace" ? (
                    <div className="skills-list-item-desc skills-marketplace-source">
                      {e.source}
                    </div>
                  ) : null}
                </div>
                {e.origin === "system" ? (
                  <button
                    type="button"
                    className="settings-btn settings-btn-icon is-trusted"
                    disabled
                    title={t("skills.sources.systemTitle")}
                    aria-label={t("skills.sources.trust.systemAria", {
                      source: key,
                    })}
                    data-testid={`skills-marketplace-trust-${key}`}
                  >
                    <IconShield />
                  </button>
                ) : showsEntryTrustControl(e, policy) ? (
                  <button
                    type="button"
                    className={`settings-btn settings-btn-icon${e.trusted ? " is-trusted" : " settings-btn-approve"}`}
                    disabled={!!busy[key]}
                    onClick={() => onToggleTrust(e)}
                    title={
                      e.trusted
                        ? t("skills.sources.trust.approvedTitle")
                        : t("skills.sources.trust.approveTitle", {
                            source: e.source,
                          })
                    }
                    aria-label={t(
                      e.trusted
                        ? "skills.sources.trust.withdrawAria"
                        : "skills.sources.trust.approveAria",
                      { source: key },
                    )}
                    data-testid={`skills-marketplace-trust-${key}`}
                  >
                    <IconShield />
                  </button>
                ) : null}
                {e.kind === "source" ? (
                  <button
                    type="button"
                    className={`settings-btn settings-btn-icon${flash === `${key} install` ? " is-synced" : ""}`}
                    disabled={syncing || held || !!busy[key]}
                    onClick={() => onInstall(e)}
                    title={
                      held
                        ? t("skills.sources.heldSyncTitle")
                        : flash === `${key} install`
                          ? t("skills.sources.installedTitle")
                          : t("skills.sources.installTitle", { source: key })
                    }
                    aria-label={t("skills.sources.installAria", {
                      source: key,
                    })}
                    data-testid={`skills-marketplace-install-${key}`}
                  >
                    {flash === `${key} install` ? (
                      <IconCheck />
                    ) : (
                      <IconDownload />
                    )}
                  </button>
                ) : null}
                <button
                  type="button"
                  className={`settings-btn settings-btn-icon${flash === key ? " is-synced" : ""}`}
                  disabled={syncing || held}
                  onClick={() => onSync(key)}
                  title={
                    e.status === "denied"
                      ? t("skills.sources.deniedSyncTitle")
                      : held
                        ? t("skills.sources.heldSyncTitle")
                        : flash === key
                          ? t("skills.sources.syncedTitle")
                          : t("skills.sources.syncTitle", { source: key })
                  }
                  aria-label={t("skills.sources.syncAria")}
                  data-testid={`skills-marketplace-sync-${key}`}
                >
                  {flash === key ? <IconCheck /> : <IconSync />}
                </button>
                <button
                  type="button"
                  className="settings-btn settings-btn-icon settings-btn-danger"
                  disabled={e.origin === "system" || !!busy[key]}
                  onClick={() => onRemove(e)}
                  title={
                    e.origin === "system"
                      ? t("skills.sources.systemTitle")
                      : t("skills.sources.removeTitle", {
                          path: e.source_path || "",
                        })
                  }
                  aria-label={t("skills.sources.removeAria")}
                  data-testid={`skills-marketplace-remove-${key}`}
                >
                  <IconTrash />
                </button>
              </div>
              {e.status === "needs_approval" ? (
                <p
                  className="skills-marketplace-note"
                  data-testid={`skills-marketplace-note-${key}`}
                >
                  {t("skills.sources.note.held", { path: e.source_path || "" })}
                </p>
              ) : e.status === "denied" ? (
                <p
                  className="skills-marketplace-note"
                  data-testid={`skills-marketplace-note-${key}`}
                >
                  {t("skills.sources.note.denied")}
                </p>
              ) : null}
            </li>
          );
        })}
      </ul>
      <div className="skills-sources-footer">
        <div className="skills-marketplace-add">
          <input
            className="settings-input"
            type="text"
            value={draft}
            placeholder={t("skills.sources.placeholder")}
            onChange={(ev) => setDraft(ev.target.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter") {
                ev.preventDefault();
                onAdd();
              }
            }}
            aria-label={t("skills.sources.addAria")}
            data-testid="skills-marketplace-input"
          />
          <select
            className="settings-input skills-marketplace-scope"
            value={effectiveScope}
            disabled={!projectScopeAvailable}
            title={
              projectScopeAvailable
                ? undefined
                : t("skills.sources.scope.noSessionTitle")
            }
            onChange={(ev) =>
              setScope(ev.target.value === "local" ? "local" : "global")
            }
            aria-label={t("skills.sources.scopeAria")}
            data-testid="skills-marketplace-scope"
          >
            <option value="global">{t("skills.sources.scope.global")}</option>
            <option value="local">{t("skills.sources.scope.local")}</option>
          </select>
          <button
            type="button"
            className="settings-btn"
            disabled={!draft.trim() || !!busy[ADD_KEY]}
            onClick={onAdd}
            data-testid="skills-marketplace-add"
          >
            {t("skills.sources.add")}
          </button>
        </div>
        <button
          type="button"
          className={`settings-btn skills-sync-all-btn${flash === SYNC_ALL_KEY ? " is-synced" : ""}`}
          disabled={syncing}
          onClick={() => onSync()}
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
