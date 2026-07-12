import React, { useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import {
  branchChipVisible,
  folderChipLabel,
  isWorktreeBadgeActive,
  pathBasename,
  pathParent,
  sortedBranches,
  type WorkspaceContext,
} from "./workspaceContext";
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

type Props = {
  context: WorkspaceContext | null;
  worktreePref: boolean;
  onPickFolder: (path: string) => void;
  onPickBranch: (branch: string, worktree: boolean) => void;
  onWorktreeToggle: () => void;
  // Anchored dropdown direction; the docked composer opens the menu upward.
  opensUp?: boolean;
  // The workspace is chosen once: locked as soon as the conversation starts.
  locked?: boolean;
};

type MenuKind = "folder" | "branch" | null;

// WorkspaceChips renders the workspace context row above the composer field:
// a folder chip (recent folders + "Open folder…" browser), a branch chip
// (branch list inside git repos), and a worktree checkbox.
export function WorkspaceChips(props: Props) {
  const [menuOpen, setMenuOpen] = useState<MenuKind>(null);
  const [menuAnchorRect, setMenuAnchorRect] = useState<DOMRect | null>(null);
  const [recents, setRecents] = useState<WorkspaceRecent[]>([]);
  const [folderModalOpen, setFolderModalOpen] = useState(false);
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const menuUseSheet = isMobileShell;

  const ctx = props.context;
  if (!ctx) {
    return null;
  }
  const locked = Boolean(props.locked);

  const closeMenu = () => {
    setMenuOpen(null);
    setMenuAnchorRect(null);
  };

  const toggleMenu = (kind: Exclude<MenuKind, null>, trigger: HTMLElement) => {
    if (locked) {
      return;
    }
    if (menuOpen === kind) {
      closeMenu();
      return;
    }
    setMenuOpen(kind);
    setMenuAnchorRect(trigger.getBoundingClientRect());
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
    <div className="composer-context-chips">
      <button
        type="button"
        className="workspace-chip"
        data-testid="composer-workspace-chip"
        title={ctx.path}
        aria-haspopup="menu"
        disabled={locked}
        onClick={(e) => toggleMenu("folder", e.currentTarget)}
      >
        <span className="workspace-chip-icon" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
            <path d="M1.75 2.5h4.3l1.4 1.5h6.8c.41 0 .75.34.75.75v8c0 .41-.34.75-.75.75H1.75a.75.75 0 0 1-.75-.75v-9.5c0-.41.34-.75.75-.75Z" />
          </svg>
        </span>
        <span className="workspace-chip-label">{folderChipLabel(ctx)}</span>
      </button>

      {showBranch ? (
        <button
          type="button"
          className="workspace-chip"
          data-testid="composer-branch-chip"
          title={ctx.branch || "detached"}
          aria-haspopup="menu"
          disabled={locked}
          onClick={(e) => toggleMenu("branch", e.currentTarget)}
        >
          <span className="workspace-chip-icon" aria-hidden="true">
            <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
              <path d="M5 3.25a1.75 1.75 0 1 1-2.5-1.58V3.25a3.25 3.25 0 0 0 3.25 3.25h3.5c.97 0 1.75.78 1.75 1.75v.42a1.75 1.75 0 1 1-1.5 0V8.25a.25.25 0 0 0-.25-.25h-3.5A4.73 4.73 0 0 1 3.5 7.1v3.23a1.75 1.75 0 1 1-1.5 0V4.83A1.75 1.75 0 0 1 5 3.25Z" />
            </svg>
          </span>
          <span className="workspace-chip-label">{ctx.branch || "detached"}</span>
        </button>
      ) : null}

      {showBranch ? (
        <label
          className={`workspace-chip workspace-chip--check ${worktreeActive ? "is-active" : ""} ${locked || ctx.is_worktree ? "is-locked" : ""}`}
          data-testid="composer-worktree-chip"
          title={
            ctx.is_worktree
              ? "This session works in a dedicated worktree"
              : "Open branch switches in a dedicated worktree"
          }
        >
          <input
            type="checkbox"
            className="workspace-chip-checkbox"
            data-testid="composer-worktree-checkbox"
            checked={worktreeActive}
            disabled={locked || ctx.is_worktree}
            onChange={() => props.onWorktreeToggle()}
          />
          <span className="workspace-chip-label">worktree</span>
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
                    <div className="mode-menu-group-label">Recent</div>
                    <div className="mode-menu-scroll">
                      {recentRows.map((r) => (
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
                          <span className="workspace-recent-name">{r.name}</span>
                          {r.path === ctx.path ? (
                            <span className="workspace-recent-check" aria-hidden="true">
                              ✓
                            </span>
                          ) : null}
                        </button>
                      ))}
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
                      Open folder…
                    </button>
                  </>
                ) : null}
                {menuOpen === "branch" ? (
                  <div className="mode-menu-scroll">
                    {sortedBranches(ctx).map((b) => (
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
                      <div className="mode-menu-empty">No branches</div>
                    ) : null}
                  </div>
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
