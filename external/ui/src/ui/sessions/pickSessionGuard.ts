import { isClientDraftSessionId } from "./draftSessions";

/**
 * Returns true when picking `pickId` from the History sidebar is a no-op because
 * that session is already active. Drafts are never considered redundant picks.
 *
 * Used by App.pickSession to prevent setting sessionLoading=true with no
 * corresponding load sequence (same sessionId → useEffect does not re-run →
 * loading state never cleared → infinite skeleton).
 */
export function isRedundantSessionPick(
  pickId: string,
  currentSessionId: string,
): boolean {
  if (isClientDraftSessionId(pickId)) return false;
  const pid = pickId.trim();
  const cid = currentSessionId.trim();
  return !!pid && pid === cid;
}
