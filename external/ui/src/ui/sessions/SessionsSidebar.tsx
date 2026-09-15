import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useT } from "../i18n/I18nProvider";
import { appNavHrefDraft, appNavHrefSession } from "../scheduler/hashRoute";
import { isClientDraftSessionId } from "./draftSessions";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
import {
  groupSessions,
  type SessionGroup,
  type SessionGroupMode,
} from "./sessionGroups";
import {
  SessionsFilterMenu,
  type SessionsEnvironmentOption,
} from "./SessionsFilterMenu";
import type { SessionArchiveFilter, SessionSortKey } from "./sessionQuery";
import {
  sessionRowShowsPermissionPending,
  sessionRowShowsQuestionPending,
  sessionRowShowsSpinner,
  sessionRowShowsUnreadDot,
} from "./sessionRowActivity";
import type { SessionRow } from "./types";

function pickFromSessionRowClick(
  ev: MouseEvent<HTMLDivElement>,
  action: () => void,
): void {
  if (ev.defaultPrevented || ev.button !== 0) {
    return;
  }
  if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) {
    return;
  }
  action();
}

/** Sliders: everything that decides what the list below shows. */
function IconFilters() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      aria-hidden
    >
      <path d="M4 7h10" />
      <path d="M18 7h2" />
      <path d="M4 17h4" />
      <path d="M12 17h8" />
      <circle cx="16" cy="7" r="2" />
      <circle cx="10" cy="17" r="2" />
    </svg>
  );
}

/** The archive tray: puts a conversation aside, or takes it back out. */
function IconArchiveRow(props: { out?: boolean }) {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M3 7h18v3H3z" />
      <path d="M5 10v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-9" />
      {props.out ? <path d="M12 18v-5" /> : <path d="M12 13v5" />}
      {props.out ? (
        <path d="M9.5 15.5L12 13l2.5 2.5" />
      ) : (
        <path d="M9.5 15.5L12 18l2.5-2.5" />
      )}
    </svg>
  );
}

export function SessionsSidebar(props: {
  sessionId: string;
  /** Session ids with an unresolved permission_prompt in the composer. */
  permissionPendingSessionIds?: ReadonlySet<string>;
  /** Session ids with an unresolved question_prompt in the composer. */
  questionPendingSessionIds?: ReadonlySet<string>;
  sessions: SessionRow[];
  error?: string | null;
  open?: boolean;
  /** Extra classes on the root aside (e.g. offset when Scheduler is docked). */
  className?: string;
  onClose?: () => void;
  onPick: (id: string) => void;
  onTitleSave?: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  /** Puts a conversation in the archive, or takes it back out. */
  onArchive?: (id: string, archived: boolean) => void;
  /** How the list is divided into headings; "none" keeps it flat. */
  groupMode?: SessionGroupMode;
  onGroupModeChange?: (mode: SessionGroupMode) => void;
  /** Which side of the archive the shell fetched. */
  archiveFilter?: SessionArchiveFilter;
  onArchiveFilterChange?: (value: SessionArchiveFilter) => void;
  /** What the shell asked the server to order the listing by. */
  sortKey?: SessionSortKey;
  onSortKeyChange?: (key: SessionSortKey) => void;
  /** Local plus every configured remote, for the environment section. */
  environments?: SessionsEnvironmentOption[];
  /** Starts a new chat already pointed at a folder, from its group heading. */
  onNewChatInWorkspace?: (cwd: string) => void;
  searchDraft: string;
  onSearchDraftChange: (v: string) => void;
  onSearchClear: () => void;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
  /** The moment the age buckets are measured against; injected by tests. */
  now?: number;
}) {
  const { t } = useT();
  const listRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const isOpen = !!props.open;
  const permissionPending =
    props.permissionPendingSessionIds ?? new Set<string>();
  const questionPending = props.questionPendingSessionIds ?? new Set<string>();
  const groupMode: SessionGroupMode = props.groupMode ?? "none";
  const { onArchive, onNewChatInWorkspace } = props;
  const [filtersOpen, setFiltersOpen] = useState(false);
  const archiveFilter: SessionArchiveFilter = props.archiveFilter ?? "exclude";
  const sortKey: SessionSortKey = props.sortKey ?? "updated";

  // Collapsed headings are keyed by group, so a group that comes and goes with
  // a search keeps the state the operator gave it while it is on screen.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());

  const groups = useMemo(
    () => groupSessions(props.sessions, groupMode, props.now),
    [props.sessions, groupMode, props.now],
  );

  useEffect(() => {
    const root = listRef.current;
    const sent = sentinelRef.current;
    if (!isOpen || !root || !sent || !props.hasMore || props.loadingMore) {
      return;
    }
    const io = new IntersectionObserver(
      (entries) => {
        const hit = entries.some((x) => x.isIntersecting);
        if (hit && props.hasMore && !props.loadingMore) {
          props.onLoadMore();
        }
      },
      { root, rootMargin: "48px", threshold: 0 },
    );
    io.observe(sent);
    return () => io.disconnect();
  }, [
    isOpen,
    props.hasMore,
    props.loadingMore,
    props.sessions.length,
    props.onLoadMore,
  ]);

  if (!isOpen) {
    return null;
  }

  const groupLabel = (group: SessionGroup): string =>
    group.labelKey ? t(group.labelKey) : String(group.label ?? "");

  const renderRow = (s: SessionRow) => (
    <div
      key={s.id}
      className={`session-item ${s.id === props.sessionId ? "active" : ""}`}
      data-testid={`session-row-${s.id}`}
      onClick={(ev) =>
        pickFromSessionRowClick(ev, () => {
          props.onPick(s.id);
        })
      }
    >
      <a
        href={
          isClientDraftSessionId(s.id)
            ? appNavHrefDraft(s.id)
            : appNavHrefSession(s.id)
        }
        className="session-row-link"
        onClick={(ev) => {
          ev.stopPropagation();
          sameTabInAppNavClick(ev, () => {
            props.onPick(s.id);
          });
        }}
      >
        <div className="session-row-leading">
          {sessionRowShowsSpinner(
            s,
            props.sessionId,
            permissionPending,
            questionPending,
          ) ? (
            <span
              className="session-activity-spinner"
              aria-hidden
              data-testid={`session-spinner-${s.id}`}
            />
          ) : null}
          {sessionRowShowsPermissionPending(s, permissionPending) ? (
            <span
              className="session-permission-icon"
              aria-label={t("sessions.permissionRequired")}
              data-testid={`session-permission-${s.id}`}
              title={t("sessions.permissionRequired")}
            >
              ?
            </span>
          ) : null}
          {sessionRowShowsQuestionPending(s, questionPending) ? (
            <span
              className="session-question-icon"
              aria-label={t("sessions.questionPending")}
              data-testid={`session-question-${s.id}`}
              title={t("sessions.questionPending")}
            >
              ?
            </span>
          ) : null}
          {sessionRowShowsUnreadDot(s, props.sessionId) ? (
            <span
              className="session-unread-dot"
              aria-label={t("sessions.unreadCompletion")}
              data-testid={`session-unread-${s.id}`}
            />
          ) : null}
          <span
            className="session-title"
            title={s.title || t("sessions.newChatFallback")}
          >
            {s.title || t("sessions.newChatFallback")}
          </span>
          {s.archived ? (
            <span
              className="session-archived-badge"
              data-testid={`session-archived-${s.id}`}
              title={t("sessions.archivedBadge")}
            >
              {t("sessions.archivedBadge")}
            </span>
          ) : null}
        </div>
        {/* The tags sit under the title rather than beside it: the title is
            what the row is for, and a long one must not be pushed out of view
            by labels. Grouping by tag is how they are navigated. */}
        {(s.tags ?? []).length > 0 ? (
          <div
            className="session-row-tags"
            data-testid={`session-tags-${s.id}`}
          >
            {(s.tags ?? []).map((tag) => (
              <span className="session-tag" key={tag}>
                {tag}
              </span>
            ))}
          </div>
        ) : null}
      </a>
      {onArchive ? (
        <button
          className="session-trash session-archive"
          type="button"
          aria-label={
            s.archived ? t("sessions.unarchive") : t("sessions.archive")
          }
          title={s.archived ? t("sessions.unarchive") : t("sessions.archive")}
          data-testid={`session-archive-${s.id}`}
          onClick={(ev) => {
            ev.preventDefault();
            ev.stopPropagation();
            onArchive(s.id, !s.archived);
          }}
        >
          <IconArchiveRow out={!!s.archived} />
        </button>
      ) : null}
      <button
        className="session-trash"
        type="button"
        aria-label={t("sessions.deleteConversation")}
        title={t("sessions.delete")}
        data-testid={`session-delete-${s.id}`}
        onClick={(ev) => {
          ev.preventDefault();
          ev.stopPropagation();
          void props.onDelete(s.id);
        }}
      >
        🗑
      </button>
    </div>
  );

  return (
    <aside
      className={["sessions", "drawer", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label={t("sessions.history")}
      data-testid="sessions"
      data-variant="drawer"
    >
      <div className="sessions-head">
        <span>{t("sessions.history")}</span>
        <button
          type="button"
          className="sessions-close"
          aria-label={t("sessions.closeHistory")}
          data-testid="sessions-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="sessions-search-row">
        <input
          type="search"
          className="sessions-search-input"
          placeholder={t("sessions.searchPlaceholder")}
          value={props.searchDraft}
          onChange={(ev) => props.onSearchDraftChange(ev.target.value)}
          aria-label={t("sessions.searchAriaLabel")}
          data-testid="sessions-search"
        />
        {props.searchDraft.trim() ? (
          <button
            type="button"
            className="sessions-search-clear"
            aria-label={t("sessions.clearSearch")}
            data-testid="sessions-search-clear"
            onClick={props.onSearchClear}
          >
            ×
          </button>
        ) : null}
        {/* The filters sit with the search, not up in the head beside the
            close button: both narrow the list below, and the close button
            does something else entirely. */}
        <button
          type="button"
          className={`sessions-filter-trigger${filtersOpen ? " is-open" : ""}`}
          aria-label={t("sessions.filter.menu")}
          title={t("sessions.filter.menu")}
          aria-haspopup="menu"
          aria-expanded={filtersOpen}
          data-testid="sessions-filter-trigger"
          onClick={() => setFiltersOpen((prev) => !prev)}
        >
          <IconFilters />
        </button>
        <SessionsFilterMenu
          open={filtersOpen}
          onClose={() => setFiltersOpen(false)}
          archiveFilter={archiveFilter}
          onArchiveFilterChange={(value) =>
            props.onArchiveFilterChange?.(value)
          }
          groupMode={groupMode}
          onGroupModeChange={(mode) => props.onGroupModeChange?.(mode)}
          sortKey={sortKey}
          onSortKeyChange={(key) => props.onSortKeyChange?.(key)}
          {...(props.environments ? { environments: props.environments } : {})}
        />
      </div>

      <div className="session-list" id="session-list" ref={listRef}>
        {props.error ? (
          <div className="sessions-empty" data-testid="sessions-error">
            {props.error}
          </div>
        ) : null}
        {!props.error && props.sessions.length === 0 ? (
          <div className="sessions-empty" data-testid="sessions-empty">
            {t("sessions.empty")}
          </div>
        ) : null}
        {groupMode === "none"
          ? props.sessions.map(renderRow)
          : groups.map((group) => {
              const isCollapsed = collapsed.has(group.key);
              const label = groupLabel(group);
              return (
                <div
                  className="session-group"
                  key={group.key}
                  data-testid={`session-group-${group.key}`}
                >
                  <div className="session-group-bar">
                    <button
                      type="button"
                      className="session-group-head"
                      data-testid={`session-group-toggle-${group.key}`}
                      aria-expanded={!isCollapsed}
                      title={
                        isCollapsed
                          ? t("sessions.group.expand", { group: label })
                          : t("sessions.group.collapse", { group: label })
                      }
                      onClick={() =>
                        setCollapsed((prev) => {
                          const next = new Set(prev);
                          if (next.has(group.key)) {
                            next.delete(group.key);
                          } else {
                            next.add(group.key);
                          }
                          return next;
                        })
                      }
                    >
                      <span className="session-group-caret" aria-hidden>
                        {isCollapsed ? "▸" : "▾"}
                      </span>
                      <span className="session-group-label">{label}</span>
                      <span className="session-group-count">
                        {group.rows.length}
                      </span>
                    </button>
                    {/* A folder heading is also where a conversation about that
                      folder starts: the plus opens a new chat already pointed
                      at the workspace, which resolves its own git branch. */}
                    {group.workspacePath && onNewChatInWorkspace ? (
                      <button
                        type="button"
                        className="session-group-new"
                        data-testid={`session-group-new-${group.key}`}
                        title={t("sessions.group.newChatHere", {
                          folder: label,
                        })}
                        aria-label={t("sessions.group.newChatHere", {
                          folder: label,
                        })}
                        onClick={() =>
                          onNewChatInWorkspace(String(group.workspacePath))
                        }
                      >
                        +
                      </button>
                    ) : null}
                  </div>
                  {isCollapsed ? null : group.rows.map(renderRow)}
                </div>
              );
            })}
        <div
          ref={sentinelRef}
          className="sessions-scroll-sentinel"
          aria-hidden
        />
        {props.loadingMore ? (
          <div
            className="sessions-loading-more"
            data-testid="sessions-loading-more"
          >
            {t("sessions.loadingMore")}
          </div>
        ) : null}
      </div>
    </aside>
  );
}
