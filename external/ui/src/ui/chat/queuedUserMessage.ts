import { parseSessionAssetFiles } from "../skills/stripCoddyAttachments";
import type { TranscriptItem } from "./types";

type UserMessageItem = Extract<TranscriptItem, { type: "user_message" }>;

/**
 * The transcript row for `event: user_message`: a queued message the agent has
 * just read, or a deferred one whose prompt starts now, where it entered the
 * conversation. The frame carries the message as the model got it; the images
 * it brought are named in its `<coddy_session_assets>` note, so the row shows
 * them as file chips until the transcript read after the turn brings their
 * thumbnails. Null for a frame with nothing to show.
 */
export function queuedUserMessageItem(
  data: string,
  id: string,
  createdAtUtc: string,
): UserMessageItem | null {
  let text = "";
  try {
    const raw = JSON.parse(data) as { content?: { text?: string } };
    text = String(raw?.content?.text || "");
  } catch {
    return null;
  }
  if (!text.trim()) return null;
  const files = parseSessionAssetFiles(text);
  return {
    id,
    type: "user_message",
    content: text,
    createdAtUtc,
    ...(files.length > 0 ? { files } : {}),
  };
}
