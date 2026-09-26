import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { type JsonSchema } from "./SchemaForm";
import {
  deriveSettingsSections,
  knownSectionLabel,
  type SectionDescriptor,
} from "./settingsSections";
import { SettingsNav } from "./SettingsNav";
import { SettingsSection } from "./SettingsSection";
import { SettingsSkeleton } from "./SettingsSkeleton";
import { SettingsTileGrid } from "./SettingsTileGrid";
import {
  ensureSettingsConfig,
  noteSettingsConfigSaved,
  refreshSettingsConfig,
  snapshotSettingsConfig,
  subscribeSettingsConfig,
} from "./settingsConfigStore";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  parseAppHash,
  setSettingsHash,
  setSettingsSectionHash,
} from "../scheduler/hashRoute";
import { useT } from "../i18n/I18nProvider";
import { hasTranslation, translate } from "../i18n/i18n";

type ValidateResponse = { ok: boolean; error?: string };

/** How long a save that went through lights the Save button green. */
export const SAVE_SUCCESS_MS = 2000;

/**
 * Grey rows the section rail and the tile grid show in place of the tabs the
 * schema will bring, while the first read of the page is on its way.
 */
const PLACEHOLDER_TABS = 12;

/**
 * The form's document: the operator's edits (doc) over the config it started
 * from (base, null before the first read lands). While doc is base itself
 * nothing was edited, and a newer copy of the config may take its place. seen
 * is the last copy the draft was measured against, so a copy is taken or passed
 * over once. replaced counts the times a copy took the place of the document,
 * which an open row form re-reads its row by (SettingsArraySection).
 */
type Draft = {
  seen: Record<string, unknown> | null;
  base: Record<string, unknown> | null;
  doc: Record<string, unknown>;
  replaced: number;
};

function IconSave(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
      <polyline points="17 21 17 13 7 13 7 21" />
      <polyline points="7 3 7 8 15 8" />
    </svg>
  );
}

function IconRefresh(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="23 4 23 10 17 10" />
      <polyline points="1 20 1 14 7 14" />
      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
    </svg>
  );
}

/** Back arrow (lucide arrow-left) for the mobile section-detail header. */
function IconArrowLeft(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19 12H5" />
      <path d="M12 19l-7-7 7-7" />
    </svg>
  );
}

/** The drawer's title while a row of a list section is open: the form it
 * shows ("Provider settings"), the section's own name for a list without one. */
function itemFormTitle(section: SectionDescriptor): string {
  const key = `settings.item.${section.id}`;
  return hasTranslation(key) ? translate(key) : section.label;
}

/** Whether the address keeps the history sidebar open (`?history=1`). */
function historyOpenInAddress(): boolean {
  const route = parseAppHash();
  return "historyOpen" in route && route.historyOpen;
}

export function Settings(props: {
  onClose: () => void;
  /** Called after the config is successfully saved so the app can re-fetch model metadata. */
  onConfigSaved?: () => void;
  /** Section id from the `#/settings/<section>` deep link (null = default/grid). */
  initialSection?: string | null;
  /** The row a list section has open, from `?id=` of that deep link. */
  initialItem?: string | null;
  /** The conversation on screen; the session table keeps it out of "delete all". */
  activeSessionId?: string;
  /** Session ids the table removed, so the shell can drop them from History. */
  onSessionsDeleted?: (ids: string[]) => void;
  /**
   * Workspace of the viewed session. The Subagents tab lists the definitions of
   * that workspace, because spawn_agent resolves them against the session's own
   * cwd.
   */
  workspacePath?: string | undefined;
  onSessionTagsChanged?: (id: string, tags: string[]) => void;
}) {
  // The schema and the config the app keeps for every open of the drawer
  // (settingsConfigStore.ts): the first open reads them, later ones draw from
  // the copy at once, and a reload of the server's config refreshes it.
  const copy = useSyncExternalStore(
    subscribeSettingsConfig,
    snapshotSettingsConfig,
    snapshotSettingsConfig,
  );
  const schema = copy.schema;
  // An open after a failed first read reads again (ensureSettingsConfig below):
  // until that read starts, the error of the attempt before is not the state
  // of this one, and the requested tab shows as loading, not as Appearance.
  const [mountCopy] = useState(copy);
  const retrying = copy === mountCopy && copy.error !== null;
  const loading = schema === null && (copy.error === null || retrying);
  const [draft, setDraft] = useState<Draft>(() => ({
    seen: copy.config,
    base: copy.config,
    doc: copy.config ?? {},
    replaced: 0,
  }));
  // A newer copy - the first read landing, the server's config reloaded by
  // anyone - replaces a form that holds no edits of its own; unsaved edits
  // stay, and Reload is the deliberate way to drop them. It is taken while
  // rendering, not in an effect after it: a frame drawn from the older document
  // would show a list with no rows, and the list reads an address naming one of
  // its rows as a stale one and rewrites it.
  if (copy.config !== null && copy.config !== draft.seen) {
    const untouched = draft.base === null || draft.doc === draft.base;
    setDraft(
      untouched
        ? {
            seen: copy.config,
            base: copy.config,
            doc: copy.config,
            replaced: draft.replaced + 1,
          }
        : { ...draft, seen: copy.config },
    );
  }
  const doc = draft.doc;
  // Counts the operator's edits (a newer copy taking the place of an untouched
  // form is none), so a save knows whether the form changed while it ran.
  const edits = useRef(0);
  const setDoc = useCallback((next: Record<string, unknown>) => {
    edits.current++;
    setDraft((d) => ({ ...d, doc: next }));
  }, []);
  useEffect(() => {
    void ensureSettingsConfig();
  }, []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { locale, t } = useT();
  const [activeTab, setActiveTab] = useState<string>(
    props.initialSection ?? "",
  );
  // Animation feedback: bump reloadKey to replay the form dissolve/reappear on
  // reload; reloading spins the refresh icon; justSaved turns the save button
  // green for SAVE_SUCCESS_MS.
  const [reloadKey, setReloadKey] = useState(0);
  const [reloading, setReloading] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const savedTimer = useRef<number | null>(null);
  useEffect(
    () => () => {
      if (savedTimer.current !== null) {
        window.clearTimeout(savedTimer.current);
      }
    },
    [],
  );
  const clearSaved = useCallback(() => {
    if (savedTimer.current !== null) {
      window.clearTimeout(savedTimer.current);
      savedTimer.current = null;
    }
    setJustSaved(false);
  }, []);
  const flashSaved = useCallback(() => {
    setJustSaved(true);
    if (savedTimer.current !== null) {
      window.clearTimeout(savedTimer.current);
    }
    savedTimer.current = window.setTimeout(() => {
      savedTimer.current = null;
      setJustSaved(false);
    }, SAVE_SUCCESS_MS);
  }, []);

  // On narrow shells the section picker is a tile grid (master) that opens one
  // section at a time (detail); `mobileDetailId` null means the grid is showing.
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [mobileDetailId, setMobileDetailId] = useState<string | null>(
    props.initialSection ?? null,
  );

  const sections = useMemo(
    () => deriveSettingsSections(schema),
    [schema, locale],
  );
  // A tab the address names that is not among the tabs yet: the schema that
  // describes it is on its way. It shows as a skeleton rather than as another
  // tab first (Appearance, which needs no schema, used to stand in for it).
  const pendingTab = (id: string | null): string | null =>
    loading && id && !sections.some((s) => s.id === id) ? id : null;
  const activeSection =
    sections.find((s) => s.id === activeTab) ??
    (pendingTab(activeTab) ? null : (sections[0] ?? null));
  const mobileSection = mobileDetailId
    ? (sections.find((s) => s.id === mobileDetailId) ?? null)
    : null;
  const mobilePending = pendingTab(mobileDetailId);

  // Reflect the `#/settings/<section>` deep link (initial load and browser
  // back/forward) into local tab state; writing the hash below re-enters here
  // with the same value, so this is a no-op on self-initiated changes.
  const routeSection = props.initialSection ?? null;
  useEffect(() => {
    setActiveTab(routeSection ?? "");
    setMobileDetailId(routeSection);
  }, [routeSection]);

  // Selecting a section (desktop tab or mobile tile) anchors it in the URL.
  const selectSection = useCallback((id: string) => {
    setActiveTab(id);
    setMobileDetailId(id);
    setSettingsSectionHash(id);
  }, []);

  // Mobile back to the tile grid drops the section anchor.
  const backToGrid = useCallback(() => {
    setMobileDetailId(null);
    setSettingsHash();
  }, []);

  // The head is the way back, on every width. With a row of a list section
  // open (a provider, a model) it is titled after that form and its arrow
  // closes the form back onto the list; on the narrow shell, with no row open,
  // it names the section and its arrow goes back to the tiles.
  const [rowOpen, setRowOpen] = useState(false);
  const [closeRowSignal, setCloseRowSignal] = useState(0);
  const headSection = isMobileShell ? mobileSection : activeSection;
  const rowTitle = rowOpen && headSection ? itemFormTitle(headSection) : null;

  // Reload with visible feedback: spin the refresh icon and replay the form
  // dissolve/reappear animation (key bump remounts the content) while re-fetching.
  // It is the deliberate way back to what the server has, so the form takes the
  // new copy over the edits it held when Reload was pressed - not over any typed
  // while the read was on its way.
  const onReload = useCallback(async () => {
    const pressedOn = doc;
    setReloading(true);
    setReloadKey((k) => k + 1);
    setError(null);
    try {
      const [read] = await Promise.all([
        refreshSettingsConfig(),
        new Promise((r) => window.setTimeout(r, 500)),
      ]);
      if (read.ok && read.copy.config) {
        const fresh = read.copy.config;
        setDraft((d) =>
          d.doc === pressedOn
            ? { seen: fresh, base: fresh, doc: fresh, replaced: d.replaced + 1 }
            : { ...d, seen: fresh },
        );
      } else if (!read.ok) {
        setError(translate("settings.error.failedToLoad", { error: read.error }));
      }
    } finally {
      setReloading(false);
    }
  }, [doc]);

  const onSave = useCallback(async () => {
    setBusy(true);
    setError(null);
    // The green of an earlier save says nothing about this one.
    clearSaved();
    const sent = doc;
    const editsAtSend = edits.current;
    try {
      const body = JSON.stringify(sent);
      const v = await fetch("/coddy/config/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      });
      const vj = (await v.json()) as ValidateResponse;
      if (!vj.ok) {
        setError(vj.error || translate("settings.error.validationFailed"));
        return;
      }
      const p = await fetch("/coddy/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body,
      });
      const pj = (await p.json()) as ValidateResponse;
      if (!p.ok || !pj.ok) {
        setError(
          pj.error ||
            translate("settings.error.saveFailed", { status: p.status }),
        );
        return;
      }
      // Green says the form on screen is saved. Edits typed while the request
      // was on its way are not in the file, so they leave the button as it is
      // and the form as holding unsaved edits.
      if (edits.current === editsAtSend) {
        flashSaved();
      }
      // What was sent is what the file has now, and the kept copy says so at
      // once (a reopen before the read below lands draws it). The copy read
      // here, and the one config_reloaded brings, replaces it with what the
      // server made of the save, unless the operator types something first.
      // A form a newer copy took over meanwhile is left as it is.
      setDraft((d) => (d.doc === sent ? { ...d, base: sent } : d));
      noteSettingsConfigSaved(sent);
      props.onConfigSaved?.();
      setBusy(false);
      void refreshSettingsConfig();
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : translate("settings.error.requestFailed"),
      );
    } finally {
      setBusy(false);
    }
  }, [doc, clearSaved, flashSaved, props]);

  // Renders the content panel for a section, reusing the schema-present and
  // appearance-without-schema paths for both the desktop rail and the mobile
  // tile-grid detail view.
  const renderSectionBody = (section: SectionDescriptor) => {
    if (schema) {
      return (
        <div className="settings-scroll">
          <div
            className={`settings-body${reloadKey > 0 ? " settings-form-anim" : ""}`}
            key={reloadKey}
          >
            <SettingsSection
              section={section}
              schema={schema}
              doc={doc}
              setDoc={setDoc}
              // The address names a row of one section. For the render in
              // which the tab has not caught up with a new address yet,
              // another section must not take that name for its own (it
              // would find no such row and rewrite the address to its list).
              routeItem={
                section.id === (props.initialSection ?? "")
                  ? (props.initialItem ?? null)
                  : null
              }
              // Only the section the address names writes it back: in the
              // render where the tab lags a new address, the old section
              // would otherwise report its closed form and undo the jump.
              onRouteItemChange={
                section.id === (props.initialSection ?? "")
                  ? (item) =>
                      setSettingsSectionHash(section.id, {
                        item,
                        historySidebar: historyOpenInAddress(),
                      })
                  : undefined
              }
              hideBackLink
              onEditingChange={setRowOpen}
              closeSignal={closeRowSignal}
              replacedSignal={draft.replaced}
              {...(props.activeSessionId
                ? { activeSessionId: props.activeSessionId }
                : {})}
              {...(props.onSessionsDeleted
                ? { onSessionsDeleted: props.onSessionsDeleted }
                : {})}
              workspacePath={props.workspacePath}
              {...(props.onSessionTagsChanged
                ? { onSessionTagsChanged: props.onSessionTagsChanged }
                : {})}
            />
          </div>
        </div>
      );
    }
    // Appearance and Sessions are client-side content (the theme picker, the
    // session table), available before the config schema loads, and they render
    // in the normal scroll flow like any other tab.
    if (section.kind === "appearance" || section.kind === "sessions") {
      return (
        <div className="settings-scroll">
          <div className="settings-body">
            <SettingsSection
              section={section}
              schema={{ type: "object", properties: {} } as JsonSchema}
              doc={doc}
              setDoc={setDoc}
              {...(props.activeSessionId
                ? { activeSessionId: props.activeSessionId }
                : {})}
              {...(props.onSessionsDeleted
                ? { onSessionsDeleted: props.onSessionsDeleted }
                : {})}
              {...(props.onSessionTagsChanged
                ? { onSessionTagsChanged: props.onSessionTagsChanged }
                : {})}
            />
          </div>
        </div>
      );
    }
    return loading ? <SettingsSkeleton /> : null;
  };

  return (
    <aside
      className="sessions settings drawer"
      aria-label={t("settings.aria.panel")}
      data-testid="settings-screen"
      data-variant="drawer"
    >
      <div className="sessions-head">
        {rowTitle && headSection ? (
          <span className="settings-head-titlegroup">
            <button
              type="button"
              className="settings-head-back"
              aria-label={t("settings.array.backTo", {
                list: headSection.label,
              })}
              title={t("settings.array.backTo", { list: headSection.label })}
              data-testid="settings-head-back"
              onClick={() => setCloseRowSignal((n) => n + 1)}
            >
              <IconArrowLeft />
            </button>
            <span className="settings-head-section">{rowTitle}</span>
          </span>
        ) : isMobileShell && (mobileSection || mobilePending) ? (
          <span className="settings-head-titlegroup">
            <button
              type="button"
              className="settings-head-back"
              aria-label={t("settings.backToSections")}
              title={t("settings.backToSections")}
              data-testid="settings-head-back"
              onClick={backToGrid}
            >
              <IconArrowLeft />
            </button>
            <span className="settings-head-section">
              {mobileSection
                ? mobileSection.label
                : (knownSectionLabel(mobilePending ?? "") ?? "")}
            </span>
          </span>
        ) : (
          <span>{t("settings.title")}</span>
        )}
        <button
          type="button"
          className="sessions-close"
          aria-label={t("settings.aria.close")}
          data-testid="settings-drawer-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      {/* The status band says what went wrong, in words; a save that went
          through says so on the Save button alone. */}
      {copy.error || error ? (
        <div className="settings-lead-pane">
          {copy.error ? (
            <p className="settings-error">
              {t("settings.error.failedToLoad", { error: copy.error })}
            </p>
          ) : null}
          {error ? <p className="settings-error">{error}</p> : null}
        </div>
      ) : null}

      <div className="settings-stack">
        {isMobileShell ? (
          mobileSection ? (
            <div className="settings-mobile-detail">
              {renderSectionBody(mobileSection)}
            </div>
          ) : mobilePending ? (
            <div className="settings-mobile-detail">
              <SettingsSkeleton />
            </div>
          ) : (
            <SettingsTileGrid
              sections={sections}
              onSelect={selectSection}
              placeholders={loading ? PLACEHOLDER_TABS : 0}
            />
          )
        ) : (
          <div className="settings-tabs-layout">
            <SettingsNav
              sections={sections}
              active={activeSection ? activeSection.id : ""}
              onSelect={selectSection}
              placeholders={loading ? PLACEHOLDER_TABS : 0}
            />
            {activeSection ? (
              renderSectionBody(activeSection)
            ) : loading ? (
              <SettingsSkeleton />
            ) : null}
          </div>
        )}

        <div className="scheduler-drawer-footer settings-footer-actions">
          <button
            type="button"
            className="settings-btn settings-btn-icon"
            data-testid="settings-reload"
            disabled={busy || reloading}
            title={t("settings.reload.title")}
            aria-label={t("settings.reload.aria")}
            onClick={() => void onReload()}
          >
            <IconRefresh
              className={`settings-footer-icon-svg${reloading ? " settings-icon-spin" : ""}`}
            />
          </button>
          <button
            type="button"
            className={`settings-btn settings-btn-primary settings-btn-icon${justSaved ? " is-saved" : ""}`}
            data-testid="settings-save"
            disabled={busy || !schema}
            title={t("settings.save.title")}
            aria-label={t("settings.save.aria")}
            onClick={() => void onSave()}
          >
            <IconSave className="settings-footer-icon-svg" />
          </button>
          {/* The green button is the whole message on screen; a screen reader
              hears it here. */}
          <span className="sr-only" role="status">
            {justSaved ? t("settings.save.saved") : ""}
          </span>
        </div>
      </div>
    </aside>
  );
}
