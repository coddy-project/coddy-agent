/**
 * Eviction policy for the per-session shadow transcript cache
 * (`streamShadowBySidRef` in App.tsx). Every opened session leaves a full
 * `TranscriptItem[]` copy in that cache; without eviction a long-lived tab
 * accumulates every dialog ever visited. The cache is kept as a small LRU:
 * the viewed session and any session with a live composer stream are pinned,
 * everything else beyond the cap is dropped and re-fetched on revisit.
 */

/** How many non-pinned shadow transcripts to keep (most recently used). */
export const SHADOW_TRANSCRIPT_CACHE_CAP = 3;

/** Moves `sid` to the most-recent end of the LRU order, in place. */
export function touchLruSid(order: string[], sid: string): void {
  const key = sid.trim();
  if (!key) return;
  const at = order.indexOf(key);
  if (at !== -1) order.splice(at, 1);
  order.push(key);
}

/**
 * Returns the session ids to evict from the shadow cache. Never returns the
 * viewed session or a session with an active stream; among the rest, the
 * least recently used entries beyond `cap` are evicted.
 */
export function sidsToEvict(p: {
  /** LRU order of cached sids, most-recent-last. */
  cachedSids: string[];
  viewedSid: string;
  activeStreamSids: Set<string>;
  cap: number;
}): string[] {
  const viewed = p.viewedSid.trim();
  const evictable: string[] = [];
  for (const sid of p.cachedSids) {
    if (!sid) continue;
    if (sid === viewed) continue;
    if (p.activeStreamSids.has(sid)) continue;
    evictable.push(sid);
  }
  const over = evictable.length - Math.max(0, p.cap);
  return over > 0 ? evictable.slice(0, over) : [];
}

/**
 * Map-like store of shadow transcripts that tracks LRU order on every write,
 * so a single `set` is the only way an entry becomes cached and `evict` can
 * never miss an entry written through a side path. Keys are trimmed session
 * ids; blank keys are ignored.
 */
export class ShadowTranscriptCache<T> {
  private readonly entries = new Map<string, T>();
  /** LRU order of cached sids, most-recent-last. */
  private order: string[] = [];

  get size(): number {
    return this.entries.size;
  }

  has(sid: string): boolean {
    return this.entries.has(sid.trim());
  }

  get(sid: string): T | undefined {
    return this.entries.get(sid.trim());
  }

  /** Stores `value` and marks `sid` as most recently used. */
  set(sid: string, value: T): void {
    const key = sid.trim();
    if (!key) return;
    this.entries.set(key, value);
    touchLruSid(this.order, key);
  }

  /** Marks `sid` as most recently used without changing its value. */
  touch(sid: string): void {
    const key = sid.trim();
    if (!key || !this.entries.has(key)) return;
    touchLruSid(this.order, key);
  }

  delete(sid: string): boolean {
    const key = sid.trim();
    const at = this.order.indexOf(key);
    if (at !== -1) this.order.splice(at, 1);
    return this.entries.delete(key);
  }

  /**
   * Moves the entry stored under `from` to `to` (a draft session id becoming
   * the server-issued one), keeping its LRU position. A missing source leaves
   * the cache untouched.
   */
  rename(from: string, to: string): void {
    const src = from.trim();
    const dst = to.trim();
    if (!src || !dst || src === dst) return;
    const value = this.entries.get(src);
    if (value === undefined) return;
    this.delete(src);
    this.set(dst, value);
  }

  /** Cached sids from least to most recently used. */
  keys(): string[] {
    return [...this.order];
  }

  /**
   * Drops least recently used entries beyond `cap`, never touching the viewed
   * session or sessions in `activeStreamSids`. Returns the evicted sids so the
   * caller can release state kept alongside (relay cursors and the like).
   */
  evict(p: {
    viewedSid: string;
    activeStreamSids: Set<string>;
    cap?: number;
  }): string[] {
    const victims = sidsToEvict({
      cachedSids: this.order,
      viewedSid: p.viewedSid,
      activeStreamSids: p.activeStreamSids,
      cap: p.cap ?? SHADOW_TRANSCRIPT_CACHE_CAP,
    });
    for (const sid of victims) this.delete(sid);
    return victims;
  }
}
