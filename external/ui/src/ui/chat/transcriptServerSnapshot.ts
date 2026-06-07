import type { TranscriptItem } from "./types";

/**
 * True when server row and local row describe the same step for prefix alignment.
 * Assistant rows allow the longer local body when the server snapshot is a prefix
 * (race right after cancel before persistence catches up).
 */
export function transcriptItemsLooselyEqual(
  a: TranscriptItem,
  b: TranscriptItem,
): boolean {
  if (a.type !== b.type) return false;
  switch (a.type) {
    case "user_message":
      return (
        (b as Extract<TranscriptItem, { type: "user_message" }>).content.trim() ===
        a.content.trim()
      );
    case "thinking":
      return (
        (b as Extract<TranscriptItem, { type: "thinking" }>).status === a.status &&
        (b as Extract<TranscriptItem, { type: "thinking" }>).content.trim() ===
          a.content.trim()
      );
    case "assistant_message": {
      const bc = (b as Extract<TranscriptItem, { type: "assistant_message" }>)
        .content;
      const ac = a.content.trim();
      const bt = bc.trim();
      if (ac === bt) return true;
      if (!bt && ac) return true;
      if (ac.startsWith(bt) || bt.startsWith(ac)) return true;
      return false;
    }
    case "tool_call":
      return (
        (b as Extract<TranscriptItem, { type: "tool_call" }>).toolCallId ===
        a.toolCallId
      );
    case "system_notice":
      return (
        (b as Extract<TranscriptItem, { type: "system_notice" }>).message ===
        a.message
      );
    case "memory_copilot":
      return (
        (b as Extract<TranscriptItem, { type: "memory_copilot" }>).memoryRowId ===
          a.memoryRowId &&
        (b as Extract<TranscriptItem, { type: "memory_copilot" }>).userTurnIndex ===
          a.userTurnIndex
      );
    case "permission_prompt": {
      const asv = a as Extract<TranscriptItem, { type: "permission_prompt" }>;
      const bl = b as Extract<TranscriptItem, { type: "permission_prompt" }>;
      if (asv.payload.toolCall.toolCallId !== bl.payload.toolCall.toolCallId) {
        return false;
      }
      if (bl.resolved && !asv.resolved) return true;
      if (!!asv.resolved !== !!bl.resolved) return false;
      return true;
    }
    case "question_prompt": {
      const asv = a as Extract<TranscriptItem, { type: "question_prompt" }>;
      const bl = b as Extract<TranscriptItem, { type: "question_prompt" }>;
      if (asv.payload.requestId !== bl.payload.requestId) return false;
      if (bl.resolved && !asv.resolved) return true;
      if (!!asv.resolved !== !!bl.resolved) return false;
      return true;
    }
    default:
      return false;
  }
}

/** Collapse consecutive identical completed reasoning rows (e.g. after reload merge). */
export function dedupeAdjacentDuplicateThinkingCompleted(
  items: TranscriptItem[],
): TranscriptItem[] {
  const out: TranscriptItem[] = [];
  for (const it of items) {
    const prev = out[out.length - 1];
    if (
      prev?.type === "thinking" &&
      it.type === "thinking" &&
      prev.status === "completed" &&
      it.status === "completed" &&
      prev.content.trim() === it.content.trim()
    ) {
      let durationMs: number | undefined;
      const pd =
        typeof prev.durationMs === "number" &&
        Number.isFinite(prev.durationMs) &&
        prev.durationMs >= 0
          ? prev.durationMs
          : undefined;
      const cd =
        typeof it.durationMs === "number" &&
        Number.isFinite(it.durationMs) &&
        it.durationMs >= 0
          ? it.durationMs
          : undefined;
      if (pd !== undefined || cd !== undefined) {
        durationMs = Math.max(pd ?? 0, cd ?? 0);
      }
      out[out.length - 1] = {
        ...prev,
        ...(durationMs !== undefined ? { durationMs } : {}),
      };
      continue;
    }
    out.push(it);
  }
  return out;
}

/**
 * When the server list is a strict prefix of the local transcript (same rows up to
 * server length), append the local tail so the UI keeps streaming text until reload.
 * When lengths match, replace the last assistant row if local has a longer body.
 */
export function mergeTranscriptPreferLocalSuffix(
  serverNext: TranscriptItem[],
  local: TranscriptItem[] | undefined,
): TranscriptItem[] {
  if (!local || local.length === 0) return serverNext;
  const minLen = Math.min(serverNext.length, local.length);
  for (let i = 0; i < minLen; i++) {
    if (!transcriptItemsLooselyEqual(serverNext[i]!, local[i]!)) {
      return serverNext;
    }
  }
  if (local.length > serverNext.length) {
    return [...serverNext, ...local.slice(serverNext.length)];
  }
  if (local.length === serverNext.length && minLen > 0) {
    const lastS = serverNext[serverNext.length - 1]!;
    const lastL = local[local.length - 1]!;
    if (
      lastS.type === "question_prompt" &&
      lastL.type === "question_prompt" &&
      lastS.payload.requestId === lastL.payload.requestId &&
      lastL.resolved &&
      !lastS.resolved
    ) {
      return [...serverNext.slice(0, -1), lastL];
    }
    if (
      lastS.type === "permission_prompt" &&
      lastL.type === "permission_prompt" &&
      lastS.payload.toolCall.toolCallId === lastL.payload.toolCall.toolCallId &&
      lastL.resolved &&
      !lastS.resolved
    ) {
      return [...serverNext.slice(0, -1), lastL];
    }
    if (lastS.type === "assistant_message" && lastL.type === "assistant_message") {
      const sText = lastS.content;
      const lText = lastL.content;
      if (
        lText.length > sText.length &&
        lText.startsWith(sText)
      ) {
        return [
          ...serverNext.slice(0, -1),
          { ...lastL, streaming: false },
        ];
      }
      if (!sText.trim() && lText.trim()) {
        return [
          ...serverNext.slice(0, -1),
          { ...lastL, streaming: false },
        ];
      }
    }
  }
  return serverNext;
}

/**
 * Copies client-only `files` metadata from local user_message items into merged items.
 * The server never persists `files`, so after a merge the field would be lost.
 * We match by position (nth user_message in local → nth user_message in merged).
 */
export function preserveUserMessageFiles(
  merged: TranscriptItem[],
  local: TranscriptItem[] | undefined,
): TranscriptItem[] {
  if (!local || local.length === 0) return merged;
  const localFiles: NonNullable<Extract<TranscriptItem, { type: "user_message" }>["files"]>[] = [];
  for (const it of local) {
    if (it.type === "user_message" && it.files && it.files.length > 0) {
      localFiles.push(it.files);
    } else if (it.type === "user_message") {
      localFiles.push([]);
    }
  }
  if (localFiles.every((f) => f.length === 0)) return merged;
  let userIdx = 0;
  return merged.map((it) => {
    if (it.type !== "user_message") return it;
    const files = localFiles[userIdx++];
    if (!files || files.length === 0) return it;
    return { ...it, files };
  });
}

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

