import { useEffect, useRef, useState } from "react";
import { PanePortal } from "../components/PanePortal";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useT } from "../i18n/I18nProvider";
import type { RuntimePanel } from "./panels";

/** Host surface of `ui.registerPanel`: a slide-over beside the chat, a full
 * overlay, or a drawer under the rail's screens. One panel is open at a time;
 * Escape and the close button take it away, running the mount cleanup. */
export function ExtensionPanelHost({
  panel,
  onClose,
}: {
  panel: RuntimePanel;
  onClose: () => void;
}) {
  const body = useRef<HTMLDivElement>(null);
  const [error, setError] = useState("");
  const { t } = useT();
  useEscapeCloses(true, onClose);
  useEffect(() => {
    const el = body.current;
    if (!el) return;
    setError("");
    let cleanup: void | (() => void);
    try {
      cleanup = panel.mount(el, panel.api);
    } catch (e) {
      setError(String(e));
    }
    return () => {
      try {
        cleanup?.();
      } catch (e) {
        console.error(e);
      }
      el.replaceChildren();
    };
  }, [panel.key, panel.mount, panel.api]);
  return (
    <PanePortal>
      <div
        className={`extension-panel extension-panel--${panel.placement}`}
        data-testid="extension-panel"
        role="dialog"
        aria-label={panel.title}
      >
        {panel.placement === "full" ? (
          <div
            className="extension-panel-scrim"
            onClick={onClose}
            aria-hidden
          />
        ) : null}
        <div className="extension-panel-box">
          <header>
            <strong>{panel.title}</strong>
            <button type="button" className="settings-btn" onClick={onClose}>
              {t("extensions.close")}
            </button>
          </header>
          {error ? <p role="alert">{error}</p> : null}
          <div ref={body} className="extension-panel-body" />
        </div>
      </div>
    </PanePortal>
  );
}
