import React, { useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import {
  BRANCH_CHARS,
  branchChipVisible,
  folderChipLabel,
  isWorktreeBadgeActive,
  middleTruncate,
  pathBasename,
  pathParent,
  sortedBranches,
  type WorkspaceContext,
} from "./workspaceContext";
import { BranchIcon, FolderIcon } from "./workspaceIcons";
import {
  pushWorkspaceRecent,
  readWorkspaceRecents,
  type WorkspaceRecent,
} from "./workspaceRecents";
import { WorkspaceFolderModal } from "./WorkspaceFolderModal";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";

type Props = {
  context: WorkspaceContext | null;
  worktreePref: boolean;
  onPickFolder: (path: string) => void;
  onPickBranch: (branch: string, worktree: boolean) => void;
  onWorktreeToggle: () => void;
  // Anchored dropdown direction; the docked composer opens the menu upward.
  opensUp?: boolean;
};

type MenuKind = "folder" | "branch" | null;

// WorkspaceChips renders the picks of the plate over the composer before a
// chat starts (WorkspaceBar, pick mode): the folder (recent folders + "Open
// folder…" browser), the branch (the branch list) and the worktree checkbox,
// the last two only inside a git repository. They are items of the plate's
// row, a lighter ground showing they are buttons. Once the chat runs the
// workspace is a fact and the plate names it without these.
export function WorkspaceChips(props: Props) {
  const { t } = useT();
  const [menuOpen, setMenuOpen] = useState<MenuKind>(null);
  const [menuAnchorRect, setMenuAnchorRect] = useState<DOMRect | null>(null);
  const [menuFilter, setMenuFilter] = useState("");
  const [recents, setRecents] = useState<WorkspaceRecent[]>([]);
  const [folderModalOpen, setFolderModalOpen] = useState(false);
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const menuUseSheet = isMobileShell;
  // Escape closes the folder or branch menu that is open.
  useEscapeCloses(menuOpen !== null, () => {
    setMenuOpen(null);
    setMenuAnchorRect(null);
    setMenuFilter("");
  });

  const ctx = props.context;
  if (!ctx) {
    return null;
  }

  const closeMenu = () => {
    setMenuOpen(null);
    setMenuAnchorRect(null);
    setMenuFilter("");
  };

  const toggleMenu = (kind: Exclude<MenuKind, null>, trigger: HTMLElement) => {
    if (menuOpen === kind) {
      closeMenu();
      return;
    }
    setMenuOpen(kind);
    setMenuAnchorRect(trigger.getBoundingClientRect());
    setMenuFilter("");
    if (kind === "folder") {
      setRecents(readWorkspaceRecents());
    }
  };

  const pickFolder = (path: string) => {
    props.onPickFolder(path);
    setRecents(pushWorkspaceRecent({ path, name: pathBasename(path) || path }));
    setFolderModalOpen(false);
    closeMenu();
  };

  // The current workspace always appears in the Recent list (checked).
  const recentRows: WorkspaceRecent[] = recents.some((r) => r.path === ctx.path)
    ? recents
    : [{ path: ctx.path, name: folderChipLabel(ctx) }, ...recents];
  const filter = menuFilter.trim().toLocaleLowerCase();
  const filteredRecents = filter
    ? recentRows.filter((row) => row.name.toLocaleLowerCase().includes(filter))
    : recentRows;
  const branches = sortedBranches(ctx);
  const filteredBranches = filter
    ? branches.filter((branch) => branch.toLocaleLowerCase().includes(filter))
    : branches;

  const dirClass = props.opensUp ? "opens-up" : "opens-down";
  const menuStyle =
    menuUseSheet || !menuAnchorRect
      ? undefined
      : props.opensUp
        ? {
            left: menuAnchorRect.left,
            bottom: window.innerHeight - menuAnchorRect.top + 8,
          }
        : { left: menuAnchorRect.left, top: menuAnchorRect.bottom + 8 };

  const showBranch = branchChipVisible(ctx);
  const worktreeActive = isWorktreeBadgeActive(ctx, props.worktreePref);

  return (
    <div className="workspace-bar-picks">
      <button
        type="button"
        className={`workspace-bar-item workspace-bar-repo workspace-bar-pick${menuOpen === "folder" ? " is-open" : ""}`}
        data-testid="composer-workspace-chip"
        title={ctx.is_worktree && ctx.repo_root ? ctx.repo_root : ctx.path}
        aria-haspopup="menu"
        aria-expanded={menuOpen === "folder"}
        onClick={(e) => toggleMenu("folder", e.currentTarget)}
      >
        <FolderIcon />
        <span className="workspace-bar-text">{folderChipLabel(ctx)}</span>
      </button>

      {showBranch ? (
        <button
          type="button"
          className={`workspace-bar-item workspace-bar-branch workspace-bar-pick${menuOpen === "branch" ? " is-open" : ""}`}
          data-testid="composer-branch-chip"
          title={ctx.branch || t("workspace.detached")}
          aria-haspopup="menu"
          aria-expanded={menuOpen === "branch"}
          onClick={(e) => toggleMenu("branch", e.currentTarget)}
        >
          <BranchIcon worktree={ctx.is_worktree === true} />
          <span className="workspace-bar-text">
            {middleTruncate(ctx.branch || t("workspace.detached"), BRANCH_CHARS)}
          </span>
        </button>
      ) : null}

      {showBranch ? (
        <label
          className={`workspace-bar-check${worktreeActive ? " is-active" : ""}${ctx.is_worktree ? " is-locked" : ""}`}
          data-testid="composer-worktree-chip"
          title={
            ctx.is_worktree
              ? t("workspace.worktreeActiveTitle")
              : t("workspace.worktreeInactiveTitle")
          }
        >
          <input
            type="checkbox"
            className="workspace-bar-checkbox"
            data-testid="composer-worktree-checkbox"
            checked={worktreeActive}
            disabled={ctx.is_worktree}
            onChange={() => props.onWorktreeToggle()}
          />
          <span className="workspace-bar-text">{t("workspace.worktree")}</span>
        </label>
      ) : null}

      {menuOpen && (menuUseSheet || menuAnchorRect)
        ? createPortal(
            <>
              <button
                type="button"
                className={`mode-menu-backdrop ${menuUseSheet ? "mode-menu-backdrop--scrim" : ""}`}
                aria-hidden="true"
                tabIndex={-1}
                onMouseDown={(e) => {
                  e.preventDefault();
                  closeMenu();
                }}
              />
              <div
                className={`mode-menu workspace-menu ${menuUseSheet ? "mode-menu--sheet" : `mode-menu--portal ${dirClass}`}`}
                role="menu"
                data-testid={
                  menuOpen === "folder"
                    ? "workspace-folder-menu"
                    : "workspace-branch-menu"
                }
                style={menuStyle}
              >
                {menuOpen === "folder" ? (
                  <>
                    <div className="mode-menu-group-label">
                      {t("workspace.recent")}
                    </div>
                    <input
                      className="mode-menu-filter"
                      data-testid="workspace-recent-filter"
                      value={menuFilter}
                      placeholder={t("workspace.filterRecent")}
                      aria-label={t("workspace.filterRecent")}
                      autoFocus
                      onChange={(event) => setMenuFilter(event.target.value)}
                    />
                    <div className="mode-menu-scroll">
                      {filteredRecents.map((r) => (
                        <button
                          key={r.path}
                          type="button"
                          role="menuitem"
                          className={`mode-item workspace-recent-item ${r.path === ctx.path ? "is-selected" : ""}`}
                          data-testid={`workspace-recent-row-${r.name}`}
                          title={r.path}
                          onClick={() => {
                            if (r.path !== ctx.path) {
                              pickFolder(r.path);
                            } else {
                              closeMenu();
                            }
                          }}
                        >
                          <span className="workspace-recent-name">
                            {r.name}
                          </span>
                          {r.path === ctx.path ? (
                            <span
                              className="workspace-recent-check"
                              aria-hidden="true"
                            >
                              ✓
                            </span>
                          ) : null}
                        </button>
                      ))}
                      {filteredRecents.length === 0 ? (
                        <div
                          className="mode-menu-empty"
                          data-testid="workspace-recent-empty"
                        >
                          {t("workspace.noRecentMatch")}
                        </div>
                      ) : null}
                    </div>
                    <div className="workspace-menu-sep" aria-hidden="true" />
                    <button
                      type="button"
                      role="menuitem"
                      className="mode-item workspace-open-folder"
                      data-testid="workspace-open-folder"
                      onClick={() => {
                        closeMenu();
                        setFolderModalOpen(true);
                      }}
                    >
                      {t("workspace.openFolder")}
                    </button>
                  </>
                ) : null}
                {menuOpen === "branch" ? (
                  <>
                    <input
                      className="mode-menu-filter"
                      data-testid="workspace-branch-filter"
                      value={menuFilter}
                      placeholder={t("workspace.filterBranches")}
                      aria-label={t("workspace.filterBranches")}
                      autoFocus
                      onChange={(event) => setMenuFilter(event.target.value)}
                    />
                    <div className="mode-menu-scroll">
                      {filteredBranches.map((b) => (
                        <button
                          key={b}
                          type="button"
                          role="menuitem"
                          title={b}
                          className={`mode-item ${b === ctx.branch ? "is-selected" : ""}`}
                          data-testid={`workspace-branch-row-${b}`}
                          onClick={() => {
                            if (b !== ctx.branch) {
                              props.onPickBranch(b, props.worktreePref);
                            }
                            closeMenu();
                          }}
                        >
                          {b}
                        </button>
                      ))}
                      {(ctx.branches || []).length === 0 ? (
                        <div className="mode-menu-empty">
                          {t("workspace.noBranches")}
                        </div>
                      ) : null}
                      {(ctx.branches || []).length > 0 &&
                      filteredBranches.length === 0 ? (
                        <div
                          className="mode-menu-empty"
                          data-testid="workspace-branch-empty"
                        >
                          {t("workspace.noBranchesMatch")}
                        </div>
                      ) : null}
                    </div>
                  </>
                ) : null}
              </div>
            </>,
            document.body,
          )
        : null}

      <WorkspaceFolderModal
        open={folderModalOpen}
        startPath={pathParent(ctx.path)}
        onClose={() => setFolderModalOpen(false)}
        onPick={pickFolder}
      />
    </div>
  );
}
