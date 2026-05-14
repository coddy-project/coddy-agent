import type { TranscriptItem } from "./types";

/**
 * Picks the transcript array to pass as `prev` into a stream mutation for `mutationSessionId`.
 * When the user switches sessions, `viewedSessionIdRef` updates synchronously but React
 * `items` (and itemsRef) can still hold the previous session for a frame. Prefer the
 * per-session shadow whenever it exists and is non-empty, or when this session has an
 * active composer POST/relay, so background streams never merge into the wrong base.
 * `assumeActiveForBase` covers the first user row of a new POST before addActiveComposer runs.
 */
export function pickStreamMutationBase(p: {
  mutationSessionId: string;
  viewingSid: string;
  shadow: TranscriptItem[] | undefined;
  hasActiveComposer: boolean;
  itemsWhenViewingMatches: TranscriptItem[];
  assumeActiveForBase?: boolean;
}): TranscriptItem[] {
  const assume = p.assumeActiveForBase === true;
  const {
    mutationSessionId,
    viewingSid,
    shadow,
    hasActiveComposer,
    itemsWhenViewingMatches,
  } = p;
  if (
    shadow !== undefined &&
    (shadow.length > 0 || hasActiveComposer || assume)
  ) {
    return shadow.slice();
  }
  if (assume && shadow === undefined) {
    return [];
  }
  if (viewingSid === mutationSessionId) {
    return itemsWhenViewingMatches.slice();
  }
  return [];
}
