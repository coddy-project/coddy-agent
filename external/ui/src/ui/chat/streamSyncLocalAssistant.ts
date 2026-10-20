import type { TranscriptItem } from "./types";

/**
 * True when the transcript already has a non-empty assistant_message under one
 * of the ids: the turn's answer is on screen. A turn's answer may have been
 * written into several rows (its text and reasoning interleaved), and the
 * persisted message that replaces them keeps the id of one, so the end of a
 * turn asks with every id its stream used.
 */
export function transcriptHasFilledAssistant(
  items: TranscriptItem[],
  assistantIds: string | readonly string[],
): boolean {
  const ids = new Set(
    typeof assistantIds === "string" ? [assistantIds] : assistantIds,
  );
  return items.some(
    (it) =>
      it.type === "assistant_message" &&
      ids.has(it.id) &&
      it.content.trim() !== "",
  );
}
