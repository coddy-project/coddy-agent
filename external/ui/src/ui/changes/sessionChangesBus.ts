/**
 * The server's word that a session's folder may have moved: a turn of it ended
 * (`event: turn_ended`) or its changes were discarded (`event:
 * session_changes`), both on `GET /coddy/events`. App forwards them here and
 * the working copy store (workingCopy.ts) reads the folder again.
 *
 * Module-level pub/sub in the shape of `skills/fileMentionBus.ts`.
 */

type SettledListener = (sessionId: string) => void;

const settledListeners = new Set<SettledListener>();

/** Publishes that a session's folder may have moved. */
export function emitChangesSettled(sessionId: string): void {
  for (const cb of [...settledListeners]) {
    try {
      cb(sessionId);
    } catch {
      // A broken listener must not block the others.
    }
  }
}

/** Subscribes to that word. Returns unsubscribe. */
export function onChangesSettled(cb: SettledListener): () => void {
  settledListeners.add(cb);
  return () => {
    settledListeners.delete(cb);
  };
}

/** Drops every listener (tests only). */
export function resetChangesBusForTests(): void {
  settledListeners.clear();
}
