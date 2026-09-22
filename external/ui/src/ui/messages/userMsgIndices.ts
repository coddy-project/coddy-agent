import type { TranscriptItem } from "../chat/types";

/**
 * userMsgIndices maps every user message to the 0-based index the server
 * knows it by. A background wake counts as a turn of its own, so a message
 * typed after one keeps the index an edit or a rewind names it with.
 */
export function userMsgIndices(items: TranscriptItem[]): Map<string, number> {
  const m = new Map<string, number>();
  let idx = 0;
  for (const it of items) {
    if (it.type === "user_message") {
      m.set(it.id, idx++);
    } else if (it.type === "background_wake") {
      idx++;
    }
  }
  return m;
}
