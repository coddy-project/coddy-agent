/**
 * Eviction policy for the per-session shadow transcript cache
 * (`streamShadowBySidRef` in App.tsx). Every opened session leaves a full
 * `TranscriptItem[]` copy in that Map; without eviction a long-lived tab
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
