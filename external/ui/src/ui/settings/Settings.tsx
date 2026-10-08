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
  snapshotSettingsConfig,
  subscribeSettingsConfig,
} from "./settingsConfigStore";
import {
  discardPendingSettings,
  editSettingsDraft,
  flushSettingsDraft,
  reloadSettingsDraft,
  saveSettingsDraft,
  snapshotSettingsDraft,
  subscribeSettingsDraft,
} from "./settingsDraftStore";
import { SettingsPendingPanel, pendingChangeLabel } from "./SettingsPending";
import { ConfirmDialog } from "../components/ConfirmDialog";
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
import { useRailEscapeStep } from "../nav/railEscape";

/** How long a save that went through lights the Save button green. */
export const SAVE_SUCCESS_MS = 2000;

/**
 * Grey rows the section rail and the tile grid show in place of the tabs the
 * schema will bring, while the first read of the page is on its way.
 */
const PLACEHOLDER_TABS = 12;

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
  /**
   * The page is on a swarm relay: its settings are the relay's deployment and
   * its log, and there is no Sessions tab (issue #401).
   */
  relay?: boolean;
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
  // The form's document and its saves live in settingsDraftStore for the life
  // of the page: the form saves on its own a moment after the last edit, and
  // what the schema marks as a deliberate act waits for Save. A newer copy of
  // the config - the first read landing, the server's config reloaded by
  // anyone - replaces a form that holds no edits of its own, in the store,
  // before any frame is drawn from it: a frame drawn from the older document
  // would show a list with no rows, and the list reads an address naming one
  // of its rows as a stale one and rewrites it.
  const draft = useSyncExternalStore(
    subscribeSettingsDraft,
    snapshotSettingsDraft,
    snapshotSettingsDraft,
  );
  const doc = draft.doc;
  const setDoc = editSettingsDraft;
  useEffect(() => {
    void ensureSettingsConfig();
  }, []);
  // A save waiting for the pause goes out when the drawer goes away, by any
  // path; changes waiting for Save stay in the store for the next open.
  useEffect(() => () => flushSettingsDraft(), []);
  // Why a Reload could not read the server; a refused save says why in the
  // store (draft.error).
  const [loadError, setLoadError] = useState<string | null>(null);
  const error = loadError ?? draft.error;
  const { locale, t, tp } = useT();
  const [activeTab, setActiveTab] = useState<string>(
    props.initialSection ?? "",
  );
  // Animation feedback: bump reloadKey to replay the form dissolve/reappear on
  // reload; reloading spins the refresh icon; justSaved turns the save button
  // green for SAVE_SUCCESS_MS.
  const [reloadKey, setReloadKey] = useState(0);
  const [reloading, setReloading] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const [askClose, setAskClose] = useState(false);
  const savedTimer = useRef<number | null>(null);
  useEffect(
    () => () => {
      if (savedTimer.current !== null) {
        window.clearTimeout(savedTimer.current);
      }
    },
    [],
  );
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

  // A Save that went through with nothing typed while it ran turns the button
  // green; every save that lands while the drawer is open, on its own or by
  // Save, tells the app, which reads the model metadata again. A save that
  // lands after the drawer closed reaches the app through config_reloaded,
  // which the server announces after every save.
  const confirmedAtMount = useRef(draft.confirmedSaves);
  useEffect(() => {
    if (draft.confirmedSaves !== confirmedAtMount.current) {
      confirmedAtMount.current = draft.confirmedSaves;
      flashSaved();
    }
  }, [draft.confirmedSaves, flashSaved]);
  const onConfigSavedRef = useRef(props.onConfigSaved);
  useEffect(() => {
    onConfigSavedRef.current = props.onConfigSaved;
  });
  const savesAtMount = useRef(draft.saves);
  useEffect(() => {
    if (draft.saves !== savesAtMount.current) {
      savesAtMount.current = draft.saves;
      onConfigSavedRef.current?.();
    }
  }, [draft.saves]);

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

  const relay = props.relay === true;
  const sections = useMemo(
    () => deriveSettingsSections(schema, { relay }),
    [schema, locale, relay],
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
  const closeRowForm = useCallback(() => setCloseRowSignal((n) => n + 1), []);

  // Closing asks first while the form holds something that would not be saved
  // without the operator: changes waiting for Save, or edits a save refused.
  // Otherwise it closes at once, and a save waiting for the pause goes out as
  // the drawer goes.
  const mustAsk =
    draft.pending.length > 0 || (draft.error !== null && draft.unsaved);
  const requestClose = useCallback(() => {
    if (mustAsk) {
      setAskClose(true);
      return;
    }
    props.onClose();
  }, [mustAsk, props]);
  const keepEditing = useCallback(() => setAskClose(false), []);

  // Escape takes the step the head's arrow takes while the head shows one,
  // and closes the drawer when it shows none (nav/railEscape.ts).
  useRailEscapeStep(
    "settings",
    rowTitle && headSection
      ? closeRowForm
      : isMobileShell && (mobileSection || mobilePending)
        ? backToGrid
        : mustAsk
          ? requestClose
          : null,
  );

  // Reload with visible feedback: spin the refresh icon and replay the form
  // dissolve/reappear animation (key bump remounts the content) while re-fetching.
  // It is the deliberate way back to what the server has, so the form takes the
  // new copy over the edits it held when Reload was pressed - not over any typed
  // while the read was on its way.
  const onReload = useCallback(async () => {
    setReloading(true);
    setReloadKey((k) => k + 1);
    setLoadError(null);
    try {
      const [read] = await Promise.all([
        reloadSettingsDraft(),
        new Promise((r) => window.setTimeout(r, 500)),
      ]);
      if (!read.ok) {
        setLoadError(
          translate("settings.error.failedToLoad", { error: read.error }),
        );
      }
    } finally {
      setReloading(false);
    }
  }, []);

  const onSave = useCallback(async (): Promise<boolean> => {
    setLoadError(null);
    // The green of an earlier save says nothing about this one.
    if (savedTimer.current !== null) {
      window.clearTimeout(savedTimer.current);
      savedTimer.current = null;
    }
    setJustSaved(false);
    return saveSettingsDraft();
  }, []);

  const saveAndClose = useCallback(async () => {
    setAskClose(false);
    if (await onSave()) {
      props.onClose();
    }
  }, [onSave, props]);

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

  // One line beside the buttons says where the form stands: saving, waiting
  // for Save, refused, a save waiting for the pause, or all saved.
  const saveState = draft.saving
    ? "saving"
    : draft.error !== null && draft.unsaved
      ? "error"
      : draft.pending.length > 0
        ? "pending"
        : draft.unsaved
          ? "unsaved"
          : draft.saves > 0
            ? "saved"
            : "idle";
  const saveStatusText = !schema
    ? ""
    : saveState === "saving"
      ? t("settings.status.saving")
      : saveState === "error"
        ? t("settings.status.error")
        : saveState === "pending"
          ? tp("settings.status.pending", draft.pending.length)
          : saveState === "unsaved"
            ? t("settings.status.unsaved")
            : saveState === "saved"
              ? t("settings.status.saved")
              : t("settings.status.auto");
  const closeMessage = [
    draft.pending.length > 0
      ? t("settings.close.pending", {
          changes: draft.pending
            .map((c) => pendingChangeLabel(c, sections, schema))
            .join("; "),
        })
      : "",
    draft.error !== null && draft.unsaved ? t("settings.close.refused") : "",
  ]
    .filter(Boolean)
    .join(" ");

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
              onClick={closeRowForm}
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
          onClick={requestClose}
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
          <SettingsPendingPanel
            pending={draft.pending}
            sections={sections}
            schema={schema}
            onDiscard={discardPendingSettings}
            disabled={draft.confirming}
          />
          <span
            className="settings-save-status"
            data-testid="settings-save-status"
            data-state={saveState}
            role="status"
          >
            {saveStatusText}
          </span>
          <button
            type="button"
            className="settings-btn settings-btn-icon"
            data-testid="settings-reload"
            disabled={reloading}
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
            className={`settings-btn settings-btn-primary settings-btn-icon${justSaved ? " is-saved" : ""}${draft.pending.length > 0 ? " has-pending" : ""}`}
            data-testid="settings-save"
            disabled={!schema || draft.confirming}
            title={
              draft.pending.length > 0
                ? t("settings.save.pendingTitle")
                : t("settings.save.title")
            }
            aria-label={
              draft.pending.length > 0
                ? tp("settings.save.pendingAria", draft.pending.length)
                : t("settings.save.aria")
            }
            onClick={() => void onSave()}
          >
            <IconSave className="settings-footer-icon-svg" />
            {draft.pending.length > 0 ? (
              <span className="settings-save-badge" aria-hidden>
                {draft.pending.length}
              </span>
            ) : null}
          </button>
        </div>
      </div>
      <ConfirmDialog
        open={askClose}
        title={t("settings.close.title")}
        message={closeMessage}
        confirmLabel={t("settings.close.save")}
        cancelLabel={t("settings.close.keep")}
        variant="primary"
        ariaLabel={t("settings.close.title")}
        onConfirm={() => void saveAndClose()}
        onCancel={keepEditing}
        dataTestId="settings-close-dialog"
      />
    </aside>
  );
}
