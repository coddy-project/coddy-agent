import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { IconMore } from "../files/windowIcons";
import { useEscapeCloses } from "../components/useEscapeCloses";

const MENU_WIDTH = 190;
const MENU_HEIGHT = 90;

export function FileActionsMenu({
  path,
  busy,
  onDiscard,
  onDelete,
}: {
  path: string;
  busy: boolean;
  onDiscard?: (() => void) | undefined;
  onDelete?: (() => void) | undefined;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [placement, setPlacement] = useState({ top: 0, left: 0, above: false });
  const host = useRef<HTMLSpanElement>(null);
  const menu = useRef<HTMLSpanElement>(null);
  useEscapeCloses(open, () => setOpen(false));

  useEffect(() => {
    if (!open) return undefined;
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!host.current?.contains(target) && !menu.current?.contains(target)) {
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", onPointer);
    return () => document.removeEventListener("pointerdown", onPointer);
  }, [open]);

  if (!onDiscard && !onDelete) return null;
  const label = t("changes.viewer.fileActions");
  const choose = (action: (() => void) | undefined) => {
    setOpen(false);
    action?.();
  };
  const toggle = () => {
    if (!open && host.current) {
      const rect = host.current.getBoundingClientRect();
      const above =
        rect.bottom + MENU_HEIGHT + 6 > window.innerHeight &&
        rect.top >= MENU_HEIGHT + 6;
      setPlacement({
        top: above ? rect.top - 6 : rect.bottom + 6,
        left: Math.max(
          MENU_WIDTH + 12,
          Math.min(rect.right, window.innerWidth - 12),
        ),
        above,
      });
    }
    setOpen((value) => !value);
  };
  return (
    <span className="dv-file-menu-host" ref={host}>
      <button
        type="button"
        className="dv-icon-btn"
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid={`dv-file-actions-${path}`}
        onClick={toggle}
      >
        <IconMore />
      </button>
      {open
        ? createPortal(
            <span
              ref={menu}
              className="dv-file-menu dv-file-menu--portal"
              role="menu"
              aria-label={label}
              style={{
                top: placement.top,
                left: placement.left,
                transform: `translateX(-100%)${placement.above ? " translateY(-100%)" : ""}`,
              }}
            >
              {onDiscard ? (
                <button
                  type="button"
                  role="menuitem"
                  disabled={busy}
                  data-testid={`dv-discard-${path}`}
                  onClick={() => choose(onDiscard)}
                >
                  {t("changes.viewer.discardChanges")}
                </button>
              ) : null}
              {onDelete ? (
                <button
                  type="button"
                  role="menuitem"
                  className="dv-file-menu-danger"
                  disabled={busy}
                  data-testid={`dv-delete-${path}`}
                  onClick={() => choose(onDelete)}
                >
                  {t("changes.viewer.deleteFile")}
                </button>
              ) : null}
            </span>,
            document.body,
          )
        : null}
    </span>
  );
}
