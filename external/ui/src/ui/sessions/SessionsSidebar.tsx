import { useEffect, useRef, type MouseEvent } from "react";
import { appNavHrefDraft, appNavHrefSession } from "../scheduler/hashRoute";
import { isClientDraftSessionId } from "./draftSessions";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
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
  searchDraft: string;
  onSearchDraftChange: (v: string) => void;
  onSearchClear: () => void;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
}) {
  const listRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const isOpen = !!props.open;
  const permissionPending =
    props.permissionPendingSessionIds ?? new Set<string>();
  const questionPending = props.questionPendingSessionIds ?? new Set<string>();

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

  return (
    <aside
      className={["sessions", "drawer", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label="History"
      data-testid="sessions"
      data-variant="drawer"
    >
      <div className="sessions-head">
        <span>History</span>
        <button
          type="button"
          className="sessions-close"
          aria-label="Close history"
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
          placeholder="Search by title or first message"
          value={props.searchDraft}
          onChange={(ev) => props.onSearchDraftChange(ev.target.value)}
          aria-label="Search history by title or first user message"
          data-testid="sessions-search"
        />
        {props.searchDraft.trim() ? (
          <button
            type="button"
            className="sessions-search-clear"
            aria-label="Clear search"
            data-testid="sessions-search-clear"
            onClick={props.onSearchClear}
          >
            ×
          </button>
        ) : null}
      </div>

      <div className="session-list" id="session-list" ref={listRef}>
        {props.error ? (
          <div className="sessions-empty" data-testid="sessions-error">
            {props.error}
          </div>
        ) : null}
        {!props.error && props.sessions.length === 0 ? (
          <div className="sessions-empty" data-testid="sessions-empty">
            No history yet
          </div>
        ) : null}
        {props.sessions.map((s) => (
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
                    aria-label="Permission required"
                    data-testid={`session-permission-${s.id}`}
                    title="Permission required"
                  >
                    ?
                  </span>
                ) : null}
                {sessionRowShowsQuestionPending(s, questionPending) ? (
                  <span
                    className="session-question-icon"
                    aria-label="Question pending"
                    data-testid={`session-question-${s.id}`}
                    title="Question pending"
                  >
                    ?
                  </span>
                ) : null}
                {sessionRowShowsUnreadDot(s, props.sessionId) ? (
                  <span
                    className="session-unread-dot"
                    aria-label="Unread completion"
                    data-testid={`session-unread-${s.id}`}
                  />
                ) : null}
                <span className="session-title" title={s.title || "New chat"}>
                  {s.title || "New chat"}
                </span>
              </div>
            </a>
            <button
              className="session-trash"
              type="button"
              aria-label="Delete conversation"
              title="Delete"
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
        ))}
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
            Loading...
          </div>
        ) : null}
      </div>
    </aside>
  );
}
