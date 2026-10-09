import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
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
  readCollapsedSessionGroups,
  writeCollapsedSessionGroups,
} from "./collapsedSessionGroups";
import {
  SessionsFilterMenu,
  type SessionsEnvironmentOption,
} from "./SessionsFilterMenu";
import type { SessionArchiveFilter, SessionSortKey } from "./sessionQuery";
import { pinDropIndex, reorderPins } from "./reorderPins";
import { SessionRowMenu, type SessionRowMenuItem } from "./SessionRowMenu";
import { SessionTagEditor } from "./SessionTagEditor";
import { Chevron } from "../components/Chevron";
import { tagVocabulary } from "./tagEditing";
import {
  sessionRowAttentionMarker,
  sessionRowShowsActivity,
  sessionRowShowsErrorSeen,
  sessionRowShowsErrorUnseen,
  sessionRowShowsUnreadDot,
} from "./sessionRowActivity";
import type { SessionRow } from "./types";

/** How far a mouse press travels before it is a drag of a pinned row, not a click. */
const PIN_DRAG_SLOP_PX = 4;
/** How long a finger holds a pinned row before it drags it rather than scrolls. */
const PIN_HOLD_MS = 400;

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

/** The row's own menu: everything that can be done to one conversation. */
function IconKebab() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden
    >
      <circle cx="12" cy="5" r="1.7" />
      <circle cx="12" cy="12" r="1.7" />
      <circle cx="12" cy="19" r="1.7" />
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
  /**
   * Sessions whose name a describe call is still working out: their rows
   * show a placeholder for the title and the tags instead of the first
   * message they would be listed under.
   */
  namingSessionIds?: ReadonlySet<string>;
  /** Session ids with an unresolved permission_prompt in the composer. */
  permissionPendingSessionIds?: ReadonlySet<string>;
  /** Session ids with an unresolved question_prompt in the composer. */
  questionPendingSessionIds?: ReadonlySet<string>;
  sessions: SessionRow[];
  error?: string | null;
  /**
   * What a refused change says, by the row it happened to. History is usually
   * scrolled away from the top of the list, where `error` is shown.
   */
  rowErrors?: Readonly<Record<string, string>>;
  open?: boolean;
  /** Extra classes on the root aside (e.g. offset when Scheduler is docked). */
  className?: string;
  onClose?: () => void;
  onPick: (id: string) => void;
  onTitleSave?: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  /** Puts a conversation in the archive, or takes it back out. */
  onArchive?: (id: string, archived: boolean) => void;
  /** Keeps a conversation at the top of the list, or lets it back into order. */
  onPin?: (id: string, pinned: boolean) => void;
  /**
   * Writes the labels a conversation is filed under, answering whether the
   * write landed so the editor can say when it did not.
   */
  onTagsSave?: (id: string, tags: string[]) => void | Promise<boolean>;
  /** Writes the order the operator dragged the pinned conversations into. */
  onReorderPins?: (ids: string[]) => void;
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
  const { onArchive, onPin, onTagsSave, onNewChatInWorkspace } = props;
  // Which row has its menu open, and where that row's control is. One at a
  // time: a second menu open behind the first would be two answers to one
  // question.
  const [rowMenu, setRowMenu] = useState<{ id: string; at: DOMRect } | null>(
    null,
  );
  // The row being renamed in place, and the row whose labels are open, with the
  // control the editor hangs under. Both are one at a time, like the menu they
  // are opened from.
  const [renaming, setRenaming] = useState<{
    id: string;
    draft: string;
  } | null>(null);
  // The same value in a ref, because a rename ends from two places at once: the
  // key that ended it and the blur the disappearing box fires. Whichever runs
  // first clears the ref, and the other one then has nothing to save - no
  // second request, and no Escape that writes the draft it discarded.
  const renamingRef = useRef<{ id: string; draft: string } | null>(null);
  const [tagEditor, setTagEditor] = useState<{
    id: string;
    at: DOMRect;
  } | null>(null);
  // What the last filing write was refused with. The drawer has no room for a
  // banner, and the editor is where the operator is looking anyway.
  const [tagError, setTagError] = useState<string | null>(null);
  // A pin being dragged, and where it would land. The pointer is tracked rather
  // than HTML5 drag-and-drop, which a finger cannot start.
  const [drag, setDrag] = useState<{ id: string; over: number } | null>(null);
  // Set when a drag ends: the release is also a click on the row, and that click
  // must not open the conversation that was only being moved.
  const draggedRef = useRef(false);
  const pinnedListRef = useRef<HTMLDivElement>(null);
  const [filtersOpen, setFiltersOpen] = useState(false);
  // The menu is portaled out of the drawer (which clips what overflows it), so
  // it is placed from the trigger's rectangle rather than by being inside it.
  const [filtersAnchor, setFiltersAnchor] = useState<DOMRect | null>(null);
  const filtersRef = useRef<HTMLButtonElement>(null);
  const archiveFilter: SessionArchiveFilter = props.archiveFilter ?? "exclude";
  const sortKey: SessionSortKey = props.sortKey ?? "updated";

  // Collapsed headings are keyed by group, so a group that comes and goes with
  // a search, drawer close, or route change keeps the state the operator gave it.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(
    readCollapsedSessionGroups,
  );

  const groups = useMemo(
    () => groupSessions(props.sessions, groupMode, props.now),
    [props.sessions, groupMode, props.now],
  );

  // What the label picker offers: the words this history already files under,
  // taken from the rows the drawer has loaded.
  const vocabulary = useMemo(
    () => tagVocabulary(props.sessions),
    [props.sessions],
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

  const { onReorderPins } = props;
  const pinnedIds = props.sessions.filter((s) => s.pinned).map((s) => s.id);

  /**
   * Drags one pin through the list by the row itself, so a finger can do it too:
   * HTML5 drag-and-drop never starts from touch. A mouse or a pen starts the drag
   * once the press has travelled past the slop, so a plain click still opens the
   * row; a finger has to hold the row first, so a swipe still scrolls the list.
   * The row follows nothing - the list shows where the drop would land instead,
   * which survives a scroll and costs no layer.
   */
  const startPinDrag = (id: string) => (ev: ReactPointerEvent) => {
    draggedRef.current = false;
    if (!onReorderPins || ev.button !== 0 || renamingRef.current) {
      return;
    }
    if ((ev.target as Element).closest("button, input")) {
      return;
    }
    const from = pinnedIds.indexOf(id);
    if (from < 0) {
      return;
    }
    const row = ev.currentTarget as HTMLElement;
    const { pointerId, clientX: x0, clientY: y0 } = ev;
    const touch = ev.pointerType === "touch";
    let dragging = false;

    const rowsOf = () =>
      [...(pinnedListRef.current?.querySelectorAll(".session-item") ?? [])].map(
        (el) => el.getBoundingClientRect(),
      );

    const begin = () => {
      dragging = true;
      try {
        row.setPointerCapture(pointerId);
      } catch {
        // The pointer is already gone; the release below still ends the drag.
      }
      setDrag({ id, over: from });
    };
    const holdTimer = touch ? window.setTimeout(begin, PIN_HOLD_MS) : 0;

    const onMove = (move: PointerEvent) => {
      if (move.pointerId !== pointerId) {
        return;
      }
      if (!dragging) {
        const travelled = Math.hypot(move.clientX - x0, move.clientY - y0);
        if (travelled <= PIN_DRAG_SLOP_PX) {
          return;
        }
        if (touch) {
          // Moved before the hold: the finger is scrolling, not dragging.
          finish(false);
          return;
        }
        begin();
      }
      setDrag((prev) =>
        prev ? { ...prev, over: pinDropIndex(rowsOf(), move.clientY) } : prev,
      );
    };
    // Once the hold has taken the row, the finger must not also pan the list.
    const holdScroll = (touchEv: TouchEvent) => {
      if (dragging) {
        touchEv.preventDefault();
      }
    };
    // A held row is not a link to open in a new tab.
    const holdMenu = (menuEv: Event) => {
      if (dragging) {
        menuEv.preventDefault();
      }
    };
    const onUp = (up: PointerEvent) => {
      if (up.pointerId === pointerId) {
        finish(up.type === "pointerup");
      }
    };
    function finish(commit: boolean) {
      window.clearTimeout(holdTimer);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      document.removeEventListener("touchmove", holdScroll);
      document.removeEventListener("contextmenu", holdMenu);
      if (!dragging) {
        return;
      }
      draggedRef.current = true;
      setDrag((prev) => {
        if (commit && prev && prev.over !== from) {
          onReorderPins?.(reorderPins(pinnedIds, from, prev.over));
        }
        return null;
      });
    }
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
    document.addEventListener("touchmove", holdScroll, { passive: false });
    document.addEventListener("contextmenu", holdMenu);
  };

  /** Swallows the click a drag ends with, which would open the moved row. */
  const swallowDragClick = (ev: MouseEvent) => {
    if (!draggedRef.current) {
      return false;
    }
    draggedRef.current = false;
    ev.preventDefault();
    ev.stopPropagation();
    return true;
  };

  /** Opens, moves and closes the inline rename, ref and state together. */
  const setRenamingBoth = (next: { id: string; draft: string } | null) => {
    renamingRef.current = next;
    setRenaming(next);
  };

  /**
   * Ends an inline rename, at most once. An empty box and a title that did not
   * move both mean "never mind": the row goes back to what it was without a
   * request.
   */
  const commitRename = () => {
    const current = renamingRef.current;
    setRenamingBoth(null);
    if (!current) {
      return;
    }
    const next = current.draft.trim();
    const row = props.sessions.find((x) => x.id === current.id);
    if (next && next !== (row?.title ?? "")) {
      props.onTitleSave?.(current.id, next);
    }
  };

  const groupLabel = (group: SessionGroup): string =>
    group.labelKey ? t(group.labelKey) : String(group.label ?? "");

  const renderRow = (s: SessionRow, pinnedIndex = -1) => {
    const naming = props.namingSessionIds?.has(s.id) === true;
    const showsActivity = sessionRowShowsActivity(
      s,
      permissionPending,
      questionPending,
    );
    // The dot already says work is going; the label says which kind, so a row
    // busy only with detached tasks does not read as a turn still answering.
    const activityLabel = t(
      s.turnActive ? "sessions.turnRunning" : "sessions.backgroundRunning",
    );
    const attention = sessionRowAttentionMarker(
      s,
      permissionPending,
      questionPending,
    );
    const showsPermission = attention === "permission";
    const showsQuestion = attention === "question";
    const showsUnread = sessionRowShowsUnreadDot(s, props.sessionId);
    const showsErrorUnseen = sessionRowShowsErrorUnseen(s, props.sessionId);
    const showsErrorSeen = sessionRowShowsErrorSeen(s, props.sessionId);
    return (
      <div
        key={s.id}
        className={[
          "session-item",
          s.id === props.sessionId ? "active" : "",
          s.archived ? "is-archived" : "",
          props.rowErrors?.[s.id] ? "has-row-error" : "",
          pinnedIndex >= 0 && onReorderPins ? "is-reorderable" : "",
          drag?.id === s.id ? "is-dragging" : "",
          drag && pinnedIndex >= 0 && drag.over === pinnedIndex
            ? "is-drop-target"
            : "",
        ]
          .filter(Boolean)
          .join(" ")}
        data-testid={`session-row-${s.id}`}
        onPointerDown={
          pinnedIndex >= 0 && onReorderPins ? startPinDrag(s.id) : undefined
        }
        onClick={(ev) => {
          if (swallowDragClick(ev)) {
            return;
          }
          pickFromSessionRowClick(ev, () => {
            props.onPick(s.id);
          });
        }}
      >
        {renaming?.id === s.id ? (
          <input
            className="session-title-input"
            autoFocus
            value={renaming.draft}
            aria-label={t("sessions.rename")}
            data-testid={`session-rename-${s.id}`}
            // Selected on open, the way a rename behaves everywhere else: typing
            // replaces the name, and the box shows its beginning rather than the
            // tail a caret at the end would scroll it to.
            onFocus={(ev) => ev.currentTarget.select()}
            onClick={(ev) => ev.stopPropagation()}
            onChange={(ev) =>
              setRenamingBoth({ id: s.id, draft: ev.target.value })
            }
            onBlur={() => commitRename()}
            onKeyDown={(ev) => {
              if (ev.key === "Enter") {
                ev.preventDefault();
                commitRename();
              }
              if (ev.key === "Escape") {
                ev.stopPropagation();
                // Clearing the ref is what makes this a cancel: the blur that
                // follows the box disappearing finds nothing left to save.
                setRenamingBoth(null);
              }
            }}
          />
        ) : (
          <a
            href={
              isClientDraftSessionId(s.id)
                ? appNavHrefDraft(s.id)
                : appNavHrefSession(s.id)
            }
            className="session-row-link"
            draggable={false}
            onClick={(ev) => {
              if (swallowDragClick(ev)) {
                return;
              }
              ev.stopPropagation();
              sameTabInAppNavClick(ev, () => {
                props.onPick(s.id);
              });
            }}
          >
            {/* The state marks take a column of their own, so the title and the
            tags under it share one left edge. A finished ring keeps grouped rows
            from blending into their headings; pending prompts own the state slot. */}
            <span className="session-row-marks">
              {showsActivity ? (
                <span
                  className="session-activity-dot"
                  role="img"
                  aria-label={activityLabel}
                  title={activityLabel}
                  data-testid={`session-activity-${s.id}`}
                />
              ) : null}
              {!showsActivity && !showsPermission && !showsQuestion ? (
                showsErrorUnseen ? (
                  <span
                    className="session-error-dot"
                    role="img"
                    aria-label={t("sessions.stateError")}
                    title={t("sessions.stateError")}
                    data-testid={`session-error-${s.id}`}
                  />
                ) : showsErrorSeen ? (
                  <span
                    className="session-error-dot is-seen"
                    role="img"
                    aria-label={t("sessions.stateError")}
                    title={t("sessions.stateError")}
                    data-testid={`session-error-${s.id}`}
                  />
                ) : showsUnread ? (
                  <span
                    className="session-unread-dot"
                    role="img"
                    aria-label={t("sessions.unreadCompletion")}
                    data-testid={`session-unread-${s.id}`}
                  />
                ) : (
                  <span
                    className="session-idle-dot"
                    role="img"
                    aria-label={t("sessions.stateFinished")}
                    title={t("sessions.stateFinished")}
                    data-testid={`session-idle-${s.id}`}
                  />
                )
              ) : null}
              {showsPermission ? (
                <span
                  className="session-permission-icon"
                  role="img"
                  aria-label={t("sessions.permissionRequired")}
                  data-testid={`session-permission-${s.id}`}
                  title={t("sessions.permissionRequired")}
                >
                  ?
                </span>
              ) : null}
              {showsQuestion ? (
                <span
                  className="session-question-icon"
                  role="img"
                  aria-label={t("sessions.questionPending")}
                  data-testid={`session-question-${s.id}`}
                  title={t("sessions.questionPending")}
                >
                  ?
                </span>
              ) : null}
              {s.archived ? (
                <span
                  className="session-archived-mark"
                  role="img"
                  data-testid={`session-archived-${s.id}`}
                  aria-label={t("sessions.archivedBadge")}
                  title={t("sessions.archivedBadge")}
                >
                  <IconArchiveRow />
                </span>
              ) : null}
            </span>
            <div className="session-row-leading">
              {naming ? (
                <span
                  className="session-title session-title--pending"
                  data-testid={`session-title-pending-${s.id}`}
                  aria-busy="true"
                  title={t("sessions.naming")}
                >
                  <span
                    className="session-title-skeleton naming-skeleton-bar"
                    aria-hidden="true"
                  />
                  <span className="sr-only">{t("sessions.naming")}</span>
                </span>
              ) : (
                <span
                  className="session-title"
                  title={s.title || t("sessions.newChatFallback")}
                >
                  {s.title || t("sessions.newChatFallback")}
                </span>
              )}
            </div>
            {/* The tags sit under the title rather than beside it: the title is
            what the row is for, and a long one must not be pushed out of view
            by labels. Grouping by tag is how they are navigated. While the
            chat is being named, the tags the same answer brings are on their
            way too. */}
            {naming ? (
              <div
                className="session-row-tags session-row-tags--pending"
                data-testid={`session-tags-pending-${s.id}`}
                aria-hidden="true"
              >
                <span className="session-tag-skeleton naming-skeleton-bar" />
                <span className="session-tag-skeleton naming-skeleton-bar" />
              </div>
            ) : (s.tags ?? []).length > 0 ? (
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
        )}
        {(() => {
          const items: SessionRowMenuItem[] = [];
          if (onPin) {
            items.push({
              key: "pin",
              label: s.pinned ? t("sessions.unpin") : t("sessions.pin"),
              testId: `session-menu-pin-${s.id}`,
              onPick: () => onPin(s.id, !s.pinned),
            });
          }
          // Renaming and filing change what the row says about itself, and stay
          // above the rule with pinning: none of them takes the conversation out
          // of the list.
          if (props.onTitleSave) {
            items.push({
              key: "rename",
              label: t("sessions.rename"),
              testId: `session-menu-rename-${s.id}`,
              onPick: () => setRenamingBoth({ id: s.id, draft: s.title || "" }),
            });
          }
          if (onTagsSave) {
            const at = rowMenu?.id === s.id ? rowMenu.at : null;
            items.push({
              key: "tags",
              label: t("sessions.tags.edit"),
              testId: `session-menu-tags-${s.id}`,
              onPick: () => {
                if (at) {
                  setTagError(null);
                  setTagEditor({ id: s.id, at });
                }
              },
            });
          }
          // Archiving and deleting both take the conversation out of the list,
          // so they stand together below the rule; pinning only moves it.
          if (onArchive) {
            items.push({
              key: "archive",
              label: s.archived
                ? t("sessions.unarchive")
                : t("sessions.archive"),
              testId: `session-menu-archive-${s.id}`,
              startsGroup: true,
              onPick: () => onArchive(s.id, !s.archived),
            });
          }
          items.push({
            key: "delete",
            label: t("sessions.delete"),
            testId: `session-menu-delete-${s.id}`,
            danger: true,
            ...(onArchive ? {} : { startsGroup: true }),
            onPick: () => void props.onDelete(s.id),
          });
          return (
            <>
              <button
                className="session-row-menu-trigger"
                type="button"
                aria-label={t("sessions.rowMenu")}
                title={t("sessions.rowMenu")}
                aria-haspopup="menu"
                aria-expanded={rowMenu?.id === s.id}
                data-testid={`session-menu-${s.id}`}
                onClick={(ev) => {
                  ev.preventDefault();
                  ev.stopPropagation();
                  const at = ev.currentTarget.getBoundingClientRect();
                  setRowMenu((prev) =>
                    prev?.id === s.id ? null : { id: s.id, at },
                  );
                }}
              >
                <IconKebab />
              </button>
              <SessionRowMenu
                open={rowMenu?.id === s.id}
                onClose={() => setRowMenu(null)}
                anchor={rowMenu?.id === s.id ? rowMenu.at : null}
                items={items}
                ariaLabel={s.title || t("sessions.newChatFallback")}
              />
              {onTagsSave ? (
                <SessionTagEditor
                  open={tagEditor?.id === s.id}
                  anchor={tagEditor?.id === s.id ? tagEditor.at : null}
                  tags={s.tags ?? []}
                  vocabulary={vocabulary}
                  onChange={(next) => {
                    setTagError(null);
                    void Promise.resolve(onTagsSave(s.id, next)).then((ok) => {
                      if (ok === false) {
                        setTagError(t("sessions.tags.failed"));
                      }
                    });
                  }}
                  {...(tagError ? { error: tagError } : {})}
                  onClose={() => {
                    setTagError(null);
                    setTagEditor(null);
                  }}
                  ariaLabel={s.title || t("sessions.newChatFallback")}
                />
              ) : null}
            </>
          );
        })()}
        {/* Beside the link rather than inside it, so the link keeps the
        conversation's name and the note is announced once, as an alert. */}
        {props.rowErrors?.[s.id] ? (
          <span
            className="session-row-error"
            role="alert"
            data-testid={`session-row-error-${s.id}`}
          >
            {props.rowErrors[s.id]}
          </span>
        ) : null}
      </div>
    );
  };

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
          ref={filtersRef}
          onClick={() => {
            setFiltersAnchor(
              filtersRef.current?.getBoundingClientRect() ?? null,
            );
            setFiltersOpen((prev) => !prev);
          }}
        >
          <IconFilters />
        </button>
        <SessionsFilterMenu
          open={filtersOpen}
          onClose={() => setFiltersOpen(false)}
          anchor={filtersAnchor}
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
        {groupMode === "none" && !props.sessions.some((s) => s.pinned)
          ? props.sessions.map((row) => renderRow(row))
          : groups.map((group) => {
              const isCollapsed = collapsed.has(group.key);
              const label = groupLabel(group);
              return (
                <div
                  className={`session-group${group.key === "pinned" ? " is-pinned" : ""}`}
                  key={group.key}
                  data-testid={`session-group-${group.key}`}
                  {...(group.key === "pinned" ? { ref: pinnedListRef } : {})}
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
                          writeCollapsedSessionGroups(next);
                          return next;
                        })
                      }
                    >
                      <span className="session-group-label">{label}</span>
                      <Chevron
                        open={!isCollapsed}
                        className="session-group-caret"
                      />
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
                  {group.subLabel ? (
                    <div
                      className="session-group-path"
                      title={group.subLabel}
                      data-testid={`session-group-path-${group.key}`}
                    >
                      {group.subLabel}
                    </div>
                  ) : null}
                  {isCollapsed
                    ? null
                    : group.key === "pinned"
                      ? group.rows.map((row, index) => renderRow(row, index))
                      : group.rows.map((row) => renderRow(row))}
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
