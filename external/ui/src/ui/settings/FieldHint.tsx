import { useEffect, useState, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";

import { useT } from "../i18n/I18nProvider";
import {
  serverSnapshotTouchOnly,
  snapshotTouchOnly,
  subscribeTouchOnly,
} from "../shellBreakpoint";

function InfoGlyph() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 14 14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      aria-hidden="true"
    >
      <circle cx="7" cy="7" r="5.6" />
      <path d="M7 6.6v2.8" strokeLinecap="round" />
      <circle cx="7" cy="4.3" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  );
}

/**
 * FieldHint is the (i) control next to a settings field's label, carrying the
 * help text that used to sit under the field as a paragraph of its own. On
 * hover-capable devices a small scrollable tooltip opens on hover or keyboard
 * focus (CSS-driven under `@media (hover: hover)`); on touch-only devices the
 * tap opens a scrollable sheet instead, so the two never fight. The text is
 * split on newlines into short paragraphs - dictionary descriptions carry the
 * breaks.
 */
export function FieldHint(props: { text: ReactNode; testid?: string }) {
  const { t } = useT();
  const touchOnly = useSyncExternalStore(
    subscribeTouchOnly,
    snapshotTouchOnly,
    serverSnapshotTouchOnly,
  );
  const [open, setOpen] = useState(false);
  const paragraphs =
    typeof props.text === "string"
      ? props.text
          .split(/\n+/)
          .map((p) => p.trim())
          .filter(Boolean)
      : [props.text];

  return (
    <span className="field-hint">
      <button
        type="button"
        className="field-hint-btn"
        data-testid={props.testid ?? "field-hint"}
        aria-label={t("settings.fieldHint")}
        onClick={(e) => {
          // Never let the click reach a wrapping label or toggle a control;
          // on touch-only it opens the sheet - elsewhere the hover/focus
          // tooltip already carries the text.
          e.preventDefault();
          e.stopPropagation();
          if (touchOnly) {
            setOpen(true);
          }
        }}
      >
        <InfoGlyph />
      </button>
      <span className="field-hint-tip" role="tooltip">
        {paragraphs.map((p, i) => (
          <span key={i} className="field-hint-para">
            {p}
          </span>
        ))}
      </span>
      {open
        ? createPortal(
            <HintSheet paragraphs={paragraphs} onClose={() => setOpen(false)} />,
            document.body,
          )
        : null}
    </span>
  );
}

function HintSheet(props: { paragraphs: ReactNode[]; onClose: () => void }) {
  const { t } = useT();
  const { onClose } = props;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="field-hint-backdrop" onClick={onClose}>
      <div
        className="field-hint-sheet"
        role="dialog"
        aria-modal="true"
        aria-label={t("settings.fieldHint")}
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          className="sessions-close field-hint-sheet-close"
          aria-label={t("settings.fieldHintClose")}
          title={t("settings.fieldHintClose")}
          onClick={onClose}
        >
          ×
        </button>
        <div className="field-hint-sheet-body">
          {props.paragraphs.map((p, i) => (
            <p key={i}>{p}</p>
          ))}
        </div>
      </div>
    </div>
  );
}
