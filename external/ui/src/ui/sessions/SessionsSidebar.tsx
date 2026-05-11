import { useEffect, useRef } from "react";
import type { SessionRow } from "./types";

export function SessionsSidebar(props: {
  sessionId: string;
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
            onClick={() => {
              props.onPick(s.id);
            }}
          >
            <div className="session-row">
              <span className="session-title" title={s.title || "New chat"}>
                {s.title || "New chat"}
              </span>
              <button
                className="session-trash"
                type="button"
                aria-label="Delete conversation"
                title="Delete"
                data-testid={`session-delete-${s.id}`}
                onMouseDown={(ev) => ev.stopPropagation()}
                onClick={(ev) => {
                  ev.stopPropagation();
                  void props.onDelete(s.id);
                }}
              >
                🗑
              </button>
            </div>
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
