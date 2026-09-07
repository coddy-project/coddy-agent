import type { TranscriptItem } from "./types";
import { parseSessionAssetFiles } from "../skills/stripCoddyAttachments";

type UserMessageFile = NonNullable<
  Extract<TranscriptItem, { type: "user_message" }>["files"]
>[number];

/** Normalize persisted attachment metadata, with XML parsing for old sessions. */
export function sessionMessageFiles(
  rawFiles: unknown,
  rawContent: string,
): UserMessageFile[] {
  if (Array.isArray(rawFiles)) {
    const files: UserMessageFile[] = [];
    for (const raw of rawFiles) {
      if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
      const row = raw as Record<string, unknown>;
      const name = typeof row.name === "string" ? row.name.trim() : "";
      if (!name) continue;
      const mimeRaw = row.mime_type ?? row.mimeType;
      const mimeType =
        typeof mimeRaw === "string" && mimeRaw.trim() !== ""
          ? mimeRaw.trim()
          : "application/octet-stream";
      const previewRaw = row.preview_url ?? row.previewUrl;
      const sizeRaw = row.size_bytes ?? row.sizeBytes;
      files.push({
        name,
        mimeType,
        ...(typeof sizeRaw === "number" &&
        Number.isFinite(sizeRaw) &&
        sizeRaw >= 0
          ? { sizeBytes: sizeRaw }
          : {}),
        ...(typeof previewRaw === "string" && previewRaw.trim() !== ""
          ? { previewUrl: previewRaw.trim() }
          : {}),
      });
    }
    if (files.length > 0) return files;
  }
  return parseSessionAssetFiles(rawContent);
}
