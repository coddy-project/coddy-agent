import React, { useEffect, useRef } from "react";
import { createPortal } from "react-dom";

export type ConfirmDialogVariant = "danger" | "primary";

export type ConfirmDialogProps = {
  open: boolean;
  /** Short question shown as the dialog heading, e.g. "Delete chat?". */
  title: string;
  /** Longer explanatory body. Optional. */
  message?: string | undefined;
  /** Label on the affirmative action button. */
  confirmLabel: string;
  /** Label on the dismissal button. Defaults to "Cancel". */
  cancelLabel?: string | undefined;
  /** danger = red destructive action; primary = accent-coloured action. */
  variant?: ConfirmDialogVariant | undefined;
  /** Used for aria-label on the dialog itself. */
  ariaLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  /** When true, action buttons are disabled (e.g. while a DELETE is in flight). */
  confirming?: boolean | undefined;
  dataTestId?: string | undefined;
};

// ConfirmDialog is the shared presentational confirmation popup used by every
// destructive action (delete session, delete scheduler job, ...). It mirrors the
// WorkspaceFolderModal shell: a portal to document.body, role="dialog"
// aria-modal="true", backdrop + click-outside + Escape to cancel, and a
// Cancel / action pair. Visual styling lives in styles.css under .confirm-dialog*.
export function ConfirmDialog(props: ConfirmDialogProps) {
  const {
    open,
    title,
    message,
    confirmLabel,
    cancelLabel = "Cancel",
    variant = "primary",
    ariaLabel,
    onConfirm,
    onCancel,
    confirming = false,
    dataTestId,
  } = props;

  // Focus Cancel first for destructive actions: an accidental Enter must not
  // confirm a delete. Kept as a ref so we can move focus without re-render loops.
  const cancelRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    // Move focus to Cancel when the dialog opens (Escape/Enter then dismisses).
    const id = window.setTimeout(() => {
      cancelRef.current?.focus();
    }, 0);
    return () => window.clearTimeout(id);
  }, [open]);

  // Escape cancels the dialog. Listens on window so focus state does not matter.
  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) {
    return null;
  }

  const confirmClass =
    variant === "danger"
      ? "confirm-dialog-btn confirm-dialog-btn--danger"
      : "confirm-dialog-btn confirm-dialog-btn--primary";

  return createPortal(
    <div
      className="confirm-dialog-backdrop"
      onMouseDown={(e) => {
        // Only treat a direct click on the scrim as cancel; clicks that start
        // inside the card must not dismiss the dialog.
        if (e.target === e.currentTarget) {
          onCancel();
        }
      }}
    >
      <div
        className="confirm-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
        data-testid={dataTestId}
      >
        <div className="confirm-dialog-body">
          <span
            className={
              variant === "danger"
                ? "confirm-dialog-icon confirm-dialog-icon--danger"
                : "confirm-dialog-icon confirm-dialog-icon--primary"
            }
            aria-hidden="true"
          >
            {variant === "danger" ? (
              <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M3 6h18" />
                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                <path d="M10 11v6" />
                <path d="M14 11v6" />
              </svg>
            ) : (
              <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M12 16v-4" />
                <path d="M12 8h.01" />
              </svg>
            )}
          </span>
          <div className="confirm-dialog-text">
            <h2 className="confirm-dialog-title">{title}</h2>
            {message ? (
              <p className="confirm-dialog-message">{message}</p>
            ) : null}
          </div>
        </div>
        <div className="confirm-dialog-actions">
          <button
            ref={cancelRef}
            type="button"
            className="confirm-dialog-btn confirm-dialog-btn--cancel"
            onClick={onCancel}
            disabled={confirming}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={confirmClass}
            onClick={onConfirm}
            disabled={confirming}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
