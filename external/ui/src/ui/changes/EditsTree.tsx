import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type React from "react";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import { IconFile, IconSearch } from "../files/windowIcons";
import { buildFileTree } from "./fileTree";
import type { FileTreeNode } from "./fileTree";
import { statusKey } from "./sessionChangesText";
import type { ChangeStatus, ChangedFile } from "./types";
import { rowInset } from "../files/treeGeometry";

/** How much room a row brought into view keeps from the tree's edge. */
const ROW_MARGIN_PX = 4;

/** The letter git's short status gives a file, beside its name in the tree. */
const STATUS_LETTER: Record<ChangeStatus, string> = {
  added: "A",
  modified: "M",
  deleted: "D",
};

/**
 * The left column of the edits window: the files git reports as changed and
 * not committed, in the folders they sit in, drawn the way the Files window
 * draws the workspace (the same filter, rows and glyphs). Only those files:
 * the tree is the table of contents of the diffs beside it, and picking a file
 * scrolls the diffs to it rather than filtering them.
 */
export function EditsTree(props: {
  files: ChangedFile[];
  /** The file on show: picked last, or at the top of the diffs. */
  active: string;
  onPick: (path: string) => void;
}) {
  const { t } = useT();
  const [filter, setFilter] = useState("");
  // Folders are open until folded; a folder is known by its path.
  const [folded, setFolded] = useState<Set<string>>(new Set());
  const treeRef = useRef<HTMLDivElement | null>(null);

  // With many files the tree scrolls too: the row the diffs mark is brought
  // into its view, by the tree alone.
  useLayoutEffect(() => {
    const tree = treeRef.current;
    const row = tree?.querySelector<HTMLElement>(".files-tree-row.is-active");
    if (!tree || !row) return;
    const port = tree.getBoundingClientRect();
    const box = row.getBoundingClientRect();
    if (box.top < port.top)
      tree.scrollTop -= port.top - box.top + ROW_MARGIN_PX;
    else if (box.bottom > port.bottom)
      tree.scrollTop += box.bottom - port.bottom + ROW_MARGIN_PX;
  }, [props.active]);

  const statuses = useMemo(() => {
    const byPath = new Map<string, ChangeStatus>();
    for (const f of props.files) byPath.set(f.path, f.status);
    return byPath;
  }, [props.files]);

  const nodes = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const paths = props.files
      .map((f) => f.path)
      .filter((p) => needle === "" || p.toLowerCase().includes(needle));
    return buildFileTree(paths);
  }, [props.files, filter]);

  const toggle = (path: string) =>
    setFolded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const row = (node: FileTreeNode, depth: number): React.ReactNode => {
    const indent = { paddingLeft: rowInset(depth) };
    if (node.kind === "file") {
      const status = statuses.get(node.path) || "modified";
      const active = node.path === props.active;
      return (
        <li key={node.path} role="none">
          <button
            type="button"
            role="treeitem"
            aria-selected={active}
            className={"files-tree-row" + (active ? " is-active" : "")}
            style={indent}
            title={`${node.path} (${t(statusKey(status))})`}
            data-testid={`edits-tree-file-${node.path}`}
            onClick={() => props.onPick(node.path)}
          >
            <span className="files-tree-glyph" aria-hidden>
              <IconFile path={node.path} />
            </span>
            <span className="files-tree-name">{node.label}</span>
            <span
              className={"edits-tree-status edits-tree-status--" + status}
              aria-label={t(statusKey(status))}
            >
              {STATUS_LETTER[status]}
            </span>
          </button>
        </li>
      );
    }
    const open = !folded.has(node.path);
    return (
      <li key={node.path} role="none">
        <button
          type="button"
          role="treeitem"
          aria-expanded={open}
          className="files-tree-row"
          style={indent}
          title={node.path}
          onClick={() => toggle(node.path)}
        >
          <span className="files-tree-glyph" aria-hidden>
            <Chevron open={open} />
          </span>
          <span className="files-tree-name">{node.label}</span>
        </button>
        {open ? (
          <ul className="files-tree-level" role="group">
            {node.children.map((child) => row(child, depth + 1))}
          </ul>
        ) : null}
      </li>
    );
  };

  return (
    <aside className="files-sidebar" aria-label={t("changes.viewer.files")}>
      <div className="files-filter">
        <IconSearch />
        <input
          type="search"
          value={filter}
          placeholder={t("changes.viewer.filterFiles")}
          aria-label={t("changes.viewer.filterFiles")}
          autoComplete="off"
          spellCheck={false}
          data-testid="edits-tree-filter"
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
      <div className="files-tree" data-testid="edits-tree" ref={treeRef}>
        {nodes.length === 0 ? (
          props.files.length > 0 ? (
            <p className="files-note">{t("changes.viewer.noMatches")}</p>
          ) : null
        ) : (
          <ul
            className="files-tree-level"
            role="tree"
            aria-label={t("changes.viewer.files")}
          >
            {nodes.map((node) => row(node, 0))}
          </ul>
        )}
      </div>
    </aside>
  );
}
