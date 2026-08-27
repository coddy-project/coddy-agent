import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  cleanPathInput,
  type WorkspaceFolderListing,
} from "./workspaceContext";

type Props = {
  open: boolean;
  // Folder the browser starts in (usually the parent of the current workspace).
  startPath: string;
  onClose: () => void;
  onPick: (path: string) => void;
};

// WorkspaceFolderModal is the project-styled "Open folder" dialog: it browses
// the server-side filesystem via GET /coddy/workspace/folders and picks the
// currently browsed folder with the Open button. The path row is editable, and
// on Windows ".." out of a drive root reaches the drive list served as the
// ":drives:" pseudo-folder.
export function WorkspaceFolderModal(props: Props) {
  const [listing, setListing] = useState<WorkspaceFolderListing | null>(null);
  const [error, setError] = useState("");
  // What the path field shows; empty on the drive level, which has no path.
  const [draft, setDraft] = useState("");
  const browseRequest = useRef(0);

  const browse = async (path: string) => {
    const request = ++browseRequest.current;
    try {
      const res = await fetch(
        "/coddy/workspace/folders?path=" + encodeURIComponent(path),
      );
      if (!res.ok) {
        if (request === browseRequest.current) {
          setError("Cannot list " + path);
        }
        return;
      }
      const next = (await res.json()) as WorkspaceFolderListing;
      if (request !== browseRequest.current) {
        return;
      }
      setListing(next);
      setDraft(next.drives ? "" : next.path);
      setError("");
    } catch {
      if (request === browseRequest.current) {
        setError("Cannot list " + path);
      }
    }
  };

  useEffect(() => {
    if (props.open) {
      setListing(null);
      setError("");
      setDraft(props.startPath);
      void browse(props.startPath);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, props.startPath]);

  if (!props.open) {
    return null;
  }

  // A typed path that has not been visited yet turns the primary button into
  // "Go", so a pasted path is never mistaken for the folder being opened.
  const typed = cleanPathInput(draft);
  const pendingPath = typed && typed !== listing?.path ? typed : "";

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
        <input
          className="workspace-modal-path"
          data-testid="workspace-modal-path"
          aria-label="Folder path"
          spellCheck={false}
          autoComplete="off"
          placeholder={listing?.drives ? "This PC" : "Path"}
          title={listing?.path || props.startPath}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && pendingPath) {
              e.preventDefault();
              void browse(pendingPath);
            }
          }}
        />
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
                {listing?.drives ? (
                  <svg
                    viewBox="0 0 16 16"
                    width="12"
                    height="12"
                    fill="currentColor"
                  >
                    <path d="M2 3.25c0-.41.34-.75.75-.75h10.5c.41 0 .75.34.75.75v5.25H1.5V3.25Zm-.5 6.25h13c.28 0 .5.22.5.5v2.75c0 .41-.34.75-.75.75H1.75a.75.75 0 0 1-.75-.75V10c0-.28.22-.5.5-.5Zm10.25 1.25a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z" />
                  </svg>
                ) : (
                  <svg
                    viewBox="0 0 16 16"
                    width="12"
                    height="12"
                    fill="currentColor"
                  >
                    <path d="M1.75 2.5h4.3l1.4 1.5h6.8c.41 0 .75.34.75.75v8c0 .41-.34.75-.75.75H1.75a.75.75 0 0 1-.75-.75v-9.5c0-.41.34-.75.75-.75Z" />
                  </svg>
                )}
              </span>
              {f.name}
            </button>
          ))}
          {listing && listing.folders.length === 0 && !error ? (
            <div className="mode-menu-empty">
              {listing.drives ? "No drives" : "No subfolders"}
            </div>
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
            disabled={!listing || (!pendingPath && Boolean(listing.drives))}
            onClick={() => {
              if (pendingPath) {
                void browse(pendingPath);
                return;
              }
              if (listing && !listing.drives) {
                props.onPick(listing.path);
              }
            }}
          >
            {pendingPath ? "Go" : "Open"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
