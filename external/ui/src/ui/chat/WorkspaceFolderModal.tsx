import React, { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
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
// ":drives:" pseudo-folder. New folder creates a subfolder of the browsed
// directory (POST /coddy/workspace/folders) and steps into it, so a workspace
// can be started without leaving the SPA for a terminal.
export function WorkspaceFolderModal(props: Props) {
  const { t } = useT();
  const [listing, setListing] = useState<WorkspaceFolderListing | null>(null);
  const [error, setError] = useState("");
  // What the path field shows; empty on the drive level, which has no path.
  const [draft, setDraft] = useState("");
  // The inline "new folder" row: shown on demand, holding the typed name.
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const browseRequest = useRef(0);

  // adopt shows a listing the server just answered with, closing the new-folder
  // row: both browsing and creating land on a folder the dialog now displays.
  const adopt = (next: WorkspaceFolderListing) => {
    setListing(next);
    setDraft(next.drives ? "" : next.path);
    setError("");
    setCreating(false);
    setNewName("");
  };

  const browse = async (path: string) => {
    const request = ++browseRequest.current;
    try {
      const res = await fetch(
        "/coddy/workspace/folders?path=" + encodeURIComponent(path),
      );
      if (!res.ok) {
        if (request === browseRequest.current) {
          setError(t("composer.folderModal.cannotList", { path }));
        }
        return;
      }
      const next = (await res.json()) as WorkspaceFolderListing;
      if (request !== browseRequest.current) {
        return;
      }
      adopt(next);
    } catch {
      if (request === browseRequest.current) {
        setError(t("composer.folderModal.cannotList", { path }));
      }
    }
  };

  // createFolder makes one subfolder of the browsed directory and steps into
  // the listing the server answers with. It shares the browse ticket, so an
  // answer that arrives after the user navigated elsewhere is dropped rather
  // than pulling the dialog back. A name that is already taken keeps the row
  // open with the typed text, which is what the operator has to correct.
  const createFolder = async () => {
    const name = newName.trim();
    if (!listing || listing.drives || !name) {
      return;
    }
    const parent = listing.path;
    const request = ++browseRequest.current;
    try {
      const res = await fetch("/coddy/workspace/folders", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: parent, name }),
      });
      if (request !== browseRequest.current) {
        return;
      }
      if (!res.ok) {
        setError(
          res.status === 409
            ? t("composer.folderModal.folderExists", { name })
            : t("composer.folderModal.cannotCreate", { name }),
        );
        return;
      }
      adopt((await res.json()) as WorkspaceFolderListing);
    } catch {
      if (request === browseRequest.current) {
        setError(t("composer.folderModal.cannotCreate", { name }));
      }
    }
  };

  useEffect(() => {
    if (props.open) {
      setListing(null);
      setError("");
      setDraft(props.startPath);
      setCreating(false);
      setNewName("");
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
        aria-label={t("composer.folderModal.title")}
        data-testid="workspace-folder-modal"
      >
        <div className="workspace-modal-head">
          <span>{t("composer.folderModal.title")}</span>
          <button
            type="button"
            className="sessions-close"
            aria-label={t("composer.folderModal.close")}
            onClick={props.onClose}
          >
            ×
          </button>
        </div>
        <input
          className="workspace-modal-path"
          data-testid="workspace-modal-path"
          aria-label={t("composer.folderModal.pathLabel")}
          spellCheck={false}
          autoComplete="off"
          placeholder={
            listing?.drives
              ? t("composer.folderModal.drivesPlaceholder")
              : t("composer.folderModal.pathPlaceholder")
          }
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
        {creating ? (
          <div className="workspace-modal-row workspace-modal-row--new">
            <span className="workspace-chip-icon" aria-hidden="true">
              <svg
                viewBox="0 0 16 16"
                width="12"
                height="12"
                fill="currentColor"
              >
                <path d="M1.75 2.5h4.3l1.4 1.5h6.8c.41 0 .75.34.75.75v8c0 .41-.34.75-.75.75H1.75a.75.75 0 0 1-.75-.75v-9.5c0-.41.34-.75.75-.75Z" />
              </svg>
            </span>
            <input
              className="workspace-modal-new-name"
              data-testid="workspace-modal-new-folder-name"
              aria-label={t("composer.folderModal.newFolderPlaceholder")}
              placeholder={t("composer.folderModal.newFolderPlaceholder")}
              spellCheck={false}
              autoComplete="off"
              autoFocus
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void createFolder();
                }
                if (e.key === "Escape") {
                  e.preventDefault();
                  setCreating(false);
                  setNewName("");
                }
              }}
            />
            <button
              type="button"
              className="workspace-modal-btn workspace-modal-btn--primary workspace-modal-btn--inline"
              data-testid="workspace-modal-new-folder-create"
              disabled={!newName.trim()}
              onClick={() => void createFolder()}
            >
              {t("composer.folderModal.newFolderCreate")}
            </button>
            <button
              type="button"
              className="workspace-modal-btn workspace-modal-btn--inline"
              data-testid="workspace-modal-new-folder-cancel"
              aria-label={t("composer.folderModal.newFolderCancel")}
              onClick={() => {
                setCreating(false);
                setNewName("");
              }}
            >
              ×
            </button>
          </div>
        ) : null}
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
              {listing.drives
                ? t("composer.folderModal.noDrives")
                : t("composer.folderModal.noSubfolders")}
            </div>
          ) : null}
        </div>
        <div className="workspace-modal-actions">
          <button
            type="button"
            className="workspace-modal-btn workspace-modal-btn--lead"
            data-testid="workspace-modal-new-folder"
            disabled={!listing || Boolean(listing.drives) || creating}
            onClick={() => {
              setCreating(true);
              setNewName("");
              setError("");
            }}
          >
            {t("composer.folderModal.newFolder")}
          </button>
          <button
            type="button"
            className="workspace-modal-btn"
            data-testid="workspace-modal-cancel"
            onClick={props.onClose}
          >
            {t("composer.folderModal.cancel")}
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
            {pendingPath
              ? t("composer.folderModal.go")
              : t("composer.folderModal.open")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
