/**
 * Resolves the most recently active leaf session in a branch tree.
 *
 * When a user opens a session URL, this function walks the branch tree and
 * returns the session ID that was most recently updated (based on lastUpdatedAt
 * timestamps from the branches endpoint).  It follows the greedy "pick the
 * most recently updated session at each branch point" strategy, which is
 * correct for the common case of linear or shallow branching.
 */

export type BranchSessionRefMinimal = {
  sessionId: string;
  lastUpdatedAt?: number;
};

export type BranchPointMinimal = {
  sessions: BranchSessionRefMinimal[];
  /** True when this session introduced the branch point (its own children). False = sibling view. */
  own?: boolean;
};

export type BranchDataMinimal = {
  branchPoints?: BranchPointMinimal[];
};

type FetchBranches = (sessionId: string) => Promise<BranchDataMinimal | null>;

/**
 * Walk the branch tree from `startId`, always moving to the session with the
 * highest `lastUpdatedAt` at each step, until no newer session is found.
 *
 * Returns the session ID of the most recently active leaf.
 */
export async function resolveLatestLeaf(
  startId: string,
  fetchBranches: FetchBranches,
  maxHops = 10,
): Promise<string> {
  let current = startId;
  let previous = "";
  const visited = new Set<string>();

  for (let hop = 0; hop < maxHops; hop++) {
    if (visited.has(current)) break;
    visited.add(current);

    let data: BranchDataMinimal | null = null;
    try {
      data = await fetchBranches(current);
    } catch {
      data = null;
    }

    if (!data) {
      // The session we hopped to cannot be read (deleted bundle still listed in a
      // stale branch file). Step back rather than handing out a dead id.
      if (previous) current = previous;
      break;
    }
    previous = current;

    if (!data.branchPoints?.length) break;

    // Among all sessions in all branch points, pick the one with the highest
    // lastUpdatedAt that we haven't visited yet.
    let bestId = current;
    let bestTime = -1;

    for (const bp of data.branchPoints) {
      // Only follow own branch points (children). Skip sibling views.
      if (bp.own === false) continue;
      for (const sess of bp.sessions) {
        if (visited.has(sess.sessionId)) continue;
        const t = sess.lastUpdatedAt ?? 0;
        if (t > bestTime) {
          bestTime = t;
          bestId = sess.sessionId;
        }
      }
    }

    if (bestId === current) break;
    current = bestId;
  }

  return current;
}
