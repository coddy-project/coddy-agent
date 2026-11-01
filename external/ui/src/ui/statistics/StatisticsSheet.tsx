import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";

/** A centered panel on desktop and a bottom sheet on phones. */
export function StatisticsSheet({
  title,
  onClose,
  children,
  busy = false,
  closeLabel,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  busy?: boolean;
  closeLabel?: string;
}) {
  const { t } = useT();
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  const busyRef = useRef(busy);
  busyRef.current = busy;
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const siblings = Array.from(document.body.children)
      .filter(
        (element): element is HTMLElement =>
          element instanceof HTMLElement && !element.contains(ref.current),
      )
      .map((element) => ({ element, inert: element.inert }));
    for (const { element } of siblings) element.inert = true;
    ref.current?.focus();
    const key = (e: KeyboardEvent) => {
      // A destructive confirmation can open above the settings sheet.
      if (
        Array.from(document.querySelectorAll('[role="dialog"]')).at(-1) !==
        ref.current
      )
        return;
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopImmediatePropagation();
        if (!busyRef.current) close.current();
      } else if (e.key === "Tab") {
        const items = Array.from(
          ref.current?.querySelectorAll<HTMLElement>(
            'button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex="0"]',
          ) ?? [],
        );
        const first = items[0];
        const last = items.at(-1);
        if (!first || !last) {
          e.preventDefault();
          return;
        }
        if (
          e.shiftKey
            ? document.activeElement === first ||
              document.activeElement === ref.current
            : document.activeElement === last ||
              document.activeElement === ref.current
        ) {
          e.preventDefault();
          (e.shiftKey ? last : first).focus();
        }
      }
    };
    window.addEventListener("keydown", key, true);
    return () => {
      window.removeEventListener("keydown", key, true);
      for (const { element, inert } of siblings) element.inert = inert;
      if (opener?.isConnected) opener.focus();
    };
  }, []);
  return createPortal(
    <div
      className="statistics-sheet-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        className="statistics-sheet"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        ref={ref}
      >
        <header className="statistics-sheet-head">
          <h2>{title}</h2>
          <button onClick={onClose} disabled={busy}>
            {closeLabel ?? t("stats.done")}
          </button>
        </header>
        <div className="statistics-sheet-body">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
