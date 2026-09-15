import { useEffect, useRef } from "react";
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
 * The sections are flat rather than nested submenus: a drawer is narrow, and a
 * hover-to-open submenu in it is a worse target than one more line of text.
 */
export function SessionsFilterMenu(props: {
  open: boolean;
  onClose: () => void;
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
  const ref = useRef<HTMLDivElement>(null);
  const { open, onClose } = props;

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, onClose]);

  useEffect(() => {
    if (open) {
      ref.current?.focus();
    }
  }, [open]);

  if (!open) {
    return null;
  }

  const environments = props.environments ?? [];

  return (
    <>
      <div className="sessions-filter-backdrop" onClick={onClose} />
      <div
        className="sessions-filter-menu"
        data-testid="sessions-filter-menu"
        role="menu"
        tabIndex={-1}
        ref={ref}
      >
        <div className="sessions-filter-section">
          <div className="sessions-filter-section-label">
            {t("sessions.filter.status")}
          </div>
          {SESSION_ARCHIVE_FILTERS.map((value) => (
            <button
              key={value}
              type="button"
              className="sessions-filter-item"
              role="menuitemradio"
              aria-checked={props.archiveFilter === value}
              data-testid={`sessions-filter-status-${value}`}
              onClick={() => {
                props.onArchiveFilterChange(value);
                onClose();
              }}
            >
              <span>{t(`sessions.filter.status.${value}`)}</span>
              {props.archiveFilter === value ? <Check /> : null}
            </button>
          ))}
        </div>

        {environments.length > 1 ? (
          <div className="sessions-filter-section">
            <div className="sessions-filter-section-label">
              {t("sessions.filter.environment")}
            </div>
            {environments.map((env) => (
              <button
                key={env.key}
                type="button"
                className="sessions-filter-item"
                role="menuitemradio"
                aria-checked={env.active}
                data-testid={`sessions-filter-env-${env.key}`}
                onClick={() => {
                  env.onPick();
                  onClose();
                }}
              >
                <span>{env.label}</span>
                {env.active ? <Check /> : null}
              </button>
            ))}
          </div>
        ) : null}

        <div className="sessions-filter-section">
          <div className="sessions-filter-section-label">
            {t("sessions.filter.groupBy")}
          </div>
          {SESSION_GROUP_MODES.map((mode) => (
            <button
              key={mode}
              type="button"
              className="sessions-filter-item"
              role="menuitemradio"
              aria-checked={props.groupMode === mode}
              data-testid={`sessions-filter-group-${mode}`}
              onClick={() => {
                props.onGroupModeChange(mode);
                onClose();
              }}
            >
              <span>{t(`sessions.group.${mode}`)}</span>
              {props.groupMode === mode ? <Check /> : null}
            </button>
          ))}
        </div>

        <div className="sessions-filter-section">
          <div className="sessions-filter-section-label">
            {t("sessions.filter.sortBy")}
          </div>
          {HISTORY_SORT_KEYS.map((key) => (
            <button
              key={key}
              type="button"
              className="sessions-filter-item"
              role="menuitemradio"
              aria-checked={props.sortKey === key}
              data-testid={`sessions-filter-sort-${key}`}
              onClick={() => {
                props.onSortKeyChange(key);
                onClose();
              }}
            >
              <span>{t(`sessions.sort.${key}`)}</span>
              {props.sortKey === key ? <Check /> : null}
            </button>
          ))}
        </div>
      </div>
    </>
  );
}
