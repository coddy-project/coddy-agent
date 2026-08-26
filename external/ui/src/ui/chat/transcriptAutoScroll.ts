import type { TranscriptItem } from "./types";

type PlanDocumentItem = Extract<TranscriptItem, { type: "plan_document" }>;

/** Plan fields that change layout chrome only; must not trigger stick-to-bottom scroll. */
function planDocumentScrollNeutral(
  it: PlanDocumentItem,
): Omit<PlanDocumentItem, "expanded" | "discarded"> & {
  discarded?: never;
  expanded?: never;
} {
  const { expanded: _e, discarded: _d, ...rest } = it;
  return rest;
}

function itemAffectsAutoScroll(
  prev: TranscriptItem,
  next: TranscriptItem,
): boolean {
  if (prev === next) {
    return false;
  }
  if (prev.type !== next.type || prev.id !== next.id) {
    return true;
  }
  if (prev.type === "plan_document" && next.type === "plan_document") {
    return (
      JSON.stringify(planDocumentScrollNeutral(prev)) !==
      JSON.stringify(planDocumentScrollNeutral(next))
    );
  }
  return JSON.stringify(prev) !== JSON.stringify(next);
}

/** True when transcript change should run stick-to-bottom scroll (new tokens, rows, etc.). */
export function transcriptItemsAffectAutoScroll(
  prev: TranscriptItem[] | undefined,
  next: TranscriptItem[],
): boolean {
  if (!prev) {
    return next.length > 0;
  }
  if (prev === next) {
    return false;
  }
  if (prev.length !== next.length) {
    return true;
  }
  for (let i = 0; i < next.length; i++) {
    const a = prev[i];
    const b = next[i];
    if (!a || !b) {
      return true;
    }
    if (itemAffectsAutoScroll(a, b)) {
      return true;
    }
  }
  return false;
}
