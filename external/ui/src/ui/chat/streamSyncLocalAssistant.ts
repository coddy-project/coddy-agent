import type { TranscriptItem } from "./types";

/** True when the transcript already has a non-empty assistant_message for assistantId. */
export function transcriptHasFilledAssistant(
  items: TranscriptItem[],
  assistantId: string,
): boolean {
  return items.some(
    (it) =>
      it.type === "assistant_message" &&
      it.id === assistantId &&
      it.content.trim() !== "",
  );
}
