import { transcriptItemsLooselyEqual } from "./transcriptServerSnapshot";
import type { TranscriptItem } from "./types";

/** Stable id for a tool_call row. */
export function stableToolCallItemId(toolCallId: string): string {
  return `tc_${toolCallId.trim()}`;
}

export function stablePermissionPromptItemId(toolCallId: string): string {
  return `pp_${toolCallId.trim()}`;
}

export function stableQuestionPromptItemId(requestId: string): string {
  return `qp_${requestId.trim()}`;
}

export function stableThinkingItemId(
  userTurnIndex: number,
  indexInTurn: number,
): string {
  return `th_${userTurnIndex}_${indexInTurn}`;
}

export function stableAssistantItemId(
  userTurnIndex: number,
  indexInTurn = 0,
): string {
  return indexInTurn === 0
    ? `as_${userTurnIndex}`
    : `as_${userTurnIndex}_${indexInTurn}`;
}

export function stableUserItemId(userTurnIndex: number): string {
  return `u_${userTurnIndex}`;
}

/** Stable id for the wake a woken turn opens with, in the slot of its turn. */
export function stableWakeItemId(userTurnIndex: number): string {
  return `wake_${userTurnIndex}`;
}

/**
 * Ids for the reasoning and answer rows of a turn a page opens in the middle
 * of: how many of each the turn had before the page is not known there, so
 * they are named after their message's index in the history. The older page
 * holding the start of the turn numbers its own rows `th_<turn>_<k>`, so the
 * two never collide once both are on screen.
 */
export function partialTurnThinkingItemId(
  userTurnIndex: number,
  messageIndex: number,
): string {
  return `th_${userTurnIndex}_m${messageIndex}`;
}

export function partialTurnAssistantItemId(
  userTurnIndex: number,
  messageIndex: number,
): string {
  return `as_${userTurnIndex}_m${messageIndex}`;
}

/** Stable id for a compaction summary row: its message's index in the history. */
export function stableCompactionItemId(messageIndex: number): string {
  return `cmp_m${messageIndex}`;
}

/** Stable id for a plan snapshot row: its message's index in the history. */
export function stablePlanDocumentItemId(messageIndex: number): string {
  return `pd_m${messageIndex}`;
}

/**
 * After rebuilding transcript from the server, reuse React keys (and plan expanded)
 * from the previous in-memory list when rows describe the same step.
 */
export function preserveTranscriptItemIds(
  merged: TranscriptItem[],
  previous: TranscriptItem[] | undefined,
): TranscriptItem[] {
  if (!previous?.length) {
    return merged;
  }
  let pi = 0;
  const out: TranscriptItem[] = [];
  for (const row of merged) {
    let matched = false;
    for (let j = pi; j < previous.length; j++) {
      const prev = previous[j];
      if (!prev || !transcriptItemsLooselyEqual(row, prev)) {
        continue;
      }
      pi = j + 1;
      matched = true;
      if (row.type === "plan_document" && prev.type === "plan_document") {
        out.push({ ...row, id: prev.id, expanded: prev.expanded });
      } else {
        out.push({ ...row, id: prev.id });
      }
      break;
    }
    if (!matched) {
      out.push(row);
    }
  }
  return out;
}
