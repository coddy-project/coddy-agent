import type { TranscriptItem } from "./types";

/** Local-only rows for interactive prompts (SSE). Persisted transcripts omit them. */
function isQuestionPrompt(
  x: TranscriptItem,
): x is Extract<TranscriptItem, { type: "question_prompt" }> {
  return x.type === "question_prompt";
}

function isPermissionPrompt(
  x: TranscriptItem,
): x is Extract<TranscriptItem, { type: "permission_prompt" }> {
  return x.type === "permission_prompt";
}

function interactivePromptKey(x: TranscriptItem): string {
  if (isQuestionPrompt(x)) {
    return `q:${x.payload.requestId.trim()}`;
  }
  if (isPermissionPrompt(x)) {
    return `p:${x.payload.toolCall.toolCallId.trim()}`;
  }
  return "";
}

function interactiveToolCallId(x: TranscriptItem): string {
  if (isQuestionPrompt(x)) {
    return x.payload.toolCallId?.trim() || "";
  }
  if (isPermissionPrompt(x)) {
    return x.payload.toolCall.toolCallId.trim();
  }
  return "";
}

/**
 * Re-insert question_prompt and permission_prompt rows from the client shadow after merging server messages.
 * The HTTP messages list contains no interactive prompt rows, so naive prefix merge strips them
 * and breaks the transcript when a tool row aligns but the following local row differs.
 */
export function reattachLocalQuestionPrompts(
  merged: TranscriptItem[],
  local: TranscriptItem[] | undefined,
): TranscriptItem[] {
  if (!local?.length) {
    return merged;
  }
  const extras = local.filter(
    (x) => isQuestionPrompt(x) || isPermissionPrompt(x),
  );
  if (extras.length === 0) {
    return merged;
  }
  const have = new Set(
    merged
      .filter((x) => isQuestionPrompt(x) || isPermissionPrompt(x))
      .map(interactivePromptKey)
      .filter(Boolean),
  );
  let out = [...merged];
  for (const q of extras) {
    const key = interactivePromptKey(q);
    if (!key || have.has(key)) {
      continue;
    }
    have.add(key);
    const tcid = interactiveToolCallId(q);
    let insertAt = out.length;
    if (tcid) {
      const idx = out.findIndex(
        (x): x is Extract<TranscriptItem, { type: "tool_call" }> =>
          x.type === "tool_call" && x.toolCallId === tcid,
      );
      if (idx >= 0) {
        insertAt = idx + 1;
      }
    }
    out.splice(insertAt, 0, q);
  }
  return out;
}
