import { useEffect, useState } from "react";
import { fetchSessionChanges } from "./api";
import { onChangesSettled } from "./sessionChangesBus";

/**
 * Whether the session has edits: the chat header shows its Edits button only
 * then. Read on its own, apart from the changed-files card, which reads
 * nothing while it is hidden (Ctrl+S, a turn still settling): once when the
 * chat opens, and again whenever the server says the session's set settled or
 * moved (`session_changes` - the end of a turn, an Undo). A failed read keeps
 * what was known: a restarting server must not hide the button.
 */
export function useSessionHasEdits(sessionId: string, enabled: boolean): boolean {
  const [has, setHas] = useState(false);
  const [epoch, setEpoch] = useState(0);
  const sid = sessionId.trim();

  useEffect(() => {
    setHas(false);
  }, [sid]);

  useEffect(() => {
    if (!sid || !enabled) return undefined;
    return onChangesSettled((settled) => {
      if (settled === sid) setEpoch((n) => n + 1);
    });
  }, [sid, enabled]);

  useEffect(() => {
    if (!sid || !enabled) {
      setHas(false);
      return undefined;
    }
    let cancelled = false;
    void fetchSessionChanges(sid).then((res) => {
      if (!cancelled && res.ok) setHas(res.data.totals.files > 0);
    });
    return () => {
      cancelled = true;
    };
  }, [sid, enabled, epoch]);

  return has;
}
