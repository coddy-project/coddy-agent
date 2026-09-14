/**
 * Ordering rules for the session message queue.
 *
 * The same change reaches this client down two connections — the turn's own
 * stream and the server-wide event stream — plus the answer to whichever
 * request made it. They can arrive in any order, so the queue is never
 * assembled from deltas: every delivery carries the whole list and a version
 * that counts the changes of that session's queue, and the client keeps the
 * highest version it has applied.
 *
 * Kept out of `App.tsx` so the rule can be tested on its own.
 */

/** One follow-up waiting for the running turn to read it. */
export type QueuedMessageRow = { id: string; text: string; createdAt?: string };

/** The highest version applied per session id. */
export type QueueVersions = Map<string, number>;

/**
 * Decide whether a delivery should be rendered, and record it when it should.
 *
 * A version of 0 means the sender had none to offer (an older server, a local
 * reset); it is always applied and never raises the high-water mark, so it
 * cannot silence the versioned deliveries that follow.
 */
export function acceptQueueVersion(
  versions: QueueVersions,
  sessionId: string,
  version: number,
): boolean {
  const key = sessionId.trim();
  if (!key) return false;
  const seen = versions.get(key) ?? 0;
  const v = Number.isFinite(version) && version > 0 ? version : 0;
  if (v > 0 && v < seen) return false;
  if (v > seen) versions.set(key, v);
  return true;
}

/**
 * Forget what was applied for a session.
 *
 * A turn that is starting has an empty queue, and a server that restarted
 * numbers its changes from the beginning again: a client still holding the old
 * high-water mark would drop every frame of the new turn as stale.
 */
export function resetQueueVersion(
  versions: QueueVersions,
  sessionId: string,
): void {
  const key = sessionId.trim();
  if (key) versions.delete(key);
}
