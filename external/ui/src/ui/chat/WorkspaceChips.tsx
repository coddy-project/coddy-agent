import React, { useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import {
  branchChipVisible,
  folderChipLabel,
  isWorktreeBadgeActive,
  pathParent,
  sortedBranches,
  type WorkspaceContext,
  type WorkspaceFolderListing,
} from "./workspaceContext";
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
  disabled?: boolean;
};

type MenuKind = "folder" | "branch" | null;

// WorkspaceChips renders the workspace context row above the composer field:
// a folder chip (opens the folder picker), a branch chip (opens the branch
// list inside git repos), and a worktree toggle chip.
export function WorkspaceChips(props: Props) {
  const [menuOpen, setMenuOpen] = useState<MenuKind>(null);
  const [menuAnchorRect, setMenuAnchorRect] = useState<DOMRect | null>(null);
  const [listing, setListing] = useState<WorkspaceFolderListing | null>(null);
  const [listingError, setListingError] = useState("");
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

  const closeMenu = () => {
    setMenuOpen(null);
    setMenuAnchorRect(null);
    setListing(null);
    setListingError("");
  };

  const browseFolders = async (path: string) => {
    try {
      const res = await fetch(
        "/coddy/workspace/folders?path=" + encodeURIComponent(path),
      );
      if (!res.ok) {
        setListingError("Cannot list " + path);
        return;
      }
      const body = (await res.json()) as WorkspaceFolderListing;
      setListing(body);
      setListingError("");
    } catch {
      setListingError("Cannot list " + path);
    }
  };

  const toggleMenu = (kind: Exclude<MenuKind, null>, trigger: HTMLElement) => {
    if (menuOpen === kind) {
      closeMenu();
      return;
    }
    setMenuOpen(kind);
    setMenuAnchorRect(trigger.getBoundingClientRect());
    if (kind === "folder") {
      // The picker opens at the parent so sibling projects are one click away.
      void browseFolders(pathParent(ctx.path));
    }
  };

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
        disabled={props.disabled}
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
          disabled={props.disabled}
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
        <button
          type="button"
          className={`workspace-chip workspace-chip--toggle ${worktreeActive ? "is-active" : ""}`}
          data-testid="composer-worktree-chip"
          title={
            ctx.is_worktree
              ? "This session works in a dedicated worktree"
              : "Open branch switches in a dedicated worktree"
          }
          aria-pressed={worktreeActive}
          disabled={props.disabled || ctx.is_worktree}
          onClick={() => props.onWorktreeToggle()}
        >
          <span className="workspace-chip-label">worktree</span>
        </button>
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
                    <div className="workspace-menu-path" title={listing?.path || ""}>
                      {listing?.path || ctx.path}
                    </div>
                    <div className="mode-menu-scroll">
                      {listingError ? (
                        <div className="mode-menu-empty">{listingError}</div>
                      ) : null}
                      {listing && listing.path !== listing.parent ? (
                        <button
                          type="button"
                          role="menuitem"
                          className="mode-item workspace-folder-up"
                          data-testid="workspace-folder-up"
                          onClick={() => void browseFolders(listing.parent)}
                        >
                          ..
                        </button>
                      ) : null}
                      {(listing?.folders || []).map((f) => (
                        <div key={f.path} className="workspace-folder-row">
                          <button
                            type="button"
                            role="menuitem"
                            className={`mode-item ${f.path === ctx.path ? "is-selected" : ""}`}
                            data-testid={`workspace-folder-row-${f.name}`}
                            title={f.path}
                            onClick={() => {
                              props.onPickFolder(f.path);
                              closeMenu();
                            }}
                          >
                            {f.name}
                          </button>
                          <button
                            type="button"
                            className="workspace-folder-browse"
                            data-testid={`workspace-folder-browse-${f.name}`}
                            aria-label={`Browse into ${f.name}`}
                            title={`Browse into ${f.name}`}
                            onClick={() => void browseFolders(f.path)}
                          >
                            ›
                          </button>
                        </div>
                      ))}
                      {listing && listing.folders.length === 0 && !listingError ? (
                        <div className="mode-menu-empty">No subfolders</div>
                      ) : null}
                    </div>
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
    </div>
  );
}
