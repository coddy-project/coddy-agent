import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { SESSION_GROUP_MODES, type SessionGroupMode } from "./sessionGroups";
import {
  SESSION_ARCHIVE_FILTERS,
  type SessionArchiveFilter,
  type SessionSortKey,
} from "./sessionQuery";

/** One environment the History can be pointed at: this server, or a remote. */
export type SessionsEnvironmentOption = {
  /** Stable id for the row; the remote URL, or "local". */
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
  key: string;
  label: string;
  active: boolean;
  testId: string;
  onPick: () => void;
};

/** One row of the menu: a question, the answer in force, and the choices. */
type MenuSection = {
  key: string;
  label: string;
  /** The answer currently in force, drawn on the row. */
  value: string;
  options: MenuOption[];
  /** A rule is drawn above a section that starts a new group of questions. */
  startsGroup?: boolean;
};

/** Width the submenu is assumed to need when deciding which side to open on. */
const SUBMENU_WIDTH = 190;

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

  useEffect(() => {
    if (!open) {
      setOpenSection(null);
    }
  }, [open]);

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
      // opening did, the way it does in every other menu.
      setOpenSection((section) => {
        if (section) {
          return null;
        }
        onClose();
        return null;
      });
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, onClose]);

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
    sections.push({
      key: "environment",
      label: t("sessions.filter.environment"),
      value: environments.find((e) => e.active)?.label ?? "",
      options: environments.map((env) => ({
        key: env.key,
        label: env.label,
        active: env.active,
        testId: `sessions-filter-env-${env.key}`,
        onPick: pick(env.onPick),
      })),
    });
  }
  sections.push(
    {
      key: "group",
      label: t("sessions.filter.groupBy"),
      value: t(`sessions.group.${props.groupMode}`),
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
  // The drawer sits at the left of the window, so a submenu normally has room
  // on the right; near the edge it opens on the other side instead.
  const opensLeft =
    !!anchor &&
    typeof window !== "undefined" &&
    anchor.right + SUBMENU_WIDTH > window.innerWidth;

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
        className={`sessions-filter-menu${opensLeft ? " opens-left" : ""}`}
        data-testid="sessions-filter-menu"
        role="menu"
        style={
          anchor
            ? {
                top: anchor.bottom + 6,
                right: window.innerWidth - anchor.right,
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
              onMouseEnter={() => setOpenSection(section.key)}
            >
              <button
                type="button"
                className={`sessions-filter-row${expanded ? " is-open" : ""}`}
                role="menuitem"
                aria-haspopup="menu"
                aria-expanded={expanded}
                data-testid={`sessions-filter-section-${section.key}`}
                onClick={() => setOpenSection(expanded ? null : section.key)}
              >
                <span className="sessions-filter-label">{section.label}</span>
                <span className="sessions-filter-value">{section.value}</span>
                <span className="sessions-filter-chevron" aria-hidden>
                  ›
                </span>
              </button>
              {expanded ? (
                <div className="sessions-filter-submenu" role="menu">
                  {section.options.map((option) => (
                    <button
                      key={option.key}
                      type="button"
                      className="sessions-filter-item"
                      role="menuitemradio"
                      aria-checked={option.active}
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
