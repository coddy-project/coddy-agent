import React, { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { WorkspaceFolderListing } from "./workspaceContext";

type Props = {
  open: boolean;
  // Folder the browser starts in (usually the parent of the current workspace).
  startPath: string;
  onClose: () => void;
  onPick: (path: string) => void;
};

// WorkspaceFolderModal is the project-styled "Open folder" dialog: it browses
// the server-side filesystem via GET /coddy/workspace/folders and picks the
// currently browsed folder with the Open button.
export function WorkspaceFolderModal(props: Props) {
  const [listing, setListing] = useState<WorkspaceFolderListing | null>(null);
  const [error, setError] = useState("");

  const browse = async (path: string) => {
    try {
      const res = await fetch(
        "/coddy/workspace/folders?path=" + encodeURIComponent(path),
      );
      if (!res.ok) {
        setError("Cannot list " + path);
        return;
      }
      setListing((await res.json()) as WorkspaceFolderListing);
      setError("");
    } catch {
      setError("Cannot list " + path);
    }
  };

  useEffect(() => {
    if (props.open) {
      setListing(null);
      setError("");
      void browse(props.startPath);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, props.startPath]);

  if (!props.open) {
    return null;
  }

  return createPortal(
    <div
      className="workspace-modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) {
          props.onClose();
        }
      }}
    >
      <div
        className="workspace-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Open folder"
        data-testid="workspace-folder-modal"
      >
        <div className="workspace-modal-head">
          <span>Open folder</span>
          <button
            type="button"
            className="sessions-close"
            aria-label="Close folder browser"
            onClick={props.onClose}
          >
            ×
          </button>
        </div>
        <div className="workspace-modal-path" title={listing?.path || props.startPath}>
          {listing?.path || props.startPath}
        </div>
        <div className="workspace-modal-list">
          {error ? <div className="mode-menu-empty">{error}</div> : null}
          {listing && listing.path !== listing.parent ? (
            <button
              type="button"
              className="workspace-modal-row workspace-modal-row--up"
              data-testid="workspace-modal-up"
              onClick={() => void browse(listing.parent)}
            >
              ..
            </button>
          ) : null}
          {(listing?.folders || []).map((f) => (
            <button
              key={f.path}
              type="button"
              className="workspace-modal-row"
              data-testid={`workspace-modal-row-${f.name}`}
              title={f.path}
              onClick={() => void browse(f.path)}
            >
              <span className="workspace-chip-icon" aria-hidden="true">
                <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
                  <path d="M1.75 2.5h4.3l1.4 1.5h6.8c.41 0 .75.34.75.75v8c0 .41-.34.75-.75.75H1.75a.75.75 0 0 1-.75-.75v-9.5c0-.41.34-.75.75-.75Z" />
                </svg>
              </span>
              {f.name}
            </button>
          ))}
          {listing && listing.folders.length === 0 && !error ? (
            <div className="mode-menu-empty">No subfolders</div>
          ) : null}
        </div>
        <div className="workspace-modal-actions">
          <button
            type="button"
            className="workspace-modal-btn"
            data-testid="workspace-modal-cancel"
            onClick={props.onClose}
          >
            Cancel
          </button>
          <button
            type="button"
            className="workspace-modal-btn workspace-modal-btn--primary"
            data-testid="workspace-modal-open"
            disabled={!listing}
            onClick={() => {
              if (listing) {
                props.onPick(listing.path);
              }
            }}
          >
            Open
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
