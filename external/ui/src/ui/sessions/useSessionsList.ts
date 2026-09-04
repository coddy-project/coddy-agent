import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { fetchJSON } from "../coddyApi";
import { useT } from "../i18n/I18nProvider";
import type { SessionRow } from "./types";

/**
 * The sessions-history list: cursor-paged rows, the search filter with its
 * debounce, and the load-more bookkeeping. `setSessions` is exposed because
 * title saves, deletes, and the title-suggest stream (which stay in App)
 * patch rows in place.
 */
export function useSessionsList({
  headers,
}: {
  headers: Record<string, string>;
}): {
  sessions: SessionRow[];
  setSessions: Dispatch<SetStateAction<SessionRow[]>>;
  sessionsError: string | null;
  loadSessionsList: (reset: boolean) => Promise<SessionRow[] | null>;
  sessionFilterDraft: string;
  setSessionFilterDraft: Dispatch<SetStateAction<string>>;
  sessionFilterQ: string;
  sessionsHasMore: boolean;
  sessionsLoadingMore: boolean;
} {
  const { t } = useT();
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const sessionsCursorRef = useRef<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [sessionFilterDraft, setSessionFilterDraft] = useState("");
  const [sessionFilterQ, setSessionFilterQ] = useState("");
  const [sessionsHasMore, setSessionsHasMore] = useState(false);
  const [sessionsLoadingMore, setSessionsLoadingMore] = useState(false);
  const sessionsHasMoreRef = useRef(false);
  const sessionsLoadingMoreRef = useRef(false);

  useEffect(() => {
    const t = window.setTimeout(
      () => setSessionFilterQ(sessionFilterDraft.trim()),
      300,
    );
    return () => window.clearTimeout(t);
  }, [sessionFilterDraft]);

  useEffect(() => {
    sessionsCursorRef.current = sessionsCursor;
  }, [sessionsCursor]);

  useEffect(() => {
    sessionsHasMoreRef.current = sessionsHasMore;
  }, [sessionsHasMore]);

  useEffect(() => {
    sessionsLoadingMoreRef.current = sessionsLoadingMore;
  }, [sessionsLoadingMore]);

  const loadSessionsList = useCallback(
    async (reset: boolean): Promise<SessionRow[] | null> => {
      if (reset) {
        sessionsCursorRef.current = null;
        setSessionsCursor(null);
      } else if (
        !sessionsHasMoreRef.current ||
        sessionsLoadingMoreRef.current
      ) {
        return null;
      }
      if (!reset) {
        sessionsLoadingMoreRef.current = true;
        setSessionsLoadingMore(true);
      }
      const ps = new URLSearchParams();
      ps.set("limit", "30");
      if (!reset) {
        const cur = sessionsCursorRef.current;
        if (cur) {
          ps.set("cursor", cur);
        }
      }
      if (sessionFilterQ) {
        ps.set("q", sessionFilterQ);
      }
      ps.set("include_activity", "true");
      const res = await fetchJSON<{
        sessions: SessionRow[];
        nextCursor?: string | null;
        hasMore?: boolean;
      }>(`/coddy/sessions?${ps.toString()}`, {
        headers,
      });
      if (!reset) {
        sessionsLoadingMoreRef.current = false;
        setSessionsLoadingMore(false);
      }
      if (!res.ok || !res.data) {
        setSessionsError(t("app.backendUnavailable", { status: res.status }));
        return null;
      }
      setSessionsError(null);
      const next = res.data.sessions || [];
      setSessions((prev) => {
        if (reset) {
          return next;
        }
        const seen = new Set(prev.map((s) => s.id));
        return [...prev, ...next.filter((s) => !seen.has(s.id))];
      });
      const nextCur = res.data.nextCursor ?? null;
      setSessionsCursor(nextCur);
      sessionsCursorRef.current = nextCur;
      const hm = !!res.data.hasMore;
      setSessionsHasMore(hm);
      sessionsHasMoreRef.current = hm;
      return next;
    },
    [sessionFilterQ, headers, t],
  );

  return {
    sessions,
    setSessions,
    sessionsError,
    loadSessionsList,
    sessionFilterDraft,
    setSessionFilterDraft,
    sessionFilterQ,
    sessionsHasMore,
    sessionsLoadingMore,
  };
}
