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
 * Only a fenced authoritative snapshot may establish a lower high-water mark
 * after a restart. A replayed turn-start event does not prove a restart.
 */
export function resetQueueVersion(
  versions: QueueVersions,
  sessionId: string,
): void {
  const key = sessionId.trim();
  if (key) versions.delete(key);
}

type QueueReadFence = { revision: number; epoch: number };

/** Orders deliveries within one server lifetime and fences local sources from
 * an earlier lifetime when a fresh REST snapshot proves versions restarted. */
export class QueueDeliveryOrder {
  private readonly versions: QueueVersions = new Map();
  private readonly fences = new Map<string, QueueReadFence>();

  capture(sessionId: string): QueueReadFence {
    return this.fences.get(sessionId.trim()) ?? { revision: 0, epoch: 0 };
  }

  accept(sessionId: string, version: number, epoch?: number): boolean {
    const key = sessionId.trim();
    const current = this.capture(key);
    if (!key || (epoch !== undefined && epoch !== current.epoch)) return false;
    // Even a lower frame may belong to a restarted server. It cannot reset the
    // mark, but it does make a snapshot already in flight too old to reset it.
    this.fences.set(key, { ...current, revision: current.revision + 1 });
    return acceptQueueVersion(this.versions, key, version);
  }

  acceptSnapshot(
    sessionId: string,
    version: number,
    fence: QueueReadFence,
  ): boolean {
    const key = sessionId.trim();
    const current = this.capture(key);
    if (
      !key ||
      fence.revision !== current.revision ||
      fence.epoch !== current.epoch
    )
      return false;
    if (
      Number.isFinite(version) &&
      version >= 0 &&
      version < (this.versions.get(key) ?? 0)
    ) {
      resetQueueVersion(this.versions, key);
      this.fences.set(key, { ...current, epoch: current.epoch + 1 });
    }
    return this.accept(key, version);
  }
}
