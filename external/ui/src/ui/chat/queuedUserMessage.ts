import {
  parseSessionAssetFiles,
  sessionAssetNames,
} from "../skills/stripCoddyAttachments";
import type { TranscriptItem } from "./types";

type UserMessageItem = Extract<TranscriptItem, { type: "user_message" }>;

/**
 * The transcript row for `event: user_message`: a queued message the agent has
 * just read, or a deferred one whose prompt starts now, where it entered the
 * conversation. The frame carries the message as the model got it; the images
 * it brought are named in its `<coddy_session_assets>` note and saved before the
 * frame goes out, so the row opens each from the session's assets at once, and
 * the transcript read after the turn brings their thumbnails. Without a session
 * to ask (`sessionId`), the row names them. Null for a frame with nothing to show.
 */
export function queuedUserMessageItem(
  data: string,
  id: string,
  createdAtUtc: string,
  sessionId?: string,
): UserMessageItem | null {
  let text = "";
  try {
    const raw = JSON.parse(data) as { content?: { text?: string } };
    text = String(raw?.content?.text || "");
  } catch {
    return null;
  }
  if (!text.trim()) return null;
  const assets = sessionId ? sessionAssetNames(text) : [];
  const files = parseSessionAssetFiles(text).map((file, i) => {
    const asset = assets[i];
    if (!asset || !sessionId) return file;
    return {
      ...file,
      url: `/coddy/sessions/${encodeURIComponent(sessionId)}/assets/${encodeURIComponent(asset)}`,
    };
  });
  return {
    id,
    type: "user_message",
    content: text,
    createdAtUtc,
    ...(files.length > 0 ? { files } : {}),
  };
}
