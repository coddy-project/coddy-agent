import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import {
  DEFAULT_SESSION_GROUP_MODE,
  SESSION_GROUP_MODES,
  type SessionGroupMode,
} from "./sessionGroups";
import {
  DEFAULT_ARCHIVE_FILTER,
  DEFAULT_SESSION_SORT_KEY,
  SESSION_ARCHIVE_FILTERS,
  type SessionArchiveFilter,
  type SessionSortKey,
} from "./sessionQuery";
import { Chevron } from "../components/Chevron";

/** One origin filter or server switch offered by History's Environment menu. */
export type SessionsEnvironmentOption = {
  kind: "origin" | "switch";
  /** Stable id for the row; an origin id or the remote URL. */
  key: string;
  label: string;
  active: boolean;
  onPick: () => void;
};

/** The columns History offers; the table in Settings sorts by more than these. */
const HISTORY_SORT_KEYS: readonly SessionSortKey[] = [
  "updated",
  "created",
  "title",
];

/** One choice inside a section. */
type MenuOption = {
  kind?: "origin" | "switch";
  key: string;
  label: string;
  active: boolean;
  testId: string;
  onPick: () => void;
  startsGroup?: boolean;
};

/** One row of the menu: a question, the answer in force, and the choices. */
type MenuSection = {
  key: string;
  label: string;
  /** The answer currently in force, drawn on the row. */
  value: string;
  /**
   * Whether that answer is the default one. Only a value the operator moved
   * away from is worth the accent colour: if every row were coloured, the
   * colour would say nothing, and what the menu is for is seeing at a glance
   * what has been narrowed.
   */
  isDefault: boolean;
  options: MenuOption[];
  /** A rule is drawn above a section that starts a new group of questions. */
  startsGroup?: boolean;
};

/** Gap between a row and the list of its choices opened beside it. */
const SUBMENU_GAP = 4;
/** Room the menu and its lists keep from the edges of the window. */
const WINDOW_MARGIN = 8;
/** Gap between the trigger and the menu hung under it. */
const MENU_OFFSET = 6;

/**
 * Where the choices of an open section go: beside the menu on the right (the
 * drawer sits at the left of the window, so that side normally has the room),
 * beside it on the left near the right edge of the window, or folded out under
 * their row, inside the menu, when neither side has the room - a phone, where
 * the drawer is the width of the screen and the trigger sits at its right edge.
 */
export type FilterSubmenuPlacement = "right" | "left" | "inline";

/**
 * Picks the side a list of choices `width` pixels wide fits on, next to a row
 * spanning `row.left`..`row.right` in a window `viewport` pixels wide.
 */
export function placeFilterSubmenu(
  row: { left: number; right: number },
  width: number,
  viewport: number,
): FilterSubmenuPlacement {
  if (row.right + SUBMENU_GAP + width <= viewport - WINDOW_MARGIN) {
    return "right";
  }
  if (row.left - SUBMENU_GAP - width >= WINDOW_MARGIN) {
    return "left";
  }
  return "inline";
}

function Check() {
  return (
    <span className="sessions-filter-check" aria-hidden>
      ✓
    </span>
  );
}

/**
 * Everything that decides *what the History shows*, behind one control: which
 * server it reads, which side of the archive, how the rows are divided into
 * headings, and what they are ordered by.
 *
 * The menu is four rows deep, not four lists long: each row names its question
 * and the answer in force, and the choices open beside it. A drawer is 320px
 * wide, so the panel is rendered into the document rather than into the drawer,
 * which clips what overflows it.
 */
export function SessionsFilterMenu(props: {
  open: boolean;
  onClose: () => void;
  /** The trigger's rectangle; the menu hangs under it. */
  anchor?: DOMRect | null;
  archiveFilter: SessionArchiveFilter;
  onArchiveFilterChange: (value: SessionArchiveFilter) => void;
  groupMode: SessionGroupMode;
  onGroupModeChange: (mode: SessionGroupMode) => void;
  sortKey: SessionSortKey;
  onSortKeyChange: (key: SessionSortKey) => void;
  /** Local plus every configured remote; omitted when there is only this one. */
  environments?: SessionsEnvironmentOption[];
}) {
  const { t } = useT();
  const { open, onClose } = props;
  const [openSection, setOpenSection] = useState<string | null>(null);
  // The side the open section's choices were measured to fit on. Once a list
  // had to fold out inline, every section does until the menu closes: the
  // window is too narrow for a list beside the menu, and switching between
  // the two kinds of list would move the rows under the reader's finger.
  const [placement, setPlacement] = useState<{
    section: string;
    side: FilterSubmenuPlacement;
  } | null>(null);
  const submenuRef = useRef<HTMLDivElement | null>(null);
  // The section a mouse hover has just opened. The click that follows the
  // same pointer onto its row lands on a section that is already open, and
  // must not fold it straight back; a later click, or a key press on the row,
  // is a deliberate one.
  const hoverOpenedRef = useRef<string | null>(null);
  const inline = placement?.side === "inline";
  const side: FilterSubmenuPlacement | null = inline
    ? "inline"
    : placement && placement.section === openSection
      ? placement.side
      : null;

  useEffect(() => {
    if (!open) {
      setOpenSection(null);
      setPlacement(null);
      hoverOpenedRef.current = null;
    }
  }, [open]);

  // A list that is not placed yet is drawn beside the menu for one layout
  // pass, which is how wide it wants to be, and placed before the browser
  // paints it: its width depends on the labels of the locale and on the names
  // of the remotes, so no fixed guess fits every list.
  useLayoutEffect(() => {
    if (!openSection || side !== null) {
      return;
    }
    const submenu = submenuRef.current;
    const row = submenu?.parentElement;
    if (!submenu || !row) {
      return;
    }
    setPlacement({
      section: openSection,
      side: placeFilterSubmenu(
        row.getBoundingClientRect(),
        submenu.getBoundingClientRect().width,
        window.innerWidth,
      ),
    });
  }, [openSection, side]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") {
        return;
      }
      ev.stopPropagation();
      // A folded section first, the menu second: escape undoes one step of what
      // opening did, the way it does in every other menu. The section is read
      // from the render, not from a state updater: an updater runs while React
      // renders, and closing the menu from there updates the drawer mid-render.
      if (openSection) {
        setOpenSection(null);
        return;
      }
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, onClose, openSection]);

  if (!open) {
    return null;
  }

  const environments = props.environments ?? [];
  const pick = (apply: () => void) => () => {
    apply();
    onClose();
  };

  const sections: MenuSection[] = [
    {
      key: "status",
      label: t("sessions.filter.status"),
      value: t(`sessions.filter.status.${props.archiveFilter}`),
      isDefault: props.archiveFilter === DEFAULT_ARCHIVE_FILTER,
      options: SESSION_ARCHIVE_FILTERS.map((value) => ({
        key: value,
        label: t(`sessions.filter.status.${value}`),
        active: props.archiveFilter === value,
        testId: `sessions-filter-status-${value}`,
        onPick: pick(() => props.onArchiveFilterChange(value)),
      })),
    },
  ];
  if (environments.length > 1) {
    const activeOrigin = environments.find(
      (environment) => environment.kind === "origin" && environment.active,
    );
    const activeSwitch = environments.find(
      (environment) => environment.kind === "switch" && environment.active,
    );
    const firstSwitch = environments.find(
      (environment) => environment.kind === "switch",
    );
    const originIsDefault = activeOrigin?.key === "all";
    const value = activeSwitch
      ? originIsDefault || !activeOrigin
        ? activeSwitch.label
        : `${activeSwitch.label} · ${activeOrigin.label}`
      : activeOrigin?.label ?? "";
    sections.push({
      key: "environment",
      label: t("sessions.filter.environment"),
      value,
      isDefault: !activeSwitch && originIsDefault,
      options: environments.map((env) => ({
        kind: env.kind,
        key: env.key,
        label: env.label,
        active: env.active,
        testId: `sessions-filter-env-${env.key}`,
        onPick: pick(env.onPick),
        startsGroup: env === firstSwitch,
      })),
    });
  }
  sections.push(
    {
      key: "group",
      label: t("sessions.filter.groupBy"),
      value: t(`sessions.group.${props.groupMode}`),
      isDefault: props.groupMode === DEFAULT_SESSION_GROUP_MODE,
      startsGroup: true,
      options: SESSION_GROUP_MODES.map((mode) => ({
        key: mode,
        label: t(`sessions.group.${mode}`),
        active: props.groupMode === mode,
        testId: `sessions-filter-group-${mode}`,
        onPick: pick(() => props.onGroupModeChange(mode)),
      })),
    },
    {
      key: "sort",
      label: t("sessions.filter.sortBy"),
      value: t(`sessions.sort.${props.sortKey}`),
      isDefault: props.sortKey === DEFAULT_SESSION_SORT_KEY,
      options: HISTORY_SORT_KEYS.map((key) => ({
        key,
        label: t(`sessions.sort.${key}`),
        active: props.sortKey === key,
        testId: `sessions-filter-sort-${key}`,
        onPick: pick(() => props.onSortKeyChange(key)),
      })),
    },
  );

  const anchor = props.anchor ?? null;
  const top = anchor ? anchor.bottom + MENU_OFFSET : 0;

  const menu = (
    <>
      <button
        type="button"
        className="sessions-filter-backdrop"
        aria-hidden="true"
        tabIndex={-1}
        onMouseDown={(ev) => {
          ev.preventDefault();
          onClose();
        }}
      />
      <div
        className={`sessions-filter-menu${inline ? " has-inline-submenu" : ""}`}
        data-testid="sessions-filter-menu"
        role="menu"
        style={
          anchor
            ? {
                top,
                right: window.innerWidth - anchor.right,
                // Hung from the trigger's right edge, the menu grows to the
                // left: never past the window's left edge.
                maxWidth: anchor.right - WINDOW_MARGIN,
                // With the choices folded in, the menu is as tall as the
                // longest list; it scrolls rather than run off the screen.
                ...(inline
                  ? { maxHeight: window.innerHeight - top - WINDOW_MARGIN }
                  : {}),
              }
            : undefined
        }
      >
        {sections.map((section) => {
          const expanded = openSection === section.key;
          return (
            <div
              className={`sessions-filter-parent${section.startsGroup ? " starts-group" : ""}`}
              key={section.key}
              onPointerEnter={(ev) => {
                // Only a mouse hovers. A touch opens a row by its tap: iOS
                // drops the click of a tap whose emulated hover shows new
                // content, so opening on that hover would cost a tap. Folded
                // inline, a hover that opened another section would move the
                // rows under the pointer; a click switches instead.
                if (ev.pointerType !== "mouse") {
                  // A device with a mouse and a touchscreen: the tap that
                  // follows the mouse's hover is the reader's own click.
                  hoverOpenedRef.current = null;
                  return;
                }
                if (!inline && openSection !== section.key) {
                  hoverOpenedRef.current = section.key;
                  setOpenSection(section.key);
                }
              }}
            >
              <button
                type="button"
                className={`sessions-filter-row${expanded ? " is-open" : ""}`}
                role="menuitem"
                aria-haspopup="menu"
                aria-expanded={expanded}
                data-testid={`sessions-filter-section-${section.key}`}
                onPointerLeave={() => {
                  if (hoverOpenedRef.current === section.key) {
                    hoverOpenedRef.current = null;
                  }
                }}
                onClick={(ev) => {
                  const hoverOpened = hoverOpenedRef.current === section.key;
                  hoverOpenedRef.current = null;
                  // A key press clicks with detail 0, a pointer with its count.
                  if (hoverOpened && ev.detail !== 0) {
                    setOpenSection(section.key);
                    return;
                  }
                  setOpenSection(expanded ? null : section.key);
                }}
              >
                <span className="sessions-filter-label">{section.label}</span>
                <span
                  className={`sessions-filter-value${section.isDefault ? " is-default" : ""}`}
                >
                  {section.value}
                </span>
                {/* Beside the menu the list opens the way the chevron points;
                    folded out under the row it is a disclosure, turned down. */}
                <Chevron
                  className="sessions-filter-chevron"
                  open={expanded && side === "inline"}
                />
              </button>
              {expanded ? (
                <div
                  ref={submenuRef}
                  className={`sessions-filter-submenu${
                    side === "left"
                      ? " opens-left"
                      : side === "inline"
                        ? " opens-inline"
                        : ""
                  }`}
                  data-testid={`sessions-filter-submenu-${section.key}`}
                  role="menu"
                >
                  {section.options.map((option) => (
                    <button
                      key={option.key}
                      type="button"
                      className={`sessions-filter-item${option.startsGroup ? " starts-group" : ""}`}
                      role={option.kind === "switch" ? "menuitem" : "menuitemradio"}
                      {...(option.kind === "switch"
                        ? option.active
                          ? { "aria-current": "true" as const }
                          : {}
                        : { "aria-checked": option.active })}
                      data-testid={option.testId}
                      onClick={option.onPick}
                    >
                      <span>{option.label}</span>
                      {option.active ? <Check /> : null}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
    </>
  );

  return typeof document === "undefined"
    ? menu
    : createPortal(menu, document.body);
}
