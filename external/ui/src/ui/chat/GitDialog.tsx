import React, { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";

export function GitDialog(props: {
  title: string;
  wide?: boolean;
  busy?: boolean;
  onClose: () => void;
  toolbar?: React.ReactNode;
  children: React.ReactNode;
}) {
  const { t } = useT();
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(props.onClose);
  close.current = props.onClose;
  const busy = useRef(props.busy);
  busy.current = props.busy;
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const card = ref.current!;
    (
      card.querySelector<HTMLElement>("input") ||
      card.querySelector<HTMLElement>("button") ||
      card
    ).focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.isComposing) {
        event.preventDefault();
        event.stopImmediatePropagation();
        if (!busy.current) close.current();
      }
      if (event.key !== "Tab") return;
      const nodes = Array.from(
        card.querySelectorAll<HTMLElement>(
          'input:not(:disabled), button:not(:disabled), [tabindex="0"]',
        ),
      );
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (!first || !last) {
        event.preventDefault();
        card.focus();
        return;
      }
      if (
        event.shiftKey
          ? document.activeElement === first ||
            !card.contains(document.activeElement)
          : document.activeElement === last ||
            !card.contains(document.activeElement)
      ) {
        event.preventDefault();
        (event.shiftKey ? last : first)?.focus();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      if (opener?.isConnected) opener.focus();
    };
  }, []);
  return createPortal(
    <div
      className="git-dialog-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !props.busy)
          props.onClose();
      }}
    >
      <div
        ref={ref}
        className={`git-dialog${props.wide ? " git-dialog--graph" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label={props.title}
        tabIndex={-1}
      >
        <header className="git-dialog-header">
          <h2>{props.title}</h2>
          {props.toolbar}
          <button
            type="button"
            className="git-dialog-icon"
            aria-label={t("chat.contextClose")}
            disabled={props.busy}
            onClick={props.onClose}
          >
            ×
          </button>
        </header>
        {props.children}
      </div>
    </div>,
    document.body,
  );
}
