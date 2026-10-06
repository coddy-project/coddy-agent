import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useRightDockEscape } from "../components/useRightDock";
import { composerAutoFocusAllowed } from "../chat/composerFocus";
import { onEnvironmentSwitch } from "../env/remoteEnv";
import { phoneMaxWidthMediaQuery } from "../shellBreakpoint";
import { FilePreview, folderOf, nameOf } from "./FilePreview";
import { mediaUrl, readTree, rereadTree, searchFiles } from "./api";
import type { FileEntry, FileHit, TreePage } from "./api";
import {
  IconExpand,
  IconFile,
  IconFolder,
  IconFolderSmall,
  IconMore,
  IconRestore,
  IconSearch,
  IconTree,
} from "./windowIcons";

/** The files a window had open, per chat and workspace, for the life of the page. */
interface OpenFiles {
  tabs: string[];
  active: string;
  /** The line each open file was on. */
  lines: Record<string, number>;
}
const openFilesMemory = new Map<string, OpenFiles>();

/** Forgets every chat's open files: another environment, or a fresh test. */
export function forgetOpenFiles(): void {
  openFilesMemory.clear();
}

// The paths of one server mean nothing on another.
onEnvironmentSwitch(forgetOpenFiles);

// A chat's workspace is chosen once, before its first message, so the chat
// alone names its files; the window remounts when it learns the folder.
function memoryKey(sessionId: string): string {
  return sessionId;
}

/** The last segment of the workspace folder, the way the window names it. */
/** A phone shows the tree or a file, one at a time (styles.css, Files window). */
function onPhone(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia(phoneMaxWidthMediaQuery).matches
  );
}

function folderName(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  return trimmed.split(/[\\/]/).pop() || trimmed;
}

/**
 * The Files window: the session's workspace in a window over the chat, opened
 * the way the documentation is (the views menu of the chat header, a file path
 * in the conversation, Ctrl+Shift+F, the composer's Files chip, the address
 * `#/s/<id>/files?path=&line=`).
 *
 * The tree on the left loads one folder at a time; its filter searches the
 * whole workspace by name. A file picked there, or a path clicked in the
 * conversation, opens in a tab on the right, and the tabs outlive the window
 * for the life of the page, so reopening it finds the files where they were.
 */
export function FilesView(props: {
  sessionId: string;
  /** The session's workspace folder: named under the title, and the scope of the search. */
  workspacePath?: string;
  /** The file to show, and its line: an address or a link asked for it. */
  initialPath?: string;
  initialLine?: number;
  /** Bumps with every request to show a file, so a path asked for again opens again. */
  openSeq?: number;
  /** Bumps when a tool call of the turn finishes, so open files are checked again. */
  toolActivity?: number;
  onClose: () => void;
  /** Reports the file and line on show, for the address. */
  onNavigate?: (path: string, line: number) => void;
}) {
  const { t } = useT();
  const { sessionId } = props;
  const workspacePath = props.workspacePath || "";
  const key = memoryKey(sessionId);

  const [tabs, setTabs] = useState<string[]>(() => {
    const kept = openFilesMemory.get(key)?.tabs ?? [];
    const asked = props.initialPath || "";
    return asked && !kept.includes(asked) ? [...kept, asked] : kept;
  });
  const [active, setActive] = useState<string>(
    () => props.initialPath || openFilesMemory.get(key)?.active || "",
  );
  // The line on show per open file: an address names one, a tab keeps its own.
  const [lines, setLines] = useState<Record<string, number>>(() => ({
    ...(openFilesMemory.get(key)?.lines ?? {}),
    ...(props.initialPath
      ? { [props.initialPath]: props.initialLine || 1 }
      : {}),
  }));
  // On a phone a file asked for by its address shows at once, not under the tree.
  const [treeOpen, setTreeOpen] = useState(
    () => !(onPhone() && (props.initialPath || openFilesMemory.get(key)?.active)),
  );
  const [expandedWindow, setExpandedWindow] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [wrap, setWrap] = useState(true);
  const [epoch, setEpoch] = useState(0);
  const [menuOpen, setMenuOpen] = useState(false);
  const [notice, setNotice] = useState("");

  const filterRef = useRef<HTMLInputElement | null>(null);
  const moreRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    const kept: Record<string, number> = {};
    for (const path of tabs) if (lines[path]) kept[path] = lines[path];
    openFilesMemory.set(key, { tabs, active, lines: kept });
  }, [key, tabs, active, lines]);

  // Opened without a file, the window shows the one it was on; the address
  // learns it, so a reload comes back to it.
  useEffect(() => {
    if (!props.initialPath && active) props.onNavigate?.(active, lines[active] || 1);
    // Once, as the window opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // A path clicked in the conversation while the window is open opens it here.
  const askedPath = props.initialPath || "";
  const askedLine = props.initialLine || 1;
  useEffect(() => {
    if (!askedPath) return;
    setTabs((prev) => (prev.includes(askedPath) ? prev : [...prev, askedPath]));
    setActive(askedPath);
    if (onPhone()) setTreeOpen(false);
    setLines((prev) => ({ ...prev, [askedPath]: askedLine }));
  }, [askedPath, askedLine, props.openSeq]);

  // The filter takes the focus as the window opens, unless that would open an
  // on-screen keyboard; closing gives the focus back to whatever opened it.
  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (composerAutoFocusAllowed()) {
      filterRef.current?.focus({ preventScroll: true });
    }
    return () => {
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);

  // Escape closes the window when nothing nearer took it: the filter clears
  // itself first, the menu closes itself first.
  useRightDockEscape(true, props.onClose);
  useEscapeCloses(menuOpen, () => {
    setMenuOpen(false);
    moreRef.current?.focus({ preventScroll: true });
  });

  const lineOf = (path: string) => lines[path] || 1;

  const open = (path: string, line = 1) => {
    setTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
    setActive(path);
    if (onPhone()) setTreeOpen(false);
    setLines((prev) => ({ ...prev, [path]: line }));
    props.onNavigate?.(path, line);
  };

  const show = (path: string) => {
    setActive(path);
    props.onNavigate?.(path, lineOf(path));
  };

  const closeTab = (path: string) => {
    const at = tabs.indexOf(path);
    const rest = tabs.filter((p) => p !== path);
    setTabs(rest);
    if (path === active) {
      const next = rest[Math.min(at, rest.length - 1)] || "";
      setActive(next);
      props.onNavigate?.(next, next ? lineOf(next) : 1);
    }
  };

  const download = async () => {
    if (!active) return;
    try {
      const url = await mediaUrl(sessionId, active, true);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.rel = "noopener noreferrer";
      anchor.referrerPolicy = "no-referrer";
      anchor.click();
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err));
    }
  };

  const copyPath = async () => {
    if (!active) return;
    try {
      await navigator.clipboard?.writeText(active);
    } catch {
      // A browser that refuses the clipboard has nothing to report here.
    }
  };

  // Tabs whose names collide say which folder they come from.
  const repeatedNames = useMemo(() => {
    const seen = new Map<string, number>();
    for (const path of tabs) {
      const name = nameOf(path);
      seen.set(name, (seen.get(name) || 0) + 1);
    }
    return seen;
  }, [tabs]);

  const workspaceName = workspacePath ? folderName(workspacePath) : "";

  return (
    <div
      className={
        "files-dock-cluster" + (expandedWindow ? " is-expanded" : "")
      }
      data-testid="files-view"
      role="dialog"
      aria-label={t("files.title")}
    >
      <div className="files-header">
        <div className="files-title-block">
          <button
            type="button"
            className={"files-icon-btn" + (treeOpen ? " is-active" : "")}
            data-testid="files-toggle-tree"
            aria-pressed={treeOpen}
            aria-label={t(treeOpen ? "files.hideTree" : "files.showTree")}
            title={t(treeOpen ? "files.hideTree" : "files.showTree")}
            onClick={() => setTreeOpen((v) => !v)}
          >
            <IconTree />
          </button>
          <div className="files-title-text">
            <h2 className="files-title">{t("files.title")}</h2>
            {workspaceName ? (
              <p
                className="files-subtitle"
                title={t("files.workspace", { path: workspacePath })}
              >
                {workspaceName}
              </p>
            ) : null}
          </div>
        </div>
        <div className="files-actions">
          <div className="files-more-host">
            <button
              type="button"
              ref={moreRef}
              className={"files-icon-btn" + (menuOpen ? " is-active" : "")}
              data-testid="files-more"
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
                  <button
                    type="button"
                    role="menuitemcheckbox"
                    aria-checked={hidden}
                    className="mode-item files-menu-item"
                    onClick={() => {
                      setHidden((v) => !v);
                      setMenuOpen(false);
                    }}
                  >
                    <span className="files-menu-check" aria-hidden>
                      {hidden ? "✓" : ""}
                    </span>
                    {t("files.hidden")}
                  </button>
                  <button
                    type="button"
                    role="menuitemcheckbox"
                    aria-checked={wrap}
                    className="mode-item files-menu-item"
                    onClick={() => {
                      setWrap((v) => !v);
                      setMenuOpen(false);
                    }}
                  >
                    <span className="files-menu-check" aria-hidden>
                      {wrap ? "✓" : ""}
                    </span>
                    {t("files.wrap")}
                  </button>
                  {active ? (
                    <>
                      <div className="files-menu-sep" role="separator" />
                      <button
                        type="button"
                        role="menuitem"
                        className="mode-item files-menu-item"
                        onClick={() => {
                          setMenuOpen(false);
                          setEpoch((n) => n + 1);
                        }}
                      >
                        <span className="files-menu-check" aria-hidden />
                        {t("files.reload")}
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        className="mode-item files-menu-item"
                        onClick={() => {
                          setMenuOpen(false);
                          void copyPath();
                        }}
                      >
                        <span className="files-menu-check" aria-hidden />
                        {t("files.copyPath")}
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        className="mode-item files-menu-item"
                        data-testid="files-download"
                        onClick={() => {
                          setMenuOpen(false);
                          void download();
                        }}
                      >
                        <span className="files-menu-check" aria-hidden />
                        {t("files.download")}
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
            data-testid="files-expand"
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
            data-testid="files-close"
            aria-label={t("files.close")}
            onClick={props.onClose}
          >
            ×
          </button>
        </div>
      </div>
      <div
        className={
          "files-layout" +
          (treeOpen ? " has-tree" : "") +
          (active ? " has-file" : "")
        }
      >
        {treeOpen ? (
          <FilesSidebar
            sessionId={sessionId}
            workspacePath={workspacePath}
            hidden={hidden}
            active={active}
            epoch={epoch}
            activity={props.toolActivity || 0}
            filterRef={filterRef}
            onOpen={(path) => open(path)}
          />
        ) : null}
        <section className="files-main" aria-label={t("files.openFiles")}>
          {notice ? (
            <p role="alert" className="files-note">
              {notice}
            </p>
          ) : null}
          {tabs.length > 0 ? (
            <div
              className="files-tabs"
              role="tablist"
              aria-label={t("files.openFiles")}
            >
              {tabs.map((path) => {
                const name = nameOf(path);
                const repeated = (repeatedNames.get(name) || 0) > 1;
                return (
                  <div
                    key={path}
                    className={"files-tab" + (path === active ? " is-active" : "")}
                    title={path}
                  >
                    <button
                      type="button"
                      role="tab"
                      aria-selected={path === active}
                      className="files-tab-name"
                      onClick={() => show(path)}
                      onAuxClick={(e) => {
                        if (e.button === 1) closeTab(path);
                      }}
                    >
                      {name}
                      {repeated ? (
                        <span className="files-tab-dir">
                          {" "}
                          {folderOf(path).replace(/\/$/, "")}
                        </span>
                      ) : null}
                    </button>
                    <button
                      type="button"
                      className="files-tab-close"
                      aria-label={t("files.closeTab", { name })}
                      onClick={() => closeTab(path)}
                    >
                      ×
                    </button>
                  </div>
                );
              })}
            </div>
          ) : null}
          {active ? (
            <FilePreview
              key={active}
              sessionId={sessionId}
              path={active}
              line={lineOf(active)}
              wrap={wrap}
              epoch={epoch}
              activity={props.toolActivity || 0}
              onLine={(line) => {
                setLines((prev) => ({ ...prev, [active]: line }));
                props.onNavigate?.(active, line);
              }}
            />
          ) : (
            <div className="files-empty" data-testid="files-empty">
              <IconFolder />
              <p className="files-empty-title">{t("files.empty.title")}</p>
              <p className="files-empty-hint">{t("files.empty.hint")}</p>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

/**
 * The left column of the window: the filter over the tree. The tree reads one
 * folder at a time; the filter asks the workspace index, so it finds a file in
 * a folder nobody opened.
 */
function FilesSidebar(props: {
  sessionId: string;
  workspacePath: string;
  hidden: boolean;
  active: string;
  epoch: number;
  activity: number;
  filterRef: React.RefObject<HTMLInputElement | null>;
  onOpen: (path: string) => void;
}) {
  const { t } = useT();
  const { sessionId, hidden } = props;
  const [directories, setDirectories] = useState<Map<string, TreePage>>(
    new Map(),
  );
  const [expanded, setExpanded] = useState(new Set([""]));
  const [treeError, setTreeError] = useState("");
  const [filter, setFilter] = useState("");
  const [hits, setHits] = useState<FileHit[] | null>(null);
  const [searching, setSearching] = useState(false);

  // The latest tree, for a refresh that should not restart on every expansion.
  const expandedRef = useRef(expanded);
  expandedRef.current = expanded;
  const directoriesRef = useRef(directories);
  directoriesRef.current = directories;

  // Coming back to the page reads the open folders again: a tool or another
  // program may have added or removed files meanwhile.
  const [focusTick, setFocusTick] = useState(0);
  useEffect(() => {
    const refresh = () => setFocusTick((n) => n + 1);
    window.addEventListener("focus", refresh);
    return () => window.removeEventListener("focus", refresh);
  }, []);

  // Every open folder, read again as far as it was read (the pages "Load
  // more" added stay). A folder that is gone or refused is folded away rather
  // than failing the rest of the tree.
  useEffect(() => {
    const abort = new AbortController();
    const dirs = [...expandedRef.current];
    void Promise.allSettled(
      dirs.map((dir) =>
        rereadTree(
          sessionId,
          dir,
          directoriesRef.current.get(dir)?.entries.length || 0,
          hidden,
          abort.signal,
        ),
      ),
    ).then((results) => {
      if (abort.signal.aborted) return;
      const gone = new Set<string>();
      results.forEach((result, i) => {
        if (result.status === "rejected") gone.add(dirs[i]!);
      });
      setTreeError(
        gone.has("") ? String((results[dirs.indexOf("")] as PromiseRejectedResult).reason) : "",
      );
      // Merged, not replaced: a folder opened or paged while this read was on
      // its way keeps what it loaded.
      setDirectories((prev) => {
        const next = new Map(prev);
        results.forEach((result, i) => {
          if (result.status === "fulfilled") next.set(dirs[i]!, result.value);
          else next.delete(dirs[i]!);
        });
        return next;
      });
      if (gone.size > 0) {
        setExpanded((prev) => {
          const kept = new Set([...prev].filter((d) => d === "" || !gone.has(d)));
          return kept.size === prev.size ? prev : kept;
        });
      }
    });
    return () => abort.abort();
  }, [sessionId, hidden, props.epoch, props.activity, focusTick]);

  // A folder opened for the first time is read on its own; the others stay.
  const load = (dirs: string[]) => {
    for (const dir of dirs) {
      if (directoriesRef.current.has(dir)) continue;
      void readTree(sessionId, dir, "", hidden).then(
        (page) => setDirectories((prev) => new Map(prev).set(dir, page)),
        () =>
          setExpanded((prev) => {
            const next = new Set(prev);
            next.delete(dir);
            return next;
          }),
      );
    }
  };

  const query = filter.trim();
  useEffect(() => {
    // The hits of the query before are never shown for this one.
    setHits(null);
    if (!query) {
      setSearching(false);
      return;
    }
    const abort = new AbortController();
    setSearching(true);
    const timer = window.setTimeout(() => {
      void searchFiles(sessionId, props.workspacePath, query, abort.signal)
        .then((found) => {
          if (!abort.signal.aborted) setHits(found);
        })
        .catch(() => {
          if (!abort.signal.aborted) setHits([]);
        })
        .finally(() => {
          if (!abort.signal.aborted) setSearching(false);
        });
    }, 120);
    return () => {
      window.clearTimeout(timer);
      abort.abort();
    };
  }, [sessionId, props.workspacePath, query]);

  const toggle = (dir: string) => {
    const opening = !expanded.has(dir);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(dir)) next.delete(dir);
      else next.add(dir);
      return next;
    });
    if (opening) load([dir]);
  };

  // A folder found by the filter opens in the tree with every folder above it.
  const reveal = (dir: string) => {
    setFilter("");
    const parts = dir.split("/");
    const chain = parts.map((_, i) => parts.slice(0, i + 1).join("/"));
    setExpanded((prev) => new Set([...prev, ...chain]));
    load(chain);
  };

  const loadMore = async (dir: string, page: TreePage) => {
    try {
      const next = await readTree(sessionId, dir, page.next_cursor, hidden);
      setDirectories((prev) => {
        const current = prev.get(dir);
        // A refresh read the folder again meanwhile: its rows already go on
        // from somewhere else, and these would repeat them.
        if (current && current.next_cursor !== page.next_cursor) return prev;
        return new Map(prev).set(dir, {
          ...next,
          entries: [...(current ?? page).entries, ...next.entries],
        });
      });
    } catch (err) {
      setTreeError(String(err));
    }
  };

  const row = (entry: FileEntry, depth: number) => {
    const isDir = entry.kind === "directory";
    const isLink = entry.kind === "symlink";
    const unopenable = isLink || entry.kind === "special";
    return (
      <li key={entry.path_rel} role="none">
        <button
          type="button"
          role="treeitem"
          aria-expanded={isDir ? expanded.has(entry.path_rel) : undefined}
          aria-selected={entry.path_rel === props.active}
          className={
            "files-tree-row" +
            (entry.path_rel === props.active ? " is-active" : "")
          }
          style={{ paddingLeft: 8 + depth * 14 }}
          title={isLink ? t("files.link") : entry.path_rel}
          disabled={unopenable}
          onClick={() =>
            isDir ? toggle(entry.path_rel) : props.onOpen(entry.path_rel)
          }
        >
          <span className="files-tree-glyph" aria-hidden>
            {isDir ? (
              <Chevron open={expanded.has(entry.path_rel)} />
            ) : (
              <IconFile path={entry.path_rel} />
            )}
          </span>
          <span className="files-tree-name">{entry.name}</span>
          {isLink ? (
            <span className="files-tree-link" aria-hidden>
              ↗
            </span>
          ) : null}
        </button>
        {isDir && expanded.has(entry.path_rel) ? level(entry.path_rel, depth + 1) : null}
      </li>
    );
  };

  const level = (dir: string, depth: number): React.ReactNode => {
    const page = directories.get(dir);
    if (!page) return null;
    return (
      <ul className="files-tree-level" role={depth ? "group" : "tree"}>
        {page.entries.map((entry) => row(entry, depth))}
        {page.has_more ? (
          <li role="none">
            <button
              type="button"
              className="files-tree-more"
              style={{ paddingLeft: 8 + depth * 14 + 20 }}
              onClick={() => void loadMore(dir, page)}
            >
              {t("files.moreEntries")}
            </button>
          </li>
        ) : null}
      </ul>
    );
  };

  return (
    <aside className="files-sidebar">
      <div className="files-filter">
        <IconSearch />
        <input
          ref={props.filterRef}
          type="search"
          value={filter}
          placeholder={t("files.search")}
          aria-label={t("files.search")}
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && filter) {
              // This Escape is the filter's: the window stays for the next one.
              e.preventDefault();
              setFilter("");
            }
          }}
        />
      </div>
      <div className="files-tree" data-testid="files-tree">
        {treeError ? <p role="alert" className="files-note">{treeError}</p> : null}
        {hits ? (
          hits.length > 0 ? (
            <ul className="files-tree-level" role="listbox" aria-label={t("files.search")}>
              {hits.map((hit) => (
                <li key={`${hit.kind}:${hit.path}`} role="none">
                  <button
                    type="button"
                    role="option"
                    aria-selected={hit.path === props.active}
                    className={
                      "files-tree-row files-hit" +
                      (hit.path === props.active ? " is-active" : "")
                    }
                    title={hit.path}
                    onClick={() =>
                      hit.kind === "directory" ? reveal(hit.path) : props.onOpen(hit.path)
                    }
                  >
                    <span className="files-tree-glyph" aria-hidden>
                      {hit.kind === "directory" ? <IconFolderSmall /> : <IconFile path={hit.path} />}
                    </span>
                    <span className="files-tree-name">{hit.path}</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="files-note">
              {searching ? t("files.searching") : t("files.noMatches")}
            </p>
          )
        ) : query ? (
          <p className="files-note">{t("files.searching")}</p>
        ) : (
          level("", 0)
        )}
      </div>
    </aside>
  );
}
