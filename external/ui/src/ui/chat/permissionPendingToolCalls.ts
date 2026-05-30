import type { TranscriptItem } from "./types";

/** Tool call ids blocked on an unresolved permission_prompt in the transcript. */
export function permissionPendingToolCallIds(
  items: readonly TranscriptItem[],
): ReadonlySet<string> {
  const out = new Set<string>();
  for (const it of items) {
    if (it.type !== "permission_prompt" || it.resolved) {
      continue;
    }
    const id = it.payload.toolCall.toolCallId.trim();
    if (id) {
      out.add(id);
    }
  }
  return out;
}
