import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import { ChangesFileTree } from "./ChangesFileTree";
import { DiffFileSection } from "./DiffFileSection";
import {
  DEFAULT_DIFF_VIEW,
  readDiffViewCookie,
  writeDiffViewCookie,
} from "./diffViewPrefs";
import type { DiffView } from "./diffViewPrefs";
import { fetchSessionChangeFile } from "./api";
import { baseName, dirName } from "./sessionChangesText";
import type { ChangedFile } from "./types";
import { useDiscard } from "./useDiscard";
import { useWorkingCopy } from "./workingCopy";

/** How many patches are in flight at once. Enough to fill a screen quickly
 *  without opening a request per file in a large change set. */
const PATCH_CONCURRENCY = 4;

interface PatchState {
  patch?: string;
  error?: string;
  truncated?: boolean;
}

/**
 * Loads each file patch in the background, a few at a time.
 *
 * The list call deliberately carries no patches, so the window can render its
 * file rows and counts immediately; the bodies fill in behind that. Fetching
 * per file also means one enormous file cannot hold up the rest. Every new
 * report of git reads every patch again - the same counts do not mean the same
 * lines - while the patch already on screen stays until its successor lands,
 * so a re-read does not blank the window. A failed read is never kept.
 */
function usePatches(
  sessionId: string,
  files: ChangedFile[],
): Map<string, PatchState> {
  const [patches, setPatches] = useState<Map<string, PatchState>>(new Map());
  const patchesRef = useRef(patches);
  patchesRef.current = patches;

  useEffect(() => {
    const listed = new Set(files.map((f) => f.path));
    const kept = new Map<string, PatchState>();
    for (const [path, state] of patchesRef.current) {
      if (listed.has(path) && !state.error) {
        kept.set(path, state);
      }
    }
    setPatches(kept);
    if (!sessionId.trim()) {
      return;
    }
    const queue = files.filter((f) => !f.binary).map((f) => f.path);
    let cancelled = false;
    let next = 0;

    const worker = async () => {
      for (;;) {
        const index = next;
        next += 1;
        if (cancelled || index >= queue.length) {
          return;
        }
        const path = queue[index]!;
        const res = await fetchSessionChangeFile(sessionId, path);
        if (cancelled) {
          return;
        }
        setPatches((prev) => {
          const copy = new Map(prev);
          copy.set(
            path,
            res.ok
              ? { patch: res.data.patch, truncated: res.data.truncated }
              : { error: res.message },
          );
          return copy;
        });
      }
    };

    for (let i = 0; i < Math.min(PATCH_CONCURRENCY, queue.length); i++) {
      void worker();
    }
    return () => {
      cancelled = true;
    };
  }, [sessionId, files]);

  return patches;
}

/**
 * The edits window: every diff of the folder's uncommitted changes in one
 * scrollable document, the one view of the edits. A modal rather than a
 * drawer, because two diff columns need the whole width; it carries the whole
 * set at once, a way to move between files, a choice of how a diff is drawn,
 * and discarding a file or all of them.
 */
export function DiffViewerModal(props: {
  open: boolean;
  sessionId: string;
  onClose: () => void;
}) {
  const { t, tp } = useT();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [view, setView] = useState<DiffView>(DEFAULT_DIFF_VIEW);
  const [treeOpen, setTreeOpen] = useState(false);
  const [gotoOpen, setGotoOpen] = useState(false);
  const [gotoFilter, setGotoFilter] = useState("");

  const sectionRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const { open, sessionId, onClose } = props;
  const wc = useWorkingCopy(sessionId, { enabled: open });
  const changes = wc.changes;
  const discard = useDiscard(sessionId);

  // Two columns of code need width the stacked shell does not have, so the
  // narrow layout reads every diff inline and hides the choice rather than
  // offering one that makes the diff unreadable.
  const narrow = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const effectiveView: DiffView = narrow ? "unified" : view;

  // Read on open rather than at module load so a change made in another tab is
  // picked up.
  useEffect(() => {
    if (open) {
      setView(readDiffViewCookie());
    }
  }, [open]);

  // Escape closes the window and claims the key, so nothing under it closes
  // on the same press; the confirmation dialog over it answers its own first
  // (it listens on window, ahead of this).
  useEscapeCloses(open, onClose);

  const patches = usePatches(sessionId, changes.files);

  const registerRef = useCallback((path: string, el: HTMLDivElement | null) => {
    if (el) {
      sectionRefs.current.set(path, el);
    } else {
      sectionRefs.current.delete(path);
    }
  }, []);

  const scrollTo = useCallback((path: string) => {
    const el = sectionRefs.current.get(path);
    if (el) {
      el.scrollIntoView({ block: "start" });
    }
    setGotoOpen(false);
  }, []);

  const allCollapsed =
    changes.files.length > 0 && collapsed.size === changes.files.length;

  const toggleAll = () => {
    setCollapsed(
      allCollapsed ? new Set() : new Set(changes.files.map((f) => f.path)),
    );
  };

  const toggleOne = (path: string) => {
    setCollapsed((prev) => {
      const copy = new Set(prev);
      if (copy.has(path)) {
        copy.delete(path);
      } else {
        copy.add(path);
      }
      return copy;
    });
  };

  const toggleView = () => {
    const next: DiffView = view === "split" ? "unified" : "split";
    setView(next);
    writeDiffViewCookie(next);
  };

  const gotoMatches = useMemo(() => {
    const needle = gotoFilter.trim().toLowerCase();
    return changes.files.filter(
      (f) => needle === "" || f.path.toLowerCase().includes(needle),
    );
  }, [changes.files, gotoFilter]);

  if (!open) {
    return null;
  }

  const skipped = changes.skipped ?? 0;
  const unversioned = wc.loaded && changes.vcs !== "git";
  // Discard all reaches the new files git reports but the server did not
  // read, too.
  const discardable = changes.files.length > 0 || skipped > 0;

  return createPortal(
    <div
      className="dv-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) {
          onClose();
        }
      }}
    >
      <div
        className="dv-window"
        role="dialog"
        aria-modal="true"
        aria-label={t("changes.viewer.title")}
        data-testid="diff-viewer"
      >
        <div className="dv-toolbar">
          <span className="dv-title">{t("changes.viewer.title")}</span>

          <span className="dv-totals" data-testid="dv-totals">
            <span className="changes-add">
              {"+" + changes.totals.additions}
            </span>
            <span className="changes-del">
              {"−" + changes.totals.deletions}
            </span>
          </span>

          <span className="dv-toolbar-spacer" />

          {discardable ? (
            <button
              type="button"
              className="changes-action changes-action--danger"
              title={t("changes.discardAllTitle")}
              disabled={discard.busy}
              data-testid="dv-discard-all"
              onClick={() => void discard.discardAll()}
            >
              {t("changes.discardAll")}
            </button>
          ) : null}

          <div className="dv-tools">
            <button
              type="button"
              className="dv-icon-btn"
              title={
                allCollapsed
                  ? t("changes.viewer.expandAll")
                  : t("changes.viewer.collapseAll")
              }
              aria-label={
                allCollapsed
                  ? t("changes.viewer.expandAll")
                  : t("changes.viewer.collapseAll")
              }
              data-testid="dv-toggle-all"
              onClick={toggleAll}
            >
              {allCollapsed ? "⇲" : "⇱"}
            </button>

            <div className="dv-goto-host">
              <button
                type="button"
                className={"dv-icon-btn" + (gotoOpen ? " is-active" : "")}
                title={t("changes.viewer.goToFile")}
                aria-label={t("changes.viewer.goToFile")}
                aria-expanded={gotoOpen}
                data-testid="dv-goto"
                onClick={() => setGotoOpen((v) => !v)}
              >
                {"⌕"}
              </button>
              {gotoOpen ? (
                <div className="dv-goto-menu" data-testid="dv-goto-menu">
                  <input
                    type="text"
                    className="dv-goto-input"
                    autoFocus
                    value={gotoFilter}
                    placeholder={t("changes.viewer.goToFile")}
                    aria-label={t("changes.viewer.goToFile")}
                    data-testid="dv-goto-input"
                    onChange={(e) => setGotoFilter(e.target.value)}
                  />
                  <div className="dv-goto-rows">
                    {gotoMatches.length === 0 ? (
                      <div className="dv-note">
                        {t("changes.viewer.noMatches")}
                      </div>
                    ) : (
                      gotoMatches.map((f) => (
                        <button
                          key={f.path}
                          type="button"
                          className="dv-goto-row"
                          title={f.path}
                          data-testid={`dv-goto-row-${f.path}`}
                          onClick={() => scrollTo(f.path)}
                        >
                          <span className="dv-goto-name">
                            {baseName(f.path)}
                          </span>
                          <span className="dv-goto-dir">{dirName(f.path)}</span>
                        </button>
                      ))
                    )}
                  </div>
                </div>
              ) : null}
            </div>

            {narrow ? null : (
              <button
                type="button"
                className="dv-icon-btn"
                title={
                  view === "split"
                    ? t("changes.viewer.viewUnified")
                    : t("changes.viewer.viewSplit")
                }
                aria-label={
                  view === "split"
                    ? t("changes.viewer.viewUnified")
                    : t("changes.viewer.viewSplit")
                }
                data-testid="dv-toggle-view"
                onClick={toggleView}
              >
                {"▥"}
              </button>
            )}

            <button
              type="button"
              className={"dv-icon-btn" + (treeOpen ? " is-active" : "")}
              title={
                treeOpen
                  ? t("changes.viewer.hideFiles")
                  : t("changes.viewer.showFiles")
              }
              aria-label={
                treeOpen
                  ? t("changes.viewer.hideFiles")
                  : t("changes.viewer.showFiles")
              }
              aria-expanded={treeOpen}
              data-testid="dv-toggle-tree"
              onClick={() => setTreeOpen((v) => !v)}
            >
              {"▤"}
            </button>
          </div>

          <button
            type="button"
            className="sessions-close"
            aria-label={t("changes.viewer.close")}
            data-testid="dv-close"
            onClick={onClose}
          >
            {"×"}
          </button>
        </div>

        <div className={"dv-body" + (treeOpen ? " dv-body--tree" : "")}>
          {treeOpen ? (
            <ChangesFileTree files={changes.files} onPick={scrollTo} />
          ) : null}

          <div className="dv-scroll" data-testid="dv-scroll">
            {skipped > 0 ? (
              <div className="dv-banner" data-testid="dv-skipped">
                <span>{tp("changes.skipped", skipped)}</span>
              </div>
            ) : null}
            {discard.error ? (
              <div className="dv-note dv-note--error" role="alert" data-testid="dv-discard-error">
                {discard.error}
              </div>
            ) : null}

            {!wc.loaded && wc.error ? (
              <div className="dv-note dv-note--error" role="alert" data-testid="dv-error">
                {wc.error}
              </div>
            ) : unversioned ? (
              <div className="dv-note" data-testid="dv-no-vcs">
                {t("changes.viewer.noVcs")}
              </div>
            ) : changes.files.length === 0 && wc.loaded ? (
              <div className="dv-note" data-testid="dv-empty">
                {t("changes.empty")}
              </div>
            ) : (
              changes.files.map((file) => {
                const state = patches.get(file.path);
                return (
                  <DiffFileSection
                    key={file.path}
                    file={{
                      ...file,
                      truncated: state?.truncated ?? file.truncated,
                    }}
                    patch={state?.patch ?? ""}
                    loading={!state}
                    error={state?.error ?? ""}
                    collapsed={collapsed.has(file.path)}
                    view={effectiveView}
                    onToggle={() => toggleOne(file.path)}
                    registerRef={registerRef}
                    discardBusy={discard.busy}
                    onDiscard={() => void discard.discardFile(file.path, file.status)}
                  />
                );
              })
            )}
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
