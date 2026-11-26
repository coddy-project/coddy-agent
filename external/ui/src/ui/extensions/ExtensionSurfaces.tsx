import { useEffect, useRef, useState } from "react";
import { PanePortal } from "../components/PanePortal";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useT } from "../i18n/I18nProvider";
import type { RuntimeModal, RuntimeOverlay, RuntimeShell } from "./surfaces";

/** Runs one surface mount and takes its cleanup away with the row. */
function useMounted(
  mount: (element: HTMLElement, api: never) => void | (() => void),
  api: unknown,
  onError: (error: unknown) => void,
) {
  const element = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = element.current;
    if (!el) return;
    let cleanup: void | (() => void);
    try {
      cleanup = mount(el, api as never);
    } catch (error) {
      onError(error);
    }
    return () => {
      try {
        cleanup?.();
      } catch (error) {
        console.error(error);
      }
      el.replaceChildren();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mount, api]);
  return element;
}

/** One modal at a time: a dialog over the chat, Escape and the close button
 * take it away - the same window the extension pages use. */
export function ExtensionModalHost({
  modal,
  onClose,
}: {
  modal: RuntimeModal;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [error, setError] = useState("");
  const { t } = useT();
  useEscapeCloses(true, onClose);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  const body = useMounted(modal.mount, modal.api, (e) => setError(String(e)));
  return (
    <PanePortal>
      <dialog
        ref={dialog}
        className="extension-window"
        aria-label={modal.title}
        onCancel={onClose}
        onClose={onClose}
      >
        <header>
          <strong>{modal.title}</strong>
          <button type="button" className="settings-btn" onClick={onClose}>
            {t("extensions.close")}
          </button>
        </header>
        {error ? <p role="alert">{error}</p> : null}
        <div ref={body} className="extension-window-body" />
      </dialog>
    </PanePortal>
  );
}

/** A free-floating box: no scrim, no focus trap, only its own content takes
 * pointer events - a pet or a toast beside the chat. */
export function ExtensionOverlayHost({
  overlay,
  onClose,
}: {
  overlay: RuntimeOverlay;
  onClose: () => void;
}) {
  const [error, setError] = useState("");
  const position = overlay.position ?? {};
  const body = useMounted(overlay.mount, overlay.api, (e) =>
    setError(String(e)),
  );
  return (
    <PanePortal>
      <div
        className="extension-overlay"
        data-testid="extension-overlay"
        data-extension-renderer={overlay.key}
        style={{
          top: position.top !== undefined ? position.top : undefined,
          right: position.right !== undefined ? position.right : undefined,
          bottom: position.bottom !== undefined ? position.bottom : undefined,
          left: position.left !== undefined ? position.left : undefined,
        }}
      >
        {error ? (
          <p role="alert">
            {error}
            <button type="button" className="settings-btn" onClick={onClose}>
              ×
            </button>
          </p>
        ) : null}
        <div ref={body} />
      </div>
    </PanePortal>
  );
}

/** The shell takeover: the host draws nothing of its own, the mount gets
 * the window. The extension must offer its own way out. */
export function ExtensionShellHost({
  shell,
  onRelease,
}: {
  shell: RuntimeShell;
  onRelease: () => void;
}) {
  const [error, setError] = useState("");
  const body = useMounted(shell.mount, shell.api, (e) => setError(String(e)));
  return (
    <div className="extension-shell" data-testid="extension-shell">
      {error ? (
        <p role="alert">
          {error}
          <button type="button" className="settings-btn" onClick={onRelease}>
            ×
          </button>
        </p>
      ) : null}
      <div ref={body} />
    </div>
  );
}
