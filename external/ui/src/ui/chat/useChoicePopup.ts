import { useContext, useId, useLayoutEffect, useRef, useState } from "react";
import type { FocusEvent, KeyboardEvent, MouseEvent } from "react";
import { PaneHidden } from "../components/PanePortal";
import { useEscapeCloses } from "../components/useEscapeCloses";

type PopupRole = "menu" | "dialog";
type Opening<K> = {
  kind: K;
  anchor: HTMLElement;
  rect: DOMRect;
  origin: HTMLElement;
  last: boolean;
};

/** Present one choice in a plain menu or in a filtered listbox. */
export function choiceProps(
  selected: boolean,
  listbox = false,
  className = "mode-item",
) {
  return {
    className: `${className}${selected ? " is-selected" : ""}`,
    role: listbox ? "option" : "menuitemradio",
    "aria-selected": listbox ? selected : undefined,
    "aria-checked": listbox ? undefined : selected,
    "data-choice-row": "",
    tabIndex: -1,
  } as const;
}

function focusChoice(element: HTMLElement, panel: HTMLElement) {
  element.focus({ preventScroll: true });
  // Reveal the row in the list or sheet without scrolling the chat behind it.
  for (
    let parent = element.parentElement;
    parent && panel.contains(parent);
    parent = parent.parentElement
  ) {
    if (parent.scrollHeight > parent.clientHeight) {
      const row = element.getBoundingClientRect();
      const top = parent.getBoundingClientRect().top + parent.clientTop;
      const bottom = top + parent.clientHeight;
      if (row.top < top) parent.scrollTop += row.top - top;
      else if (row.bottom > bottom) parent.scrollTop += row.bottom - bottom;
    }
  }
}

/** Focus and keyboard contract for the composer's portalled choice popups. */
export function useChoicePopup<K extends string>({
  onOpen,
  onClose,
  fallbackFocus,
  findTrigger,
}: {
  onOpen?: (kind: K) => void;
  onClose?: () => void;
  fallbackFocus?: () => HTMLElement | null;
  findTrigger?: (kind: K) => HTMLElement | null;
} = {}) {
  const id = useId();
  const hidden = useContext(PaneHidden);
  const [opening, setOpening] = useState<Opening<K> | null>(null);
  const active = useRef<Opening<K> | null>(null);
  const restore = useRef<HTMLElement | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  const lastPanelRole = useRef<string | null>(null);

  function close(returnFocus = true) {
    if (!active.current) return;
    restore.current = returnFocus ? active.current.origin : null;
    active.current = null;
    setOpening(null);
    onClose?.();
  }

  function toggle(kind: K, anchor: HTMLElement, origin = anchor, last = false) {
    if (active.current?.kind === kind) {
      close();
      return;
    }
    const next = {
      kind,
      anchor,
      rect: anchor.getBoundingClientRect(),
      origin,
      last,
    };
    restore.current = null;
    active.current = next;
    setOpening(next);
    onOpen?.(kind);
  }

  useEscapeCloses(opening !== null, close);

  useLayoutEffect(() => {
    const el = panel.current;
    if (!opening || !el) {
      lastPanelRole.current = null;
      const target = restore.current;
      restore.current = null;
      if (target) {
        const destination = target.isConnected ? target : fallbackFocus?.();
        destination?.focus({ preventScroll: true });
      }
      return;
    }
    if (hidden) {
      close(false);
      return;
    }
    if (!opening.anchor.isConnected) {
      // An extension can move the trigger across composer rows and remount it.
      const replacement = findTrigger?.(opening.kind);
      if (replacement?.isConnected) {
        const next = {
          ...opening,
          anchor: replacement,
          rect: replacement.getBoundingClientRect(),
          origin:
            opening.origin === opening.anchor ? replacement : opening.origin,
        };
        active.current = next;
        setOpening(next);
      } else {
        close();
      }
      return;
    }
    const role = el.getAttribute("role");
    const changedRole = role !== lastPanelRole.current;
    lastPanelRole.current = role;
    // A live options refresh can remove the focused row. Recover to the
    // filter (or a remaining row) instead of leaving focus on the document.
    if (changedRole || !el.contains(document.activeElement)) {
      const rows = el.querySelectorAll<HTMLElement>("[data-choice-row]");
      const filter = el.querySelector<HTMLElement>("[data-choice-filter]");
      const row = rows[opening.last ? rows.length - 1 : 0];
      focusChoice(filter ?? row ?? el, el);
    }
  });

  function triggerProps(kind: K, role: PopupRole = "menu") {
    const isOpen = opening?.kind === kind;
    return {
      "aria-haspopup": role,
      "aria-expanded": isOpen,
      "aria-controls": isOpen ? id : undefined,
      onClick: (event: MouseEvent<HTMLButtonElement>) =>
        toggle(kind, event.currentTarget),
      onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => {
        if (
          event.defaultPrevented ||
          event.altKey ||
          event.ctrlKey ||
          event.metaKey ||
          event.shiftKey ||
          (event.key !== "ArrowDown" && event.key !== "ArrowUp")
        )
          return;
        event.preventDefault();
        toggle(
          kind,
          event.currentTarget,
          event.currentTarget,
          event.key === "ArrowUp",
        );
      },
    };
  }

  function popupProps(role: PopupRole, label: string, enterFromFilter = false) {
    return {
      id,
      ref: panel,
      role,
      "aria-label": label,
      tabIndex: -1,
      onBlur: (event: FocusEvent<HTMLDivElement>) => {
        if (
          event.relatedTarget &&
          !event.currentTarget.contains(event.relatedTarget)
        )
          close(false);
      },
      onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
        if (
          event.defaultPrevented ||
          event.nativeEvent.isComposing ||
          event.keyCode === 229 ||
          event.altKey ||
          event.ctrlKey ||
          event.metaKey
        )
          return;
        const el = event.currentTarget;
        const target = event.target as HTMLElement;
        const rows = Array.from(
          el.querySelectorAll<HTMLButtonElement>("[data-choice-row]"),
        );
        const index = rows.findIndex((row) => row.contains(target));
        const filter = el.querySelector<HTMLElement>("[data-choice-filter]");
        const actions = Array.from(
          el.querySelectorAll<HTMLButtonElement>("[data-choice-action]"),
        );
        if (event.key === "Tab") {
          // Each result list is one stop, between the filter and actions.
          const list = el.querySelector<HTMLElement>('[role="listbox"]');
          const stops = [filter, list, ...actions].filter(
            (stop) => stop !== null,
          );
          const current = stops.findIndex((stop) => stop.contains(target));
          const next = stops[current + (event.shiftKey ? -1 : 1)];
          if (next && current >= 0) {
            event.preventDefault();
            focusChoice(next, el);
            return;
          }
          // The portal is at the end of body. Start the browser's Tab from
          // the origin so the next stop is its real neighbor, not the portal's.
          const origin = active.current?.origin;
          close(false);
          if (origin?.isConnected) origin.focus({ preventScroll: true });
          return;
        }
        if (event.shiftKey) return;
        if (target === filter && event.key === "Enter" && enterFromFilter) {
          event.preventDefault();
          rows[0]?.click();
          return;
        }
        if (index >= 0 && event.key === "Enter") {
          event.preventDefault();
          rows[index]?.click();
          return;
        }
        if (index === -1 && target !== filter) return;
        let next: HTMLButtonElement | undefined;
        if (event.key === "ArrowDown") next = rows[(index + 1) % rows.length];
        else if (event.key === "ArrowUp")
          next = rows[index < 1 ? rows.length - 1 : index - 1];
        else if (index >= 0 && event.key === "Home") next = rows[0];
        else if (index >= 0 && event.key === "End")
          next = rows[rows.length - 1];
        if (next) {
          event.preventDefault();
          focusChoice(next, el);
        }
      },
    };
  }

  function listProps(label: string, count: number) {
    if (!count) return {};
    return {
      id: `${id}-list`,
      role: "listbox",
      "aria-label": label,
      tabIndex: 0,
      onFocus: (event: FocusEvent<HTMLDivElement>) => {
        const list = event.currentTarget;
        if (event.target !== list) return;
        const row =
          list.querySelector<HTMLElement>('[aria-selected="true"]') ??
          list.querySelector<HTMLElement>("[data-choice-row]");
        if (row && panel.current) focusChoice(row, panel.current);
      },
    };
  }

  return {
    opening,
    close,
    toggle,
    triggerProps,
    popupProps,
    listProps,
    filterProps: (count: number) => ({
      "data-choice-filter": "",
      "aria-controls": count ? `${id}-list` : undefined,
    }),
  };
}
