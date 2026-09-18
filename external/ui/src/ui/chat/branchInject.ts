import type { TranscriptItem } from "./types";

export type BranchPointData = {
  userMessageIndex: number;
  currentIndex: number;
  total: number;
  sessions: Array<{ sessionId: string; preview?: string }>;
  own?: boolean;
};

/** Keep only the last branch_nav per userMessageIndex, removing any duplicates. */
export function deduplicateBranchNavs(items: TranscriptItem[]): TranscriptItem[] {
  const lastNavIdx = new Map<number, number>();
  for (let i = 0; i < items.length; i++) {
    const it = items[i]!;
    if (it.type === "branch_nav") {
      lastNavIdx.set(it.userMessageIndex, i);
    }
  }
  if (lastNavIdx.size === 0) return items;
  return items.filter((it, i) => {
    if (it.type !== "branch_nav") return true;
    return lastNavIdx.get(it.userMessageIndex) === i;
  });
}

export function injectBranchNavItems(
  items: TranscriptItem[],
  branchPoints: BranchPointData[],
): TranscriptItem[] {
  if (!branchPoints.length) return items;
  const byUserIdx = new Map<number, BranchPointData>();
  for (const bp of branchPoints) {
    byUserIdx.set(bp.userMessageIndex, bp);
  }
  const result: TranscriptItem[] = [];
  let userMsgCount = 0;
  for (const item of items) {
    result.push(item);
    // The wake a woken turn opens with is a user message on the server, so it
    // takes an index of its own; nothing branches from it.
    if (item.type === "background_wake") {
      userMsgCount++;
      continue;
    }
    if (item.type === "user_message") {
      const bp = byUserIdx.get(userMsgCount);
      if (bp) {
        result.push({
          id: `branch-nav-${bp.userMessageIndex}`,
          type: "branch_nav",
          userMessageIndex: bp.userMessageIndex,
          currentIndex: bp.currentIndex,
          total: bp.total,
          sessions: bp.sessions,
        });
      }
      userMsgCount++;
    }
  }
  return result;
}
