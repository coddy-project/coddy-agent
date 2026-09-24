import type { TranscriptItem } from "../chat/types";

/**
 * userMsgIndices maps every user message to the 0-based index the server
 * knows it by. A background wake counts as a turn of its own, so a message
 * typed after one keeps the index an edit or a rewind names it with. `base`
 * is the number of prompts before `items[0]` when the list holds only the end
 * of a long history (its window's `turnsBefore`).
 */
export function userMsgIndices(
  items: TranscriptItem[],
  base = 0,
): Map<string, number> {
  const m = new Map<string, number>();
  let idx = base;
  for (const it of items) {
    if (it.type === "user_message") {
      m.set(it.id, idx++);
    } else if (it.type === "background_wake") {
      idx++;
    }
  }
  return m;
}
