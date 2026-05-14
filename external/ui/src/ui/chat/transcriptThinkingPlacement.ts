import type { TranscriptItem } from "./types";

/**
 * Inserts a new in-progress thinking row so it stays above the streaming assistant
 * bubble for this turn. Reasoning deltas that resume after the first assistant content
 * chunk must not render below that bubble (chronological work log).
 */
export function insertNewThinkingBeforeStreamingAssistant(
  prev: TranscriptItem[],
  assistantMessageId: string,
  row: Extract<TranscriptItem, { type: "thinking" }>,
): TranscriptItem[] {
  const aIdx = prev.findIndex(
    (x) => x.type === "assistant_message" && x.id === assistantMessageId,
  );
  if (aIdx >= 0) {
    const arr = [...prev];
    arr.splice(aIdx, 0, row);
    return arr;
  }
  return [...prev, row];
}
