import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useRightDockEscape } from "../components/useRightDock";
import {
  phoneMaxWidthMediaQuery,
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  IconExpand,
  IconMore,
  IconRestore,
  IconTree,
} from "../files/windowIcons";
import { DiffFileSection } from "./DiffFileSection";
import { EditsTree } from "./EditsTree";
import {
  readDiffViewCookie,
  writeDiffViewCookie,
} from "./diffViewPrefs";
import type { DiffView } from "./diffViewPrefs";
import { fetchSessionChangeFile } from "./api";
import { treeOrder } from "./fileTree";
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

/** A phone shows the tree or the diffs, one at a time (styles.css, Files window). */
function onPhone(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia(phoneMaxWidthMediaQuery).matches
  );
}

/** The last segment of the workspace folder, the way the window names it. */
function folderName(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  return trimmed.split(/[\\/]/).pop() || trimmed;
}

/** How far under the top of the diffs a file still counts as the one on show. */
const ON_SHOW_SLACK_PX = 24;

/**
 * The edits window: every diff of the folder's uncommitted changes in one
 * scrollable document, the one view of the edits. Framed and headed the way
 * the Files window is (files/FilesView.tsx): the same sheet beside the rail,
 * the tree switch and the title on the left, the menu, the expand button and
 * the close button on the right, the tree of the changed files - and only
 * those - beside the diffs. The menu holds the rest: side by side, folding
 * every diff, discarding everything.
 */
export function EditsView(props: {
  sessionId: string;
  /** The chat's workspace folder, named under the title. */
  workspacePath?: string;
  onClose: () => void;
}) {
  const { t, tp } = useT();
  const { sessionId, onClose } = props;
  const workspacePath = props.workspacePath || "";
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // Read as the window opens rather than at module load, so a choice made in
  // another tab is picked up.
  const [view, setView] = useState<DiffView>(readDiffViewCookie);
  // A phone opens on the diffs; the tree switch brings the list over them.
  const [treeOpen, setTreeOpen] = useState(() => !onPhone());
  const [expandedWindow, setExpandedWindow] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [active, setActive] = useState("");
  // A file picked in the tree, scrolled to once the diffs are on screen (on a
  // phone the tree gives way to them first).
  const [jump, setJump] = useState<{ path: string; seq: number } | null>(null);

  const sectionRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const moreRef = useRef<HTMLButtonElement | null>(null);
  // The file picked last: marked while it is in sight, even when the diffs
  // cannot scroll it to their top (the last file of a short set).
  const pickedRef = useRef("");
  const wc = useWorkingCopy(sessionId, { enabled: true });
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

  // Escape closes the window when nothing nearer took it: the confirmation
  // dialog over it answers its own, the tree's filter clears itself, the menu
  // closes itself first.
  useRightDockEscape(true, onClose);
  useEscapeCloses(menuOpen, () => {
    setMenuOpen(false);
    moreRef.current?.focus({ preventScroll: true });
  });

  // The diffs take the focus as the window opens, so the keys scroll them;
  // closing gives the focus back to whatever opened it.
  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    scrollRef.current?.focus({ preventScroll: true });
    return () => {
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);

  const patches = usePatches(sessionId, changes.files);

  // The diffs read in the order the tree lists their files, so the mark that
  // follows the scroll walks the tree from top to bottom.
  const files = useMemo(() => {
    const byPath = new Map(changes.files.map((f) => [f.path, f]));
    return treeOrder(changes.files.map((f) => f.path)).flatMap((path) => {
      const file = byPath.get(path);
      return file ? [file] : [];
    });
  }, [changes.files]);
  // Until the reader picks or scrolls, the file at the top is the one on show.
  const onShow = files.some((f) => f.path === active) ? active : files[0]?.path || "";

  const registerRef = useCallback((path: string, el: HTMLDivElement | null) => {
    if (el) {
      sectionRefs.current.set(path, el);
    } else {
      sectionRefs.current.delete(path);
    }
  }, []);

  const pick = (path: string) => {
    pickedRef.current = path;
    setActive(path);
    if (onPhone()) setTreeOpen(false);
    setJump((prev) => ({ path, seq: (prev?.seq ?? 0) + 1 }));
  };

  useLayoutEffect(() => {
    if (!jump) return;
    sectionRefs.current.get(jump.path)?.scrollIntoView({ block: "start" });
  }, [jump]);

  // The tree marks the file at the top of the diffs as the reader scrolls.
  const followScroll = () => {
    const scroller = scrollRef.current;
    if (!scroller) return;
    const port = scroller.getBoundingClientRect();
    const picked = pickedRef.current ? sectionRefs.current.get(pickedRef.current) : undefined;
    if (picked) {
      const r = picked.getBoundingClientRect();
      if (r.top < port.bottom && r.bottom > port.top) return;
      pickedRef.current = "";
    }
    const top = port.top + ON_SHOW_SLACK_PX;
    let onShowNow = "";
    for (const file of files) {
      const el = sectionRefs.current.get(file.path);
      if (!el) continue;
      if (el.getBoundingClientRect().top <= top) onShowNow = file.path;
      else break;
    }
    if (onShowNow && onShowNow !== active) setActive(onShowNow);
  };

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

  const skipped = changes.skipped ?? 0;
  const unversioned = wc.loaded && changes.vcs !== "git";
  // Discard all reaches the new files git reports but the server did not
  // read, too.
  const discardable = changes.files.length > 0 || skipped > 0;
  const workspaceName = workspacePath ? folderName(workspacePath) : "";
  const fromMenu = (action: () => void) => () => {
    setMenuOpen(false);
    action();
  };

  return (
    <div
      className={
        "files-dock-cluster edits-window" + (expandedWindow ? " is-expanded" : "")
      }
      data-testid="edits-view"
      role="dialog"
      aria-label={t("changes.viewer.title")}
    >
      <div className="files-header">
        <div className="files-title-block">
          <button
            type="button"
            className={"files-icon-btn" + (treeOpen ? " is-active" : "")}
            data-testid="edits-toggle-tree"
            aria-pressed={treeOpen}
            aria-label={t(treeOpen ? "changes.viewer.hideFiles" : "changes.viewer.showFiles")}
            title={t(treeOpen ? "changes.viewer.hideFiles" : "changes.viewer.showFiles")}
            onClick={() => setTreeOpen((v) => !v)}
          >
            <IconTree />
          </button>
          <div className="files-title-text">
            <h2 className="files-title">{t("changes.viewer.title")}</h2>
            <p
              className="files-subtitle"
              title={workspacePath ? t("files.workspace", { path: workspacePath }) : undefined}
            >
              {workspaceName ? (
                <span className="edits-folder">{workspaceName}</span>
              ) : null}
              <span className="edits-totals" data-testid="edits-totals">
                <span className="changes-add">{"+" + changes.totals.additions}</span>
                <span className="changes-del">{"−" + changes.totals.deletions}</span>
              </span>
            </p>
          </div>
        </div>
        <div className="files-actions">
          <div className="files-more-host">
            <button
              type="button"
              ref={moreRef}
              className={"files-icon-btn" + (menuOpen ? " is-active" : "")}
              data-testid="edits-more"
              aria-label={t("files.more")}
              title={t("files.more")}
              aria-haspopup="menu"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen((v) => !v)}
            >
              <IconMore />
            </button>
            {menuOpen ? (
              <>
                <button
                  type="button"
                  className="mode-menu-backdrop"
                  aria-hidden="true"
                  tabIndex={-1}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    setMenuOpen(false);
                  }}
                />
                <div
                  className="mode-menu files-more-menu"
                  role="menu"
                  aria-label={t("files.more")}
                >
                  {narrow ? null : (
                    <button
                      type="button"
                      role="menuitemcheckbox"
                      aria-checked={view === "split"}
                      className="mode-item files-menu-item"
                      data-testid="edits-split"
                      onClick={fromMenu(toggleView)}
                    >
                      <span className="files-menu-check" aria-hidden>
                        {view === "split" ? "✓" : ""}
                      </span>
                      {t("changes.viewer.split")}
                    </button>
                  )}
                  <button
                    type="button"
                    role="menuitem"
                    className="mode-item files-menu-item"
                    data-testid="edits-toggle-all"
                    disabled={changes.files.length === 0}
                    onClick={fromMenu(toggleAll)}
                  >
                    <span className="files-menu-check" aria-hidden />
                    {allCollapsed
                      ? t("changes.viewer.expandAll")
                      : t("changes.viewer.collapseAll")}
                  </button>
                  {discardable ? (
                    <>
                      <div className="files-menu-sep" role="separator" />
                      <button
                        type="button"
                        role="menuitem"
                        className="mode-item files-menu-item files-menu-item--danger"
                        title={t("changes.discardAllTitle")}
                        disabled={discard.busy}
                        data-testid="edits-discard-all"
                        onClick={fromMenu(() => void discard.discardAll())}
                      >
                        <span className="files-menu-check" aria-hidden />
                        {t("changes.discardAll")}
                      </button>
                    </>
                  ) : null}
                </div>
              </>
            ) : null}
          </div>
          <button
            type="button"
            className={"files-icon-btn" + (expandedWindow ? " is-active" : "")}
            data-testid="edits-expand"
            aria-pressed={expandedWindow}
            aria-label={t(expandedWindow ? "files.restore" : "files.expand")}
            title={t(expandedWindow ? "files.restore" : "files.expand")}
            onClick={() => setExpandedWindow((v) => !v)}
          >
            {expandedWindow ? <IconRestore /> : <IconExpand />}
          </button>
          <button
            type="button"
            className="sessions-close files-close"
            data-testid="edits-close"
            aria-label={t("changes.viewer.close")}
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </div>

      <div className={"files-layout" + (treeOpen ? " has-tree" : "")}>
        {treeOpen ? (
          <EditsTree files={changes.files} active={onShow} onPick={pick} />
        ) : null}

        <section className="files-main" aria-label={t("changes.viewer.title")}>
          <div
            ref={scrollRef}
            className="dv-scroll"
            data-testid="dv-scroll"
            tabIndex={-1}
            onScroll={followScroll}
          >
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
              files.map((file) => {
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
        </section>
      </div>
    </div>
  );
}
