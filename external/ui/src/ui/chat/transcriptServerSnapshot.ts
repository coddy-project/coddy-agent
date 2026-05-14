import type { TranscriptItem } from "./types";

/**
 * When GET /messages returns an empty transcript but we already have local rows
 * (shadow or on-screen items for this session), keep local state. This avoids wiping
 * the UI after client-side cancel races a stale or incomplete server read.
 */
export function keepLocalTranscriptIfServerEmpty(p: {
  serverNext: TranscriptItem[];
  sid: string;
  viewingSid: string;
  prevShadow: TranscriptItem[] | undefined;
  prevItems: TranscriptItem[];
}): TranscriptItem[] | null {
  if (p.serverNext.length > 0) {
    return null;
  }
  if (p.prevShadow && p.prevShadow.length > 0) {
    return p.prevShadow.slice();
  }
  if (p.viewingSid.trim() === p.sid.trim() && p.prevItems.length > 0) {
    return p.prevItems.slice();
  }
  return null;
}

