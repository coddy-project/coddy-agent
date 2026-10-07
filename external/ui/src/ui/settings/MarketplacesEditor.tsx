import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { IconTrash } from "./SchemaForm";
import { LegendWithHint } from "./FieldHint";
import { IconCheck, IconShield, IconSync } from "./icons";
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
): Promise<{ ok: boolean; error?: string }> {
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
  if (res.ok) return { ok: true };
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

/**
 * MarketplacesEditor is the marketplaces list of Settings -> Skills. It is
 * API-driven like the MCP tab (/coddy/skills/sources*): what it shows is
 * declared in ~/.coddy/marketplaces.json and the viewed workspace's
 * .coddy/marketplaces.json, never in the settings document, and every action
 * applies at once. A row is a source (every plugin installed) or a
 * marketplace (a catalog), with where it is declared; a project row under
 * ask carries the MCP shield that approves it for the workspace, and the
 * built-in source a shield that says it is always trusted.
 */
export function MarketplacesEditor(props: {
  activeSessionId?: string | undefined;
  /** Called after a sync, so the installed list can follow. */
  onSynced: () => Promise<void> | void;
}) {
  const { t } = useT();
  const sessionId = props.activeSessionId || undefined;
  const onSynced = props.onSynced;
  const [listing, setListing] = useState<MarketplaceListing | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [syncing, setSyncing] = useState(false);
  const [flash, setFlash] = useState<string | null>(null);
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

  const onSync = (key?: string) => {
    setSyncing(true);
    setError(null);
    void (async () => {
      const path = key
        ? `/coddy/skills/sync?source=${encodeURIComponent(key)}`
        : "/coddy/skills/sync";
      try {
        const res = await send(sessionId, path, "POST");
        if (!res.ok) setError(res.error || translate("skills.error.sync"));
        else {
          await onSynced();
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
    withBusy(ADD_KEY, async () => {
      const res = await send(sessionId, "/coddy/skills/sources", "POST", {
        source,
        scope: effectiveScope,
      });
      if (!res.ok) {
        setError(
          res.error || translate("skills.sources.error.add", { source }),
        );
        return;
      }
      setDraft("");
      await load();
    });
  };

  const entries = listing?.entries ?? [];
  const policy = listing?.projectTrust ?? "ask";

  return (
    <fieldset
      className="settings-fieldset skills-marketplaces-box"
      data-testid="skills-marketplaces"
    >
      <LegendWithHint
        label={t("skills.sources.legend")}
        description={t("skills.sources.description")}
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
